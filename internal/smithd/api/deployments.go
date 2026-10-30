package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
)

func (s *Server) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	limit := 50
	offset := 0

	if limitStr := query.Get("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	if offsetStr := query.Get("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	filter := store.DeploymentFilter{
		AppID:       query.Get("appId"),
		Environment: query.Get("environment"),
		Status:      query.Get("status"),
	}
	deployments, total, err := s.svc.ListDeployments(filter, limit, offset)
	if err != nil {
		writeServiceError(w, err, "Failed to list deployments")
		return
	}

	writeJSON(w, http.StatusOK, models.ListDeploymentsResponse{
		Deployments: deployments,
		Total:       total,
		Limit:       limit,
		Offset:      offset,
	})
}

func (s *Server) handleGetDeployment(w http.ResponseWriter, r *http.Request) {
	deploymentID := chi.URLParam(r, "deploymentId")

	deployment, err := s.svc.GetDeployment(deploymentID)
	if err != nil {
		writeServiceError(w, err, "Failed to get deployment")
		return
	}

	writeJSON(w, http.StatusOK, deployment)
}

// handleApproveDeployment approves the plan of a terraform deployment. The
// body may be empty.
func (s *Server) handleApproveDeployment(w http.ResponseWriter, r *http.Request) {
	var req models.ApproveDeploymentRequest
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	deployment, err := s.svc.ApproveTerraform(r.Context(), chi.URLParam(r, "deploymentId"), req.ConfirmDestroy)
	if err != nil {
		writeServiceError(w, err, "Failed to approve the plan")
		return
	}
	writeJSON(w, http.StatusOK, deployment)
}

// handleRejectDeployment rejects the plan of a terraform deployment. The
// body may be empty.
func (s *Server) handleRejectDeployment(w http.ResponseWriter, r *http.Request) {
	var req models.RejectDeploymentRequest
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	deployment, err := s.svc.RejectTerraform(r.Context(), chi.URLParam(r, "deploymentId"), req.Reason)
	if err != nil {
		writeServiceError(w, err, "Failed to reject the plan")
		return
	}
	writeJSON(w, http.StatusOK, deployment)
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	overview, err := s.svc.Overview()
	if err != nil {
		writeServiceError(w, err, "Failed to build overview")
		return
	}

	writeJSON(w, http.StatusOK, overview)
}
