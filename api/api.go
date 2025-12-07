package api

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/sorenmh/infrastructure-shared/deployment-api/config"
	"github.com/sorenmh/infrastructure-shared/deployment-api/db"
	"github.com/sorenmh/infrastructure-shared/deployment-api/git"
	"github.com/sorenmh/infrastructure-shared/deployment-api/manifests"
	"github.com/sorenmh/infrastructure-shared/deployment-api/models"
	"github.com/sorenmh/infrastructure-shared/deployment-api/registry"
)

type Server struct {
	config           *config.Config
	db               *db.Database
	gitClient        git.GitClient
	manifestGenerator manifests.ManifestGenerator
	router           *gin.Engine
}

const Version = "1.0.0"

func NewServer(cfg *config.Config, database *db.Database, gitClient git.GitClient) *Server {
	if cfg.Logging.Level == "debug" {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	// Create manifest generator with registry configuration
	registryConfig := &manifests.RegistryConfig{
		Type:                cfg.Registry.Type,
		ImagePullSecretName: cfg.Registry.ImagePullSecretName,
	}

	s := &Server{
		config:           cfg,
		db:               database,
		gitClient:        gitClient,
		manifestGenerator: manifests.NewGeneratorWithConfig(registryConfig),
		router:           gin.Default(),
	}

	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	// Health check (no auth)
	s.router.GET("/health", s.handleHealth)

	// API routes (with auth)
	api := s.router.Group("/api/v1")
	api.Use(s.authMiddleware())
	{
		// Services (Legacy deployment API)
		api.GET("/services", s.handleListServices)
		api.GET("/services/:service/versions", s.handleListVersions)
		api.GET("/services/:service/current", s.handleGetCurrent)
		api.POST("/services/:service/deploy", s.handleDeploy)
		api.POST("/services/:service/rollback", s.handleRollback)
		api.GET("/services/:service/deployments", s.handleGetDeployments)

		// Service Abstraction Layer API
		api.POST("/manifests/generate", s.handleGenerateManifests)
		api.POST("/manifests/validate", s.handleValidateService)
		api.POST("/manifests/deploy", s.handleDeployService)

		// Webhook
		api.POST("/webhook/build", s.handleWebhook)
	}
}

func (s *Server) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		auth := c.GetHeader("Authorization")
		if auth == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing authorization header"})
			c.Abort()
			return
		}

		// Extract token from "Bearer <token>"
		parts := strings.Split(auth, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid authorization format"})
			c.Abort()
			return
		}

		token := parts[1]
		if !s.config.ValidateAPIKey(token) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid API key"})
			c.Abort()
			return
		}

		c.Next()
	}
}

func (s *Server) handleHealth(c *gin.Context) {
	gitOK := s.gitClient.CheckHealth() == nil
	dbOK := s.db.Ping() == nil

	status := "healthy"
	if !gitOK || !dbOK {
		status = "degraded"
	}

	c.JSON(http.StatusOK, models.HealthResponse{
		Status:             status,
		Version:            Version,
		GitRepoAccessible:  gitOK,
		DatabaseAccessible: dbOK,
	})
}

func (s *Server) handleListServices(c *gin.Context) {
	var services []models.Service

	for _, svc := range s.config.Services {
		currentVersion, err := s.gitClient.GetCurrentImageTag(svc.ManifestPath)
		if err != nil {
			log.Printf("Error getting current image tag for service %s: %v", svc.Name, err)
			// Optionally, return an error to the client or set a specific status for this service
			// For now, we'll continue, but the version will be empty.
			// A more robust solution might involve returning a 500 here or marking the service as "degraded".
			// For this specific issue, I'll log and set currentVersion to an empty string to indicate unknown.
			currentVersion = "" 
		}

		services = append(services, models.Service{
			Name:            svc.Name,
			Namespace:       svc.Namespace,
			CurrentVersion:  currentVersion,
			ManifestPath:    svc.ManifestPath,
			ImageRepository: svc.ImageRepository,
		})
	}

	c.JSON(http.StatusOK, gin.H{"services": services})
}

