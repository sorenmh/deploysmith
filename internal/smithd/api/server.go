package api

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/shared/logging"
	"github.com/sorenmh/deploysmith/internal/shared/tfbundle"
	"github.com/sorenmh/deploysmith/internal/smithd/auth"
	"github.com/sorenmh/deploysmith/internal/smithd/config"
	"github.com/sorenmh/deploysmith/internal/smithd/db"
	"github.com/sorenmh/deploysmith/internal/smithd/events"
	"github.com/sorenmh/deploysmith/internal/smithd/flux"
	"github.com/sorenmh/deploysmith/internal/smithd/gitops"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/push"
	"github.com/sorenmh/deploysmith/internal/smithd/service"
	"github.com/sorenmh/deploysmith/internal/smithd/slack"
	"github.com/sorenmh/deploysmith/internal/smithd/storage"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
	"github.com/sorenmh/deploysmith/internal/smithd/web"
	"gopkg.in/yaml.v3"
)

// Version is the smithd version reported by /health, set at startup
var Version = "dev"

// Server represents the HTTP server
type Server struct {
	cfg              *config.Config
	db               *db.DB
	router           *chi.Mux
	appStore         *store.ApplicationStore
	versionStore     *store.VersionStore
	deploymentStore  *store.DeploymentStore
	policyStore      *store.PolicyStore
	environmentStore *store.EnvironmentStore
	pushStore        *store.PushSubscriptionStore
	storage          *storage.S3Storage
	publisher        events.Publisher
	svc              *service.Service
	login            *auth.Login // nil unless login with GitHub is configured
	uiRouter         *chi.Mux    // set when the UI has its own listener
}

// NewServer creates a new HTTP server
func NewServer(cfg *config.Config, database *db.DB) *Server {
	s3Storage, err := storage.NewS3Storage(cfg.S3Bucket, cfg.S3Region, cfg.AWSEndpoint)
	if err != nil {
		log.Fatalf("Failed to initialize S3 storage: %v", err)
	}

	gitopsService := gitops.NewService(cfg.GitopsRepo, cfg.GitopsSSHKeyPath)

	publisher := events.New(cfg.NATSUrl)

	s := &Server{
		cfg:              cfg,
		db:               database,
		router:           chi.NewRouter(),
		appStore:         store.NewApplicationStore(database),
		versionStore:     store.NewVersionStore(database),
		deploymentStore:  store.NewDeploymentStore(database),
		policyStore:      store.NewPolicyStore(database),
		environmentStore: store.NewEnvironmentStore(database),
		pushStore:        store.NewPushSubscriptionStore(database),
		storage:          s3Storage,
		publisher:        publisher,
	}

	deps := service.Deps{
		Apps:                  s.appStore,
		Versions:              s.versionStore,
		Deployments:           s.deploymentStore,
		Policies:              s.policyStore,
		Environments:          s.environmentStore,
		Storage:               s3Storage,
		Gitops:                gitopsService,
		Publisher:             publisher,
		FluxReconciler:        flux.NewReconciler(cfg.FluxWebhookURL, cfg.FluxWebhookToken),
		FluxKustomizationName: cfg.FluxKustomizationName,
		Slack:                 slack.NewNotifier(cfg.SlackWebhookURL),
		Push:                  push.NewNotifier(s.pushStore, cfg.VAPIDPublicKey, cfg.VAPIDPrivateKey, vapidSubject(cfg)),
		PushSubscriptions:     s.pushStore,
		UIBaseURL:             cfg.UIPublicURL,
	}
	// Assign only when available, so the interface is nil rather than
	// holding a nil pointer
	if poller := flux.NewKustomizationPoller(cfg.FluxNamespace); poller != nil {
		deps.FluxPoller = poller
	}

	// Terraform apps, only with OpenBao
	deps.TerraformEnvironments = store.NewTerraformStore(database)
	deps.TerraformSharedBackend = models.TerraformBackend{Bucket: cfg.TerraformStateBucket, Region: cfg.TerraformStateRegion, LockTable: cfg.TerraformLockTable}
	if deps.TerraformManifests, err = terraformManifestOptions(cfg); err != nil {
		log.Fatalf("Failed to configure Terraform support: %v", err)
	}
	deps.TerraformRoots = terraformRootOptions(cfg)
	if deps.Terraform, err = newTerraformProvisioner(cfg); err != nil {
		log.Fatalf("Failed to configure OpenBao: %v", err)
	}
	if deps.Terraform != nil {
		deps.TerraformGitops = gitopsService
		log.Printf("Terraform support enabled (OpenBao at %s)", cfg.OpenBaoAddr)
	} else {
		log.Printf("Terraform support disabled: OPENBAO_ADDR is not set")
	}
	s.svc = service.New(deps)
	s.login = newLogin(cfg, database)

	s.setupRoutes()
	return s
}

