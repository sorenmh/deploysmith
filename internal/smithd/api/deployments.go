package api

import (
	"log"
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
	switch filter.Status {
	case "", "pending", "success", "failed":
	default:
		writeError(w, http.StatusBadRequest, "invalid_request", "status must be one of: pending, success, failed")
		return
	}

	deployments, total, err := s.deploymentStore.ListDetailed(filter, limit, offset)
	if err != nil {
		log.Printf("Failed to list deployments: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to list deployments")
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

	deployment, err := s.deploymentStore.GetDetailed(deploymentID)
	if err != nil {
		if err.Error() == "deployment not found" {
			writeError(w, http.StatusNotFound, "not_found", "Deployment not found")
			return
		}
		log.Printf("Failed to get deployment: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to get deployment")
		return
	}

	writeJSON(w, http.StatusOK, deployment)
}