func (s *Server) handleListVersions(c *gin.Context) {
	serviceName := c.Param("service")
	limitStr := c.DefaultQuery("limit", "10")
	limit, _ := strconv.Atoi(limitStr)

	svc := s.config.GetService(serviceName)
	if svc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
		return
	}

	// Create registry client
	var regClient *registry.Client
	if svc.RegistryAuth != nil {
		regClient = registry.NewClient(svc.RegistryAuth.Username, svc.RegistryAuth.Password)
	} else {
		regClient = registry.NewClient("", "")
	}

	versions, err := regClient.ListVersions(svc.ImageRepository, limit)
	if err != nil {
		log.Printf("Error listing versions: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list versions"})
		return
	}

	// Mark deployed versions
	currentVersion, _ := s.gitClient.GetCurrentImageTag(svc.ManifestPath)
	for i := range versions {
		if versions[i].Tag == currentVersion {
			versions[i].Deployed = true
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"service":  serviceName,
		"versions": versions,
	})
}

func (s *Server) handleGetCurrent(c *gin.Context) {
	serviceName := c.Param("service")

	svc := s.config.GetService(serviceName)
	if svc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
		return
	}

	currentDep, err := s.db.GetCurrentDeployment(serviceName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get current deployment"})
		return
	}

	if currentDep == nil {
		// Get from Git manifest
		version, err := s.gitClient.GetCurrentImageTag(svc.ManifestPath)
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "no deployment found"})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"service": serviceName,
			"version": version,
			"status":  "unknown",
		})
		return
	}

	c.JSON(http.StatusOK, currentDep)
}

func (s *Server) handleDeploy(c *gin.Context) {
	serviceName := c.Param("service")

	var req models.DeployRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	svc := s.config.GetService(serviceName)
	if svc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
		return
	}

	// Verify version exists in registry
	var regClient *registry.Client
	if svc.RegistryAuth != nil {
		regClient = registry.NewClient(svc.RegistryAuth.Username, svc.RegistryAuth.Password)
	} else {
		regClient = registry.NewClient("", "")
	}

	exists, err := regClient.TagExists(svc.ImageRepository, req.Version)
	if err != nil {
		log.Printf("Error checking tag: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to verify version"})
		return
	}

	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "version not found in registry"})
		return
	}

	// Create deployment record
	deploymentID := uuid.New().String()
	deployment := &models.Deployment{
		ID:          deploymentID,
		ServiceName: serviceName,
		Version:     req.Version,
		DeployedAt:  time.Now(),
		DeployedBy:  req.DeployedBy,
		Status:      "pending",
		Type:        "deploy",
		Message:     req.Message,
	}

	if err := s.db.CreateDeployment(deployment); err != nil {
		log.Printf("Error creating deployment: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create deployment"})
		return
	}

	// Update Git manifest
	commit, err := s.gitClient.UpdateImageTag(svc.ManifestPath, req.Version)
	if err != nil {
		log.Printf("Error updating git: %v", err)
		s.db.UpdateDeploymentStatus(deploymentID, "failed")
		s.db.AddEvent(deploymentID, "git_update_failed", err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update git manifest"})
		return
	}

	deployment.GitCommit = commit
	s.db.UpdateDeploymentStatus(deploymentID, "success")
	s.db.AddEvent(deploymentID, "git_push", commit)

	c.JSON(http.StatusOK, deployment)
}

func (s *Server) handleRollback(c *gin.Context) {
	serviceName := c.Param("service")

	var req models.RollbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	svc := s.config.GetService(serviceName)
	if svc == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
		return
	}

	// If no version specified, get previous successful deployment
	targetVersion := req.Version
	if targetVersion == "" {
		deployments, _, err := s.db.GetDeployments(serviceName, 2, 0)
		if err != nil {
			log.Printf("Error getting deployments for rollback: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to retrieve deployment history"})
			return
		}
		if len(deployments) < 2 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "no previous deployment found"})
			return
		}
		targetVersion = deployments[1].Version
	}

	// Create rollback deployment
	deploymentID := uuid.New().String()
	deployment := &models.Deployment{
		ID:          deploymentID,
		ServiceName: serviceName,
		Version:     targetVersion,
		DeployedAt:  time.Now(),
		DeployedBy:  req.DeployedBy,
		Status:      "pending",
		Type:        "rollback",
	}

	if err := s.db.CreateDeployment(deployment); err != nil {
		log.Printf("Error creating deployment: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create deployment"})
		return
	}

	// Update Git manifest
	commit, err := s.gitClient.UpdateImageTag(svc.ManifestPath, targetVersion)
	if err != nil {
		log.Printf("Error updating git: %v", err)
		s.db.UpdateDeploymentStatus(deploymentID, "failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update git manifest"})
		return
	}

	deployment.GitCommit = commit
	s.db.UpdateDeploymentStatus(deploymentID, "success")

	c.JSON(http.StatusOK, deployment)
}

