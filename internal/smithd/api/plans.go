package api

import (
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/shared/tfbundle"
	"github.com/sorenmh/deploysmith/internal/smithd/auth"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// handlePlanVersion starts a plan-only run of a draft or published terraform
// version for an environment. It returns once the run is committed, planning;
// the result is read from the run (handleGetPlanRun).
func (s *Server) handlePlanVersion(w http.ResponseWriter, r *http.Request) {
	var req models.PlanVersionRequest
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

	run, err := s.svc.PlanVersion(r.Context(), chi.URLParam(r, "appId"), chi.URLParam(r, "versionId"), req.Environment, triggeredBy)
	if err != nil {
		writeServiceError(w, err, "Plan failed")
		return
	}

	writeJSON(w, http.StatusAccepted, run)
}

// handleLocalPlan makes the temporary version of a local plan for the
// signed-in user and returns where to upload the bundle. The plan itself is
// started like that of any version (handlePlanVersion) once it is uploaded.
// API keys are refused: a local plan is a person's.
func (s *Server) handleLocalPlan(w http.ResponseWriter, r *http.Request) {
	var req models.LocalPlanRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	app, version, err := s.svc.PrepareLocalPlan(r.Context(), chi.URLParam(r, "appId"), req)
	if err != nil {
		writeServiceError(w, err, "Failed to prepare the local plan")
		return
	}

	uploadURL, err := s.storage.GeneratePresignedURL(app.Name, version.VersionID, tfbundle.ArchiveName)
	if err != nil {
		log.Printf("Failed to generate presigned URL: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to generate upload URL")
		return
	}

	writeJSON(w, http.StatusCreated, models.LocalPlanResponse{
		VersionID:     version.VersionID,
		UploadURL:     uploadURL,
		UploadExpires: time.Now().Add(5 * time.Minute),
		ExpiresAt:     *version.ExpiresAt,
	})
}

// handleGetVersionPlan returns the newest plan-only run of a version for an
// environment (?environment=)
func (s *Server) handleGetVersionPlan(w http.ResponseWriter, r *http.Request) {
	environment := r.URL.Query().Get("environment")
	if environment == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "environment is required")
		return
	}

	run, err := s.svc.GetVersionPlan(chi.URLParam(r, "appId"), chi.URLParam(r, "versionId"), environment)
	if err != nil {
		writeServiceError(w, err, "Failed to get plan")
		return
	}

	writeJSON(w, http.StatusOK, run)
}

// handleGetPlanRun returns a plan-only run with its plan
func (s *Server) handleGetPlanRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.svc.GetPlanRun(chi.URLParam(r, "planRunId"))
	if err != nil {
		writeServiceError(w, err, "Failed to get plan run")
		return
	}

	writeJSON(w, http.StatusOK, run)
}
