package api

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/sorenmh/deploysmith/internal/smithd/config"
	"github.com/sorenmh/deploysmith/internal/smithd/db"
	"github.com/sorenmh/deploysmith/internal/smithd/gitops"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/storage"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
	"github.com/go-chi/chi/v5"
	"gopkg.in/yaml.v3"
)

// Server represents the HTTP server
type Server struct {
	cfg             *config.Config
	db              *db.DB
	router          *chi.Mux
	appStore        *store.ApplicationStore
	versionStore    *store.VersionStore
	deploymentStore *store.DeploymentStore
	policyStore     *store.PolicyStore
	storage         *storage.S3Storage
	gitops          *gitops.Service
}

// NewServer creates a new HTTP server
func NewServer(cfg *config.Config, database *db.DB) *Server {
	s3Storage, err := storage.NewS3Storage(cfg.S3Bucket, cfg.S3Region, cfg.AWSEndpoint)
	if err != nil {
		log.Fatalf("Failed to initialize S3 storage: %v", err)
	}

	gitopsService := gitops.NewService(cfg.GitopsRepo, cfg.GitopsSSHKeyPath)

	s := &Server{
		cfg:             cfg,
		db:              database,
		router:          chi.NewRouter(),
		appStore:        store.NewApplicationStore(database.DB),
		versionStore:    store.NewVersionStore(database.DB),
		deploymentStore: store.NewDeploymentStore(database.DB),
		policyStore:     store.NewPolicyStore(database.DB),
		storage:         s3Storage,
		gitops:          gitopsService,
	}

	s.setupRoutes()
	return s
}

// setupRoutes configures all HTTP routes
func (s *Server) setupRoutes() {
	// Global middleware
	s.router.Use(Logger)
	s.router.Use(CORS)
	s.router.Use(ContentType)

	// Health check (no auth required)
	s.router.Get("/health", s.handleHealth)

	// API routes (auth required)
	s.router.Route("/api/v1", func(r chi.Router) {
		r.Use(Auth(s.cfg.APIKeys))

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

		// Policy routes
		r.Post("/apps/{appId}/policies", s.handleCreatePolicy)
		r.Get("/apps/{appId}/policies", s.handleListPolicies)
		r.Delete("/apps/{appId}/policies/{policyId}", s.handleDeletePolicy)
	})
}

// Start starts the HTTP server
func (s *Server) Start() error {
	addr := fmt.Sprintf(":%s", s.cfg.Port)
	log.Printf("Starting server on %s", addr)
	return http.ListenAndServe(addr, s.router)
}