func (s *Server) handleGetDeployments(c *gin.Context) {
	serviceName := c.Param("service")
	limitStr := c.DefaultQuery("limit", "20")
	offsetStr := c.DefaultQuery("offset", "0")

	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)

	deployments, total, err := s.db.GetDeployments(serviceName, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to get deployments"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"service":     serviceName,
		"deployments": deployments,
		"total":       total,
		"limit":       limit,
		"offset":      offset,
	})
}

func (s *Server) handleWebhook(c *gin.Context) {
	var req models.WebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	log.Printf("Received webhook: service=%s version=%s auto_deploy=%v", req.Service, req.Version, req.AutoDeploy)

	// If auto-deploy is enabled, trigger deployment
	if req.AutoDeploy {
		deployReq := models.DeployRequest{
			Version:    req.Version,
			DeployedBy: "webhook-auto",
			Message:    fmt.Sprintf("Auto-deploy from webhook (git:%s)", req.GitSHA),
		}

		// Use internal deploy logic (simplified)
		svc := s.config.GetService(req.Service)
		if svc == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "service not found"})
			return
		}

		deploymentID := uuid.New().String()
		deployment := &models.Deployment{
			ID:          deploymentID,
			ServiceName: req.Service,
			Version:     req.Version,
			DeployedAt:  time.Now(),
			DeployedBy:  "webhook-auto",
			Status:      "pending",
			Type:        "deploy",
			Message:     deployReq.Message,
		}

		if err := s.db.CreateDeployment(deployment); err != nil {
			log.Printf("Error creating deployment record for webhook: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create deployment record"})
			return
		}

		commit, err := s.gitClient.UpdateImageTag(svc.ManifestPath, req.Version)
		if err != nil {
			log.Printf("Error updating git manifest from webhook: %v", err)
			s.db.UpdateDeploymentStatus(deploymentID, "failed") // Update DB with failed status
			s.db.AddEvent(deploymentID, "git_update_failed", err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update git manifest"})
			return
		}
		deployment.GitCommit = commit
		s.db.UpdateDeploymentStatus(deploymentID, "success")
		s.db.AddEvent(deploymentID, "git_push", commit)
	}

	c.JSON(http.StatusOK, gin.H{
		"status":      "received",
		"service":     req.Service,
		"version":     req.Version,
		"auto_deploy": req.AutoDeploy,
	})
}

// Service Abstraction Layer API Handlers

func (s *Server) handleGenerateManifests(c *gin.Context) {
	var req models.GenerateManifestsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Invalid request body",
			Details: err.Error(),
			Time:    time.Now(),
		})
		return
	}

	// Generate manifests using the manifest generator
	manifests, err := s.manifestGenerator.GenerateManifests(req.ServiceDefinition.Name, req.ServiceDefinition)
	if err != nil {
		log.Printf("Error generating manifests: %v", err)
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Failed to generate manifests",
			Details: err.Error(),
			Time:    time.Now(),
		})
		return
	}

	// Convert byte maps to string maps for JSON response
	manifestsStr := make(map[string]string)
	for filename, content := range manifests {
		manifestsStr[filename] = string(content)
	}

	response := models.GenerateManifestsResponse{
		ServiceName: req.ServiceDefinition.Name,
		Manifests:   manifestsStr,
		GeneratedAt: time.Now(),
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) handleValidateService(c *gin.Context) {
	var req models.ValidateServiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Invalid request body",
			Details: err.Error(),
			Time:    time.Now(),
		})
		return
	}

	// Use comprehensive validation
	var validationErrors []models.ValidationError
	var warnings []string

	// First validate using the detailed validation system
	if err := models.ValidateServiceDefinition(req.ServiceDefinition); err != nil {
		// Convert to detailed validation errors
		if valErrors, ok := err.(models.ValidationErrors); ok {
			validationErrors = append(validationErrors, valErrors...)
		} else {
			// Convert single errors to validation error format
			validationErrors = append(validationErrors, models.ValidationError{
				Field:   "service_definition",
				Message: err.Error(),
			})
		}
	}

	// Additional validation warnings
	for componentName, component := range req.ServiceDefinition.Components {
		if component.ImagePolicy == nil {
			warnings = append(warnings, fmt.Sprintf("Component %q has no image policy - Flux automation will be disabled", componentName))
		}

		if component.Port == 0 && component.Type == models.ComponentTypeWeb {
			warnings = append(warnings, fmt.Sprintf("Web component %q has no port specified - Service will not be created", componentName))
		}

		// Check for potential configuration issues
		if component.Type == models.ComponentTypeAPI && component.Port == 0 {
			warnings = append(warnings, fmt.Sprintf("API component %q has no port - consider adding a port if it serves HTTP traffic", componentName))
		}

		if component.Replicas > 10 {
			warnings = append(warnings, fmt.Sprintf("Component %q has high replica count (%d) - ensure your cluster has sufficient resources", componentName, component.Replicas))
		}
	}

	// Create validation summary
	validationSummary := "Service definition is valid"
	if len(validationErrors) > 0 {
		validationSummary = fmt.Sprintf("Found %d validation error(s)", len(validationErrors))
	}
	if len(warnings) > 0 {
		if len(validationErrors) == 0 {
			validationSummary = fmt.Sprintf("Valid with %d warning(s)", len(warnings))
		} else {
			validationSummary += fmt.Sprintf(" and %d warning(s)", len(warnings))
		}
	}

	response := models.ValidateServiceResponse{
		Valid:             len(validationErrors) == 0,
		Errors:            validationErrors,
		Warnings:          warnings,
		ValidationSummary: validationSummary,
		Validated:         time.Now(),
	}

	if len(validationErrors) > 0 {
		c.JSON(http.StatusBadRequest, response)
	} else {
		c.JSON(http.StatusOK, response)
	}
}

