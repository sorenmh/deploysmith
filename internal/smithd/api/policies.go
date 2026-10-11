package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

func policyResponse(policy *models.Policy) models.PolicyResponse {
	return models.PolicyResponse{
		ID:                policy.ID,
		AppID:             policy.AppID,
		Name:              policy.Name,
		GitBranchPattern:  policy.GitBranchPattern,
		TargetEnvironment: policy.TargetEnvironment,
		Enabled:           policy.Enabled,
		CreatedAt:         policy.CreatedAt,
	}
}

func (s *Server) handleCreatePolicy(w http.ResponseWriter, r *http.Request) {
	var req models.CreatePolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	policy, err := s.svc.CreatePolicy(chi.URLParam(r, "appId"), req)
	if err != nil {
		writeServiceError(w, err, "Failed to create policy")
		return
	}

	writeJSON(w, http.StatusCreated, policyResponse(policy))
}

func (s *Server) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := s.svc.ListPolicies(chi.URLParam(r, "appId"))
	if err != nil {
		writeServiceError(w, err, "Failed to list policies")
		return
	}

	writeJSON(w, http.StatusOK, models.ListPoliciesResponse{
		Policies: policies,
		Total:    len(policies),
	})
}

func (s *Server) handleUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	var req models.UpdatePolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	policy, err := s.svc.UpdatePolicy(chi.URLParam(r, "appId"), chi.URLParam(r, "policyId"), req)
	if err != nil {
		writeServiceError(w, err, "Failed to update policy")
		return
	}

	writeJSON(w, http.StatusOK, policyResponse(policy))
}

func (s *Server) handleDeletePolicy(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.DeletePolicy(chi.URLParam(r, "appId"), chi.URLParam(r, "policyId")); err != nil {
		writeServiceError(w, err, "Failed to delete policy")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