// Health check handler
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	health := map[string]interface{}{
		"status":  "healthy",
		"version": "dev",
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

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Application name is required")
		return
	}

	app, err := s.appStore.Create(req.Name)
	if err != nil {
		if err.Error() == fmt.Sprintf("application with name '%s' already exists", req.Name) {
			writeError(w, http.StatusConflict, "conflict", err.Error())
			return
		}
		log.Printf("Failed to create application: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create application")
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

	// Check if version already exists
	existing, _ := s.versionStore.GetByVersionID(appID, req.VersionID)
	if existing != nil {
		writeError(w, http.StatusConflict, "conflict", fmt.Sprintf("Version '%s' already exists", req.VersionID))
		return
	}

	// Create version record
	version, err := s.versionStore.Create(appID, req.VersionID, req.Metadata)
	if err != nil {
		log.Printf("Failed to create version: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create version")
		return
	}

	// Generate presigned URL for manifest upload
	uploadURL, err := s.storage.GeneratePresignedURL(app.Name, req.VersionID, "manifests.tar.gz")
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
	}

	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handlePublishVersion(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	versionID := chi.URLParam(r, "versionId")

	log.Printf("Publishing version %s for app %s", versionID, appID)

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

	// Check if already published
	if version.Status == "published" {
		writeError(w, http.StatusConflict, "conflict", "Version is already published")
		return
	}

	// List files in draft location
	files, err := s.storage.ListFiles(app.Name, versionID, false)
	if err != nil {
		log.Printf("Failed to list draft files: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to access draft files")
		return
	}

	log.Printf("Found %d files in draft location for version %s: %v", len(files), versionID, files)

	if len(files) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "No manifest files uploaded")
		return
	}

	// Check if we have a tarball that needs to be extracted
	manifestFiles := []string{}
	var tarballFiles map[string][]byte

	// Look for manifests.tar.gz
	hasTarball := false
	for _, file := range files {
		if file == "manifests.tar.gz" {
			hasTarball = true
			log.Printf("Found tarball, extracting files...")

			// Get and extract tarball
			reader, err := s.storage.GetFile(app.Name, versionID, file, false)
			if err != nil {
				log.Printf("Failed to get tarball %s: %v", file, err)
				writeError(w, http.StatusInternalServerError, "internal_error", "Failed to read manifest files")
				return
			}
			defer reader.Close()

			tarballFiles, err = s.extractTarball(reader)
			if err != nil {
				log.Printf("Failed to extract tarball: %v", err)
				writeError(w, http.StatusInternalServerError, "internal_error", "Failed to extract manifest files")
				return
			}

			log.Printf("Extracted %d files from tarball: %v", len(tarballFiles), getKeys(tarballFiles))
			break
		}
	}

	// Process files (either from tarball or individual uploads)
	if hasTarball {
		// Validate files from tarball
		for filename, content := range tarballFiles {
			log.Printf("Processing extracted file: %s", filename)
			if strings.HasSuffix(filename, ".yaml") || strings.HasSuffix(filename, ".yml") {
				log.Printf("File %s is a YAML file, validating...", filename)
				log.Printf("Read %d bytes from file %s", len(content), filename)

				// Validate YAML syntax
				var yamlContent interface{}
				if err := yaml.Unmarshal(content, &yamlContent); err != nil {
					log.Printf("YAML validation failed for file %s: %v", filename, err)
					writeError(w, http.StatusBadRequest, "validation_failed", fmt.Sprintf("Invalid YAML in %s: %v", filename, err))
					return
				}

				log.Printf("File %s validated successfully", filename)
				manifestFiles = append(manifestFiles, filename)
			} else {
				log.Printf("Skipping non-YAML file: %s", filename)
			}
		}
	} else {
		// Validate individual files
		for _, file := range files {
			log.Printf("Processing file: %s", file)
			if strings.HasSuffix(file, ".yaml") || strings.HasSuffix(file, ".yml") {
				log.Printf("File %s is a YAML file, validating...", file)
				// Get file content
				reader, err := s.storage.GetFile(app.Name, versionID, file, false)
				if err != nil {
					log.Printf("Failed to get file %s: %v", file, err)
					writeError(w, http.StatusInternalServerError, "internal_error", "Failed to read manifest files")
					return
				}
				defer reader.Close()

				// Read content
				content, err := io.ReadAll(reader)
				if err != nil {
					log.Printf("Failed to read file %s: %v", file, err)
					writeError(w, http.StatusInternalServerError, "internal_error", "Failed to read manifest files")
					return
				}

				log.Printf("Read %d bytes from file %s", len(content), file)

				// Validate YAML syntax
				var yamlContent interface{}
				if err := yaml.Unmarshal(content, &yamlContent); err != nil {
					log.Printf("YAML validation failed for file %s: %v", file, err)
					writeError(w, http.StatusBadRequest, "validation_failed", fmt.Sprintf("Invalid YAML in %s: %v", file, err))
					return
				}

				log.Printf("File %s validated successfully", file)
				manifestFiles = append(manifestFiles, file)
			} else {
				log.Printf("Skipping non-YAML file: %s", file)
			}
		}
	}

	if len(manifestFiles) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "No valid YAML manifest files found")
		return
	}

	// Move files from drafts to published
	if err := s.storage.MoveVersion(app.Name, versionID); err != nil {
		log.Printf("Failed to move version to published: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to publish version")
		return
	}

	// Update version status
	if err := s.versionStore.UpdateStatus(version.ID, "published"); err != nil {
		log.Printf("Failed to update version status: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to update version status")
		return
	}

	// Refresh version to get updated fields
	version, _ = s.versionStore.GetByVersionID(appID, versionID)

	// Check for matching auto-deploy policies
	if version.GitBranch != "" {
		matchingPolicies, err := s.policyStore.FindMatchingPolicies(appID, version.GitBranch)
		if err != nil {
			log.Printf("Failed to check auto-deploy policies: %v", err)
			// Don't fail the publish, just log the error
		} else {
			for _, policy := range matchingPolicies {
				log.Printf("Auto-deploying version %s to %s via policy %s", versionID, policy.TargetEnvironment, policy.Name)

				// Trigger deployment asynchronously to avoid blocking the response
				go s.autoDeployVersion(app.Name, appID, version, policy)
			}
		}
	}

	resp := models.PublishVersionResponse{
		VersionID:     version.VersionID,
		Status:        version.Status,
		PublishedAt:   *version.PublishedAt,
		ManifestFiles: manifestFiles,
	}

	writeJSON(w, http.StatusOK, resp)
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
		VersionID:   version.VersionID,
		Status:      version.Status,
		CreatedAt:   version.CreatedAt,
		PublishedAt: version.PublishedAt,
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

	// Decode request body
	var req models.DeployVersionRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	// Validate environment
	if req.Environment == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Environment is required")
		return
	}

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

	// Verify version exists and is published
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

	if version.Status != "published" {
		writeError(w, http.StatusBadRequest, "invalid_status", "Version must be published before deployment")
		return
	}

	// Create deployment record
	deployment, err := s.deploymentStore.Create(appID, version.ID, req.Environment, req.TriggeredBy, nil)
	if err != nil {
		log.Printf("Failed to create deployment: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create deployment")
		return
	}

	// Fetch manifests from S3
	manifests, err := s.storage.GetAllFiles(app.Name, versionID, true)
	if err != nil {
		log.Printf("Failed to fetch manifests from S3: %v", err)
		s.deploymentStore.UpdateStatus(deployment.ID, "failed", "", fmt.Sprintf("Failed to fetch manifests: %v", err))
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to fetch manifests")
		return
	}

	// Clone gitops repo
	if err := s.gitops.Clone(); err != nil {
		log.Printf("Failed to clone gitops repo: %v", err)
		s.deploymentStore.UpdateStatus(deployment.ID, "failed", "", fmt.Sprintf("Failed to clone gitops repo: %v", err))
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to clone gitops repository")
		return
	}

	// Write manifests to gitops repo
	if err := s.gitops.WriteManifests(app.Name, req.Environment, versionID, manifests); err != nil {
		log.Printf("Failed to write manifests: %v", err)
		s.deploymentStore.UpdateStatus(deployment.ID, "failed", "", fmt.Sprintf("Failed to write manifests: %v", err))
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to write manifests")
		return
	}

	// Commit changes
	commitMsg := fmt.Sprintf("Deploy %s version %s to %s", app.Name, versionID, req.Environment)
	commitSHA, err := s.gitops.Commit(commitMsg)
	if err != nil {
		log.Printf("Failed to commit: %v", err)
		s.deploymentStore.UpdateStatus(deployment.ID, "failed", "", fmt.Sprintf("Failed to commit: %v", err))
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to commit changes")
		return
	}

	// Push to remote
	if err := s.gitops.Push(); err != nil {
		log.Printf("Failed to push: %v", err)
		s.deploymentStore.UpdateStatus(deployment.ID, "failed", commitSHA, fmt.Sprintf("Failed to push: %v", err))
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to push to gitops repository")
		return
	}

	// Update deployment status
	if err := s.deploymentStore.UpdateStatus(deployment.ID, "success", commitSHA, ""); err != nil {
		log.Printf("Failed to update deployment status: %v", err)
		// Don't return error, deployment was successful
	}

	// Return response
	resp := models.DeployVersionResponse{
		DeploymentID:    deployment.ID,
		VersionID:       versionID,
		Environment:     req.Environment,
		Status:          "success",
		GitopsCommitSHA: commitSHA,
		StartedAt:       deployment.StartedAt,
	}

	writeJSON(w, http.StatusAccepted, resp)
}