func (s *Server) handleDeployService(c *gin.Context) {
	var req models.DeployServiceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Invalid request body",
			Details: err.Error(),
			Time:    time.Now(),
		})
		return
	}

	// Set default target directory if not provided
	targetDirectory := req.TargetDirectory
	if targetDirectory == "" {
		targetDirectory = fmt.Sprintf("services/%s", req.ServiceDefinition.Name)
	}

	// Validate service definition first
	if err := models.ValidateServiceDefinition(req.ServiceDefinition); err != nil {
		log.Printf("Service definition validation failed: %v", err)

		// Provide detailed validation errors in the response
		if valErrors, ok := err.(models.ValidationErrors); ok {
			errorDetails := fmt.Sprintf("Validation failed with %d error(s):", len(valErrors))
			for _, ve := range valErrors {
				errorDetails += fmt.Sprintf("\n  - %s", ve.Error())
			}
			c.JSON(http.StatusBadRequest, models.ErrorResponse{
				Error:   "Service definition validation failed",
				Details: errorDetails,
				Time:    time.Now(),
			})
		} else {
			c.JSON(http.StatusBadRequest, models.ErrorResponse{
				Error:   "Service definition validation failed",
				Details: err.Error(),
				Time:    time.Now(),
			})
		}
		return
	}

	// Generate manifests using the manifest generator
	manifests, err := s.manifestGenerator.GenerateManifests(req.ServiceDefinition.Name, req.ServiceDefinition)
	if err != nil {
		log.Printf("Error generating manifests: %v", err)
		c.JSON(http.StatusBadRequest, models.ErrorResponse{
			Error:   "Failed to generate manifests",
			Details: err.Error(),
			Time:    time.Now(),
		})
		return
	}

	// Write manifests to git repository
	gitCommit, err := s.gitClient.WriteServiceManifests(req.ServiceDefinition.Name, targetDirectory, manifests)
	if err != nil {
		log.Printf("Error writing manifests to git: %v", err)
		c.JSON(http.StatusInternalServerError, models.ErrorResponse{
			Error:   "Failed to write manifests to git repository",
			Details: err.Error(),
			Time:    time.Now(),
		})
		return
	}

	// Convert byte maps to string maps for JSON response
	manifestsStr := make(map[string]string)
	for filename, content := range manifests {
		manifestsStr[filename] = string(content)
	}

	response := models.DeployServiceResponse{
		ServiceName:     req.ServiceDefinition.Name,
		TargetDirectory: targetDirectory,
		Manifests:       manifestsStr,
		GitCommit:       gitCommit,
		DeployedAt:      time.Now(),
		DeployedBy:      req.DeployedBy,
	}

	c.JSON(http.StatusOK, response)
}

func (s *Server) Run() error {
	addr := fmt.Sprintf(":%d", s.config.Server.Port)
	log.Printf("Starting server on %s", addr)
	return s.router.Run(addr)
}