// setupRoutes configures all HTTP routes
func (s *Server) setupRoutes() {
	// Global middleware
	s.router.Use(Logger)

	// Health check (no auth required)
	s.router.Get("/health", s.handleHealth)

	// API routes (auth required)
	s.router.Route("/api/v1", func(r chi.Router) {
		r.Use(CORS)
		r.Use(ContentType)

		// Signing in to smithctl, before there is a session
		r.Post("/auth/device", s.handleStartDeviceLogin)
		r.Post("/auth/device/token", s.handleDeviceToken)

		r.Group(func(r chi.Router) {
			r.Use(Auth(s.cfg.APIKeys, s.login))
			s.setupAuthenticatedRoutes(r)
		})
	})

	s.setupUI()
}

// setupAuthenticatedRoutes configures the API routes that need an API key
// or a session
func (s *Server) setupAuthenticatedRoutes(r chi.Router) {
	// Who am I, and logging out of a session
	r.Get("/auth/whoami", s.handleWhoAmI)
	r.Post("/auth/logout", s.handleLogout)

	// Application routes
	r.Post("/apps", s.handleRegisterApp)
	r.Get("/apps", s.handleListApps)
	r.Get("/apps/{appId}", s.handleGetApp)

	// Version routes
	r.Post("/apps/{appId}/versions/draft", s.handleDraftVersion)
	r.Post("/apps/{appId}/versions/{versionId}/publish", s.handlePublishVersion)
	r.Get("/apps/{appId}/versions", s.handleListVersions)
	r.Get("/apps/{appId}/versions/{versionId}", s.handleGetVersion)

	// Deployment routes
	r.Post("/apps/{appId}/versions/{versionId}/deploy", s.handleDeployVersion)
	r.Get("/deployments", s.handleListDeployments)
	r.Get("/deployments/{deploymentId}", s.handleGetDeployment)
	r.Get("/overview", s.handleOverview)

	// Environment routes
	r.Post("/environments", s.handleCreateEnvironment)
	r.Get("/environments", s.handleListEnvironments)
	r.Get("/environments/{environmentId}", s.handleGetEnvironment)
	r.Put("/environments/{environmentId}", s.handleUpdateEnvironment)
	r.Delete("/environments/{environmentId}", s.handleDeleteEnvironment)

	// Policy routes
	r.Post("/apps/{appId}/policies", s.handleCreatePolicy)
	r.Get("/apps/{appId}/policies", s.handleListPolicies)
	r.Patch("/apps/{appId}/policies/{policyId}", s.handleUpdatePolicy)
	r.Delete("/apps/{appId}/policies/{policyId}", s.handleDeletePolicy)

	// Terraform routes
	r.Post("/apps/{appId}/terraform/environments", s.handleEnableTerraform)
	r.Get("/apps/{appId}/terraform/environments", s.handleListTerraformEnvironments)
	r.Patch("/apps/{appId}/terraform/environments/{environment}", s.handleUpdateTerraform)
	r.Post("/apps/{appId}/terraform/sync", s.handleSyncTerraform)

	// Admin routes (DB migration between drivers)
	r.Get("/admin/db-export", s.handleDBExport)
	r.Post("/admin/db-import", s.handleDBImport)
}

