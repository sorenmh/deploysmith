package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
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
