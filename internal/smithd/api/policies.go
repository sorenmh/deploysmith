package api

import (
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

func (s *Server) handleUpdatePolicy(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	policyID := chi.URLParam(r, "policyId")

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

	var req models.UpdatePolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "Policy name cannot be empty")
			return
		}
		if name != policy.Name {
			taken, err := s.policyNameTaken(appID, name)
			if err != nil {
				log.Printf("Failed to list policies: %v", err)
				writeError(w, http.StatusInternalServerError, "internal_error", "Failed to update policy")
				return
			}
			if taken {
				writeError(w, http.StatusConflict, "conflict", fmt.Sprintf("Policy '%s' already exists", name))
				return
			}
		}
		policy.Name = name
	}

	if req.GitBranchPattern != nil {
		pattern := strings.TrimSpace(*req.GitBranchPattern)
		if pattern == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "Git branch pattern cannot be empty")
			return
		}
		if _, err := filepath.Match(pattern, ""); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("Invalid git branch pattern: %v", err))
			return
		}
		policy.GitBranchPattern = pattern
	}

	if req.TargetEnvironment != nil {
		env := strings.TrimSpace(*req.TargetEnvironment)
		exists, err := s.environmentStore.Exists(env)
		if err != nil {
			log.Printf("Failed to check if environment exists: %v", err)
			writeError(w, http.StatusInternalServerError, "internal_error", "Failed to validate environment")
			return
		}
		if !exists {
			writeError(w, http.StatusBadRequest, "invalid_environment", fmt.Sprintf("Environment '%s' does not exist", env))
			return
		}
		policy.TargetEnvironment = env
	}

	if req.Enabled != nil {
		policy.Enabled = *req.Enabled
	}

	updated, err := s.policyStore.Update(policy)
	if err != nil {
		log.Printf("Failed to update policy: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to update policy")
		return
	}

	writeJSON(w, http.StatusOK, models.PolicyResponse{
		ID:                updated.ID,
		AppID:             updated.AppID,
		Name:              updated.Name,
		GitBranchPattern:  updated.GitBranchPattern,
		TargetEnvironment: updated.TargetEnvironment,
		Enabled:           updated.Enabled,
		CreatedAt:         updated.CreatedAt,
	})
}

// policyNameTaken reports whether the app already has a policy with this name
func (s *Server) policyNameTaken(appID, name string) (bool, error) {
	policies, err := s.policyStore.List(appID)
	if err != nil {
		return false, err
	}
	for _, p := range policies {
		if p.Name == name {
			return true, nil
		}
	}
	return false, nil
}