// setupUI mounts the web UI, served when it signs people in with GitHub or
// when an authenticating proxy is configured
func (s *Server) setupUI() {
	authCfg := web.AuthConfig{
		UserHeader:     s.cfg.UIUserHeader,
		AllowedUsers:   s.cfg.UIAllowedUsers,
		TrustedOrigins: s.cfg.UITrustedOrigins,
		Login:          s.login,
		PublicURL:      s.cfg.UIPublicURL,
	}
	if authCfg.UserHeader == "" && authCfg.Login == nil {
		return
	}

	ui, err := web.New(s.svc, authCfg)
	if err != nil {
		log.Fatalf("Failed to set up web UI: %v", err)
	}
	router := s.router
	if s.cfg.UIListenAddr != "" {
		s.uiRouter = chi.NewRouter()
		s.uiRouter.Use(Logger)
		router = s.uiRouter
	}
	router.Mount(web.Prefix, ui)
	router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, web.Prefix+"/", http.StatusFound)
	})
	if s.login != nil {
		log.Printf("Web UI enabled at %s (login with GitHub)", web.Prefix)
	} else {
		log.Printf("Web UI enabled at %s (user header: %s)", web.Prefix, s.cfg.UIUserHeader)
	}
}

// newLogin returns the login with GitHub service, or nil if it is not
// configured
func newLogin(cfg *config.Config, database *db.DB) *auth.Login {
	if !cfg.GitHubLoginEnabled() {
		return nil
	}
	log.Printf("Login with GitHub enabled (%s)", cfg.GitHubURL)
	return auth.NewLogin(auth.Config{
		GitHub:       auth.NewGitHub(cfg.GitHubClientID, cfg.GitHubClientSecret, cfg.GitHubURL, cfg.GitHubAPIURL),
		AllowedOrg:   cfg.GitHubAllowedOrg,
		AllowedTeam:  cfg.GitHubAllowedTeam,
		AllowedUsers: cfg.GitHubAllowedUsers,
		SessionTTL:   cfg.SessionTTL,
	}, store.NewUserStore(database), store.NewSessionStore(database))
}

// Start starts the HTTP server
func (s *Server) Start() error {
	go s.svc.WatchStaleDeployments(context.Background())
	go s.svc.SyncAllTerraform(context.Background())

	if s.uiRouter != nil {
		go func() {
			log.Printf("Serving web UI on %s", s.cfg.UIListenAddr)
			log.Fatalf("Web UI server failed: %v", http.ListenAndServe(s.cfg.UIListenAddr, s.uiRouter))
		}()
	}

	addr := fmt.Sprintf(":%s", s.cfg.Port)
	log.Printf("Starting server on %s", addr)
	return http.ListenAndServe(addr, s.router)
}

// Health check handler
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":  "healthy",
		"version": Version,
		"checks": map[string]string{
			"database": "ok",
			"s3":       "ok",
			"gitops":   "ok",
		},
	}

	// Check database
	if err := s.db.Ping(); err != nil {
		health["status"] = "unhealthy"
		health["checks"].(map[string]string)["database"] = "error"
		writeJSON(w, http.StatusServiceUnavailable, health)
		return
	}

	writeJSON(w, http.StatusOK, health)
}