func (s *Server) handleCreatePolicy(w http.ResponseWriter, r *http.Request) {
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

	// Decode request body
	var req models.CreatePolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	// Validate required fields
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Policy name is required")
		return
	}
	if req.GitBranchPattern == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Git branch pattern is required")
		return
	}
	if req.TargetEnvironment == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Target environment is required")
		return
	}

	// Default enabled to true if not specified
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	// Create policy
	policy, err := s.policyStore.Create(appID, req.Name, req.GitBranchPattern, req.TargetEnvironment, enabled)
	if err != nil {
		log.Printf("Failed to create policy: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to create policy")
		return
	}

	resp := models.PolicyResponse{
		ID:                policy.ID,
		AppID:             policy.AppID,
		Name:              policy.Name,
		GitBranchPattern:  policy.GitBranchPattern,
		TargetEnvironment: policy.TargetEnvironment,
		Enabled:           policy.Enabled,
		CreatedAt:         policy.CreatedAt,
	}

	writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleListPolicies(w http.ResponseWriter, r *http.Request) {
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

	// List policies
	policies, err := s.policyStore.List(appID)
	if err != nil {
		log.Printf("Failed to list policies: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list policies")
		return
	}

	resp := models.ListPoliciesResponse{
		Policies: policies,
		Total:    len(policies),
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDeletePolicy(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	policyID := chi.URLParam(r, "policyId")

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

	// Verify policy exists and belongs to this app
	policy, err := s.policyStore.GetByID(policyID)
	if err != nil {
		if err.Error() == "policy not found" {
			writeError(w, http.StatusNotFound, "not_found", "Policy not found")
			return
		}
		log.Printf("Failed to get policy: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to get policy")
		return
	}

	if policy.AppID != appID {
		writeError(w, http.StatusNotFound, "not_found", "Policy not found")
		return
	}

	// Delete policy
	if err := s.policyStore.Delete(policyID); err != nil {
		log.Printf("Failed to delete policy: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to delete policy")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// autoDeployVersion automatically deploys a version based on a policy
// This runs asynchronously in a goroutine
func (s *Server) autoDeployVersion(appName, appID string, version *models.Version, policy models.Policy) {
	// Create deployment record
	policyID := policy.ID
	deployment, err := s.deploymentStore.Create(appID, version.ID, policy.TargetEnvironment, "auto-deploy", &policyID)
	if err != nil {
		log.Printf("Auto-deploy failed to create deployment record: %v", err)
		return
	}

	// Fetch manifests from S3
	manifests, err := s.storage.GetAllFiles(appName, version.VersionID, true)
	if err != nil {
		log.Printf("Auto-deploy failed to fetch manifests: %v", err)
		s.deploymentStore.UpdateStatus(deployment.ID, "failed", "", fmt.Sprintf("Failed to fetch manifests: %v", err))
		return
	}

	// Clone gitops repo
	if err := s.gitops.Clone(); err != nil {
		log.Printf("Auto-deploy failed to clone gitops repo: %v", err)
		s.deploymentStore.UpdateStatus(deployment.ID, "failed", "", fmt.Sprintf("Failed to clone gitops repo: %v", err))
		return
	}

	// Write manifests to gitops repo
	if err := s.gitops.WriteManifests(appName, policy.TargetEnvironment, version.VersionID, manifests); err != nil {
		log.Printf("Auto-deploy failed to write manifests: %v", err)
		s.deploymentStore.UpdateStatus(deployment.ID, "failed", "", fmt.Sprintf("Failed to write manifests: %v", err))
		return
	}

	// Commit changes
	commitMsg := fmt.Sprintf("Auto-deploy %s version %s to %s (policy: %s)", appName, version.VersionID, policy.TargetEnvironment, policy.Name)
	commitSHA, err := s.gitops.Commit(commitMsg)
	if err != nil {
		log.Printf("Auto-deploy failed to commit: %v", err)
		s.deploymentStore.UpdateStatus(deployment.ID, "failed", "", fmt.Sprintf("Failed to commit: %v", err))
		return
	}

	// Push to remote
	if err := s.gitops.Push(); err != nil {
		log.Printf("Auto-deploy failed to push: %v", err)
		s.deploymentStore.UpdateStatus(deployment.ID, "failed", commitSHA, fmt.Sprintf("Failed to push: %v", err))
		return
	}

	// Update deployment status
	if err := s.deploymentStore.UpdateStatus(deployment.ID, "success", commitSHA, ""); err != nil {
		log.Printf("Auto-deploy failed to update deployment status: %v", err)
		return
	}

	log.Printf("Auto-deploy succeeded: %s version %s to %s (deployment: %s, commit: %s)", appName, version.VersionID, policy.TargetEnvironment, deployment.ID, commitSHA)
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