// Application handlers
func (s *Server) handleRegisterApp(w http.ResponseWriter, r *http.Request) {
	var req models.RegisterAppRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	app, err := s.svc.RegisterApp(req.Name)
	if err != nil {
		writeServiceError(w, err, "Failed to create application")
		return
	}

	writeJSON(w, http.StatusCreated, app)
}

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	// Parse pagination parameters
	limit := 50
	offset := 0

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	apps, total, err := s.appStore.List(limit, offset)
	if err != nil {
		log.Printf("Failed to list applications: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list applications")
		return
	}

	resp := models.ListAppsResponse{
		Apps:   apps,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")

	app, err := s.appStore.GetByID(appID)
	if err != nil {
		if err.Error() == "application not found" {
			writeError(w, http.StatusNotFound, "not_found", "Application not found")
			return
		}
		log.Printf("Failed to get application: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to get application")
		return
	}

	// Get current versions for each environment
	currentVersions, err := s.appStore.GetCurrentVersions(appID)
	if err != nil {
		log.Printf("Failed to get current versions: %v", err)
		// Continue without current versions rather than failing
		currentVersions = make(map[string]string)
	}

	resp := models.GetAppResponse{
		ID:             app.ID,
		Name:           app.Name,
		CreatedAt:      app.CreatedAt,
		CurrentVersion: currentVersions,
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDraftVersion(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")

	// Verify application exists
	app, err := s.appStore.GetByID(appID)
	if err != nil {
		if err.Error() == "application not found" {
			writeError(w, http.StatusNotFound, "not_found", "Application not found")
			return
		}
		log.Printf("Failed to get application: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to get application")
		return
	}

	var req models.DraftVersionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	if req.VersionID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "versionId is required")
		return
	}

	// Validate metadata
	if req.Metadata.GitSHA == "" || req.Metadata.GitBranch == "" || req.Metadata.Timestamp == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "metadata must include gitSha, gitBranch, and timestamp")
		return
	}

	if req.ArtifactType == "" {
		req.ArtifactType = models.ArtifactManifests
	}
	if !models.ValidArtifactType(req.ArtifactType) {
		writeError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("Unknown artifactType '%s' (supported: %s, %s)", req.ArtifactType, models.ArtifactManifests, models.ArtifactTerraform))
		return
	}

	// Check if version already exists
	existing, _ := s.versionStore.GetByVersionID(appID, req.VersionID)
	if existing != nil {
		writeError(w, http.StatusConflict, "conflict", fmt.Sprintf("Version '%s' already exists", req.VersionID))
		return
	}

	// Create version record
	version, err := s.versionStore.Create(appID, req.VersionID, req.ArtifactType, req.Metadata)
	if err != nil {
		log.Printf("Failed to create version: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create version")
		return
	}

	// Generate presigned URL for the archive upload
	uploadURL, err := s.storage.GeneratePresignedURL(app.Name, req.VersionID, archiveName(version.ArtifactType))
	if err != nil {
		log.Printf("Failed to generate presigned URL: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to generate upload URL")
		return
	}

	resp := models.DraftVersionResponse{
		VersionID:     version.VersionID,
		UploadURL:     uploadURL,
		UploadExpires: version.CreatedAt.Add(5 * 60 * 1000000000), // 5 minutes
		Status:        version.Status,
		ArtifactType:  version.ArtifactType,
	}

	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handlePublishVersion(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	versionID := chi.URLParam(r, "versionId")

	logging.LogInfoCtx(r.Context(), fmt.Sprintf("Publishing version %s for app %s", versionID, appID))

	// Verify application exists
	app, err := s.appStore.GetByID(appID)
	if err != nil {
		if err.Error() == "application not found" {
			writeError(w, http.StatusNotFound, "not_found", "Application not found")
			return
		}
		logging.LogErrorCtx(r.Context(), "Failed to get application: "+err.Error(), err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to get application")
		return
	}

	// Get version
	version, err := s.versionStore.GetByVersionID(appID, versionID)
	if err != nil {
		if err.Error() == "version not found" {
			writeError(w, http.StatusNotFound, "not_found", "Version not found")
			return
		}
		logging.LogErrorCtx(r.Context(), "Failed to get version: "+err.Error(), err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to get version")
		return
	}

	// Check if already published
	if version.Status == "published" {
		writeError(w, http.StatusConflict, "conflict", "Version is already published")
		return
	}

	var manifestFiles []string
	var ok bool
	if version.ArtifactType == models.ArtifactTerraform {
		manifestFiles, ok = s.validateTerraformDraft(w, r, app, versionID)
	} else {
		manifestFiles, ok = s.validateManifestDraft(w, r, app, versionID)
	}
	if !ok {
		return
	}

	// Move files from drafts to published
	if err := s.storage.MoveVersion(app.Name, versionID); err != nil {
		logging.LogErrorCtx(r.Context(), "Failed to move version to published: "+err.Error(), err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to publish version")
		return
	}

	// Update version status
	if err := s.versionStore.UpdateStatus(version.ID, "published"); err != nil {
		logging.LogErrorCtx(r.Context(), "Failed to update version status: "+err.Error(), err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to update version status")
		return
	}

	s.publisher.Publish(events.Event{
		Type:      events.EventVersionPublished,
		AppName:   app.Name,
		AppID:     appID,
		VersionID: version.VersionID,
		Timestamp: time.Now(),
	})

	// Refresh version to get updated fields
	version, _ = s.versionStore.GetByVersionID(appID, versionID)

	s.svc.VersionPublished(r.Context(), app, version)
	s.svc.TriggerAutoDeploys(r.Context(), app, version)

	resp := models.PublishVersionResponse{
		VersionID:     version.VersionID,
		Status:        version.Status,
		ArtifactType:  version.ArtifactType,
		PublishedAt:   *version.PublishedAt,
		ManifestFiles: manifestFiles,
	}

	writeJSON(w, http.StatusOK, resp)
}

// validateManifestDraft validates the manifests uploaded for a draft version
// and returns their names. It writes the error response and returns false
// if they are invalid.
func (s *Server) validateManifestDraft(w http.ResponseWriter, r *http.Request, app *models.Application, versionID string) ([]string, bool) {
	// List files in draft location
	files, err := s.storage.ListFiles(app.Name, versionID, false)
	if err != nil {
		logging.LogErrorCtx(r.Context(), "Failed to list draft files: "+err.Error(), err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access draft files")
		return nil, false
	}

	logging.LogInfoCtx(r.Context(), fmt.Sprintf("Found %d files in draft location for version %s: %v", len(files), versionID, files))

	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "No manifest files uploaded")
		return nil, false
	}

	// Check if we have a tarball that needs to be extracted
	manifestFiles := []string{}
	var tarballFiles map[string][]byte

	// Look for manifests.tar.gz
	hasTarball := false
	for _, file := range files {
		if file == "manifests.tar.gz" {
			hasTarball = true
			logging.LogInfoCtx(r.Context(), "Found tarball, extracting files...")

			// Get and extract tarball
			reader, err := s.storage.GetFile(app.Name, versionID, file, false)
			if err != nil {
				logging.LogErrorCtx(r.Context(), "Failed to get tarball "+file+": "+err.Error(), err)
				writeError(w, http.StatusInternalServerError, "internal_error", "Failed to read manifest files")
				return nil, false
			}
			defer reader.Close()

			tarballFiles, err = s.extractTarball(reader)
			if err != nil {
				logging.LogErrorCtx(r.Context(), "Failed to extract tarball: "+err.Error(), err)
				writeError(w, http.StatusInternalServerError, "internal_error", "Failed to extract manifest files")
				return nil, false
			}

			logging.LogInfoCtx(r.Context(), fmt.Sprintf("Extracted %d files from tarball: %v", len(tarballFiles), getKeys(tarballFiles)))
			break
		}
	}

	// Process files (either from tarball or individual uploads)
	if hasTarball {
		// Validate files from tarball
		for filename, content := range tarballFiles {
			logging.LogDebugCtx(r.Context(), fmt.Sprintf("Processing extracted file: %s", filename))
			if strings.HasSuffix(filename, ".yaml") || strings.HasSuffix(filename, ".yml") {
				logging.LogInfoCtx(r.Context(), fmt.Sprintf("File %s is a YAML file, validating...", filename))
				logging.LogInfoCtx(r.Context(), fmt.Sprintf("Read %d bytes from file %s", len(content), filename))

				// Validate YAML syntax
				var yamlContent interface{}
				if err := yaml.Unmarshal(content, &yamlContent); err != nil {
					logging.LogErrorCtx(r.Context(), fmt.Sprintf("YAML validation failed for file %s: %v", filename, err), err)
					writeError(w, http.StatusBadRequest, "validation_failed", fmt.Sprintf("Invalid YAML in %s: %v", filename, err))
					return nil, false
				}

				logging.LogDebugCtx(r.Context(), fmt.Sprintf("File %s validated successfully", filename))
				manifestFiles = append(manifestFiles, filename)
			} else {
				logging.LogInfoCtx(r.Context(), fmt.Sprintf("Skipping non-YAML file: %s", filename))
			}
		}
	} else {
		// Validate individual files
		for _, file := range files {
			logging.LogDebugCtx(r.Context(), fmt.Sprintf("Processing file: %s", file))
			if strings.HasSuffix(file, ".yaml") || strings.HasSuffix(file, ".yml") {
				logging.LogInfoCtx(r.Context(), fmt.Sprintf("File %s is a YAML file, validating...", file))
				// Get file content
				reader, err := s.storage.GetFile(app.Name, versionID, file, false)
				if err != nil {
					logging.LogErrorCtx(r.Context(), fmt.Sprintf("Failed to get file %s: %v", file, err), err)
					writeError(w, http.StatusInternalServerError, "internal_error", "Failed to read manifest files")
					return nil, false
				}
				defer reader.Close()

				// Read content
				content, err := io.ReadAll(reader)
				if err != nil {
					logging.LogErrorCtx(r.Context(), fmt.Sprintf("Failed to read file %s: %v", file, err), err)
					writeError(w, http.StatusInternalServerError, "internal_error", "Failed to read manifest files")
					return nil, false
				}

				logging.LogInfoCtx(r.Context(), fmt.Sprintf("Read %d bytes from file %s", len(content), file))

				// Validate YAML syntax
				var yamlContent interface{}
				if err := yaml.Unmarshal(content, &yamlContent); err != nil {
					logging.LogErrorCtx(r.Context(), fmt.Sprintf("YAML validation failed for file %s: %v", file, err), err)
					writeError(w, http.StatusBadRequest, "validation_failed", fmt.Sprintf("Invalid YAML in %s: %v", file, err))
					return nil, false
				}

				logging.LogDebugCtx(r.Context(), fmt.Sprintf("File %s validated successfully", file))
				manifestFiles = append(manifestFiles, file)
			} else {
				logging.LogInfoCtx(r.Context(), fmt.Sprintf("Skipping non-YAML file: %s", file))
			}
		}
	}

	if len(manifestFiles) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "No valid YAML manifest files found")
		return nil, false
	}

	return manifestFiles, true
}

// validateTerraformDraft validates the OpenTofu bundle uploaded for a draft
// version, with the same rules forge applies before uploading, and returns
// its file names. It writes the error response and returns false if the
// bundle is missing or invalid.
func (s *Server) validateTerraformDraft(w http.ResponseWriter, r *http.Request, app *models.Application, versionID string) ([]string, bool) {
	files, err := s.storage.ListFiles(app.Name, versionID, false)
	if err != nil {
		logging.LogErrorCtx(r.Context(), "Failed to list draft files: "+err.Error(), err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access draft files")
		return nil, false
	}
	if !slices.Contains(files, tfbundle.ArchiveName) {
		writeError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("No %s uploaded (upload it with forge upload --terraform)", tfbundle.ArchiveName))
		return nil, false
	}

	reader, err := s.storage.GetFile(app.Name, versionID, tfbundle.ArchiveName, false)
	if err != nil {
		logging.LogErrorCtx(r.Context(), "Failed to get terraform bundle: "+err.Error(), err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to read terraform bundle")
		return nil, false
	}
	defer reader.Close()

	bundleFiles, err := tfbundle.Read(reader)
	if err == nil {
		_, err = tfbundle.Validate(bundleFiles)
	}
	if err != nil {
		logging.LogInfoCtx(r.Context(), "Rejected terraform bundle: "+err.Error())
		writeError(w, http.StatusBadRequest, "validation_failed", err.Error())
		return nil, false
	}

	names := slices.Sorted(maps.Keys(bundleFiles))
	logging.LogInfoCtx(r.Context(), fmt.Sprintf("Validated terraform bundle with %d files: %v", len(names), names))
	return names, true
}

// archiveName is the file a draft version's artifact is uploaded as
func archiveName(artifactType string) string {
	if artifactType == models.ArtifactTerraform {
		return tfbundle.ArchiveName
	}
	return "manifests.tar.gz"
}

func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")

	// Verify application exists
	_, err := s.appStore.GetByID(appID)
	if err != nil {
		if err.Error() == "application not found" {
			writeError(w, http.StatusNotFound, "not_found", "Application not found")
			return
		}
		log.Printf("Failed to get application: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to get application")
		return
	}

	// Parse pagination parameters
	limit := 50
	offset := 0

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	// List versions
	versions, total, err := s.versionStore.List(appID, limit, offset)
	if err != nil {
		log.Printf("Failed to list versions: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list versions")
		return
	}

	// Build response with deployment info
	versionsWithDeployment := []models.VersionWithDeployment{}
	for _, v := range versions {
		deployedTo, err := s.versionStore.GetDeployedEnvironments(v.ID)
		if err != nil {
			log.Printf("Failed to get deployed environments for version %s: %v", v.ID, err)
			deployedTo = []string{}
		}

		versionsWithDeployment = append(versionsWithDeployment, models.VersionWithDeployment{
			Version:    v,
			DeployedTo: deployedTo,
		})
	}

	resp := models.ListVersionsResponse{
		Versions: versionsWithDeployment,
		Total:    total,
		Limit:    limit,
		Offset:   offset,
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	versionID := chi.URLParam(r, "versionId")

	// Verify application exists
	app, err := s.appStore.GetByID(appID)
	if err != nil {
		if err.Error() == "application not found" {
			writeError(w, http.StatusNotFound, "not_found", "Application not found")
			return
		}
		log.Printf("Failed to get application: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to get application")
		return
	}

	// Get version
	version, err := s.versionStore.GetByVersionID(appID, versionID)
	if err != nil {
		if err.Error() == "version not found" {
			writeError(w, http.StatusNotFound, "not_found", "Version not found")
			return
		}
		log.Printf("Failed to get version: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to get version")
		return
	}

	// Get manifest files
	manifestFiles := []string{}
	if version.Status == "published" {
		files, err := s.storage.ListFiles(app.Name, versionID, true)
		if err != nil {
			log.Printf("Failed to list manifest files: %v", err)
			// Continue without manifest files rather than failing
		} else {
			manifestFiles = files
		}
	}

	// Get deployed environments
	deployedTo, err := s.versionStore.GetDeployedEnvironments(version.ID)
	if err != nil {
		log.Printf("Failed to get deployed environments: %v", err)
		deployedTo = []string{}
	}

	resp := models.GetVersionResponse{
		VersionID:    version.VersionID,
		Status:       version.Status,
		ArtifactType: version.ArtifactType,
		CreatedAt:    version.CreatedAt,
		PublishedAt:  version.PublishedAt,
		Metadata: models.VersionMetadata{
			GitSHA:       version.GitSHA,
			GitBranch:    version.GitBranch,
			GitCommitter: version.GitCommitter,
			BuildNumber:  version.BuildNumber,
			Timestamp:    version.MetadataTimestamp.Format("2006-01-02T15:04:05Z07:00"),
		},
		ManifestFiles: manifestFiles,
		DeployedTo:    deployedTo,
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDeployVersion(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	versionID := chi.URLParam(r, "versionId")

	var req models.DeployVersionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	// A signed-in user is who triggered it, whatever the client says; API
	// key clients name themselves
	triggeredBy := req.TriggeredBy
	if id, ok := auth.FromContext(r.Context()); ok && id.Kind == auth.KindUser {
		triggeredBy = id.Login
	}

	deployment, err := s.svc.DeployVersion(r.Context(), appID, versionID, req.Environment, triggeredBy)
	if err != nil {
		writeServiceError(w, err, "Deployment failed")
		return
	}

	resp := models.DeployVersionResponse{
		DeploymentID:    deployment.ID,
		VersionID:       versionID,
		Environment:     deployment.Environment,
		Status:          deployment.Status,
		GitopsCommitSHA: deployment.GitopsCommitSHA,
		StartedAt:       deployment.StartedAt,
		Terraform:       deployment.Terraform,
	}

	writeJSON(w, http.StatusAccepted, resp)
}

// extractTarball extracts files from a gzipped tarball
func (s *Server) extractTarball(reader io.ReadCloser) (map[string][]byte, error) {
	gzReader, err := gzip.NewReader(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)
	files := make(map[string][]byte)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar header: %w", err)
		}

		// Only process regular files
		if header.Typeflag == tar.TypeReg {
			content, err := io.ReadAll(tarReader)
			if err != nil {
				return nil, fmt.Errorf("failed to read file %s: %w", header.Name, err)
			}
			files[header.Name] = content
		}
	}

	return files, nil
}

// Environment handlers

// handleCreateEnvironment creates a new environment
func (s *Server) handleCreateEnvironment(w http.ResponseWriter, r *http.Request) {
	var req models.CreateEnvironmentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	env, err := s.svc.CreateEnvironment(req)
	if err != nil {
		writeServiceError(w, err, "Failed to create environment")
		return
	}

	writeJSON(w, http.StatusCreated, env)
}

// handleListEnvironments lists all environments
func (s *Server) handleListEnvironments(w http.ResponseWriter, r *http.Request) {
	environments, err := s.svc.ListEnvironments()
	if err != nil {
		writeServiceError(w, err, "Failed to list environments")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"environments": environments,
	})
}

// handleGetEnvironment gets an environment by ID
func (s *Server) handleGetEnvironment(w http.ResponseWriter, r *http.Request) {
	env, err := s.svc.GetEnvironment(chi.URLParam(r, "environmentId"))
	if err != nil {
		writeServiceError(w, err, "Failed to get environment")
		return
	}

	writeJSON(w, http.StatusOK, env)
}

// handleUpdateEnvironment updates an environment
func (s *Server) handleUpdateEnvironment(w http.ResponseWriter, r *http.Request) {
	var req models.UpdateEnvironmentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	env, err := s.svc.UpdateEnvironment(chi.URLParam(r, "environmentId"), req)
	if err != nil {
		writeServiceError(w, err, "Failed to update environment")
		return
	}

	writeJSON(w, http.StatusOK, env)
}

// handleDeleteEnvironment deletes an environment
func (s *Server) handleDeleteEnvironment(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeleteEnvironment(chi.URLParam(r, "environmentId")); err != nil {
		writeServiceError(w, err, "Failed to delete environment")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Admin handlers (DB migration between drivers)

// handleDBExport exports all rows from the database as JSON.
func (s *Server) handleDBExport(w http.ResponseWriter, r *http.Request) {
	dumps, err := s.db.Dump()
	if err != nil {
		log.Printf("Failed to export database: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to export database")
		return
	}

	writeJSON(w, http.StatusOK, dumps)
}

// handleDBImport imports a database dump previously produced by handleDBExport.
// The target database must be freshly migrated and empty.
func (s *Server) handleDBImport(w http.ResponseWriter, r *http.Request) {
	var dumps []db.TableDump
	if err := decodeJSON(r, &dumps); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	if err := s.db.EnsureEmpty(); err != nil {
		writeError(w, http.StatusBadRequest, "not_empty", err.Error())
		return
	}

	if err := s.db.Load(dumps); err != nil {
		log.Printf("Failed to import database: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to import database")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"imported": true})
}

// getKeys returns the keys of a map as a slice
func getKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// Helper to decode JSON request
func decodeJSON(r *http.Request, v interface{}) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// vapidSubject is the contact push services get with every notification: the
// configured subject, or else the UI's public URL
func vapidSubject(cfg *config.Config) string {
	if cfg.VAPIDSubject != "" {
		return cfg.VAPIDSubject
	}
	return cfg.UIPublicURL
}
