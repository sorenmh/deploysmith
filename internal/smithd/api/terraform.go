package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithd/config"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/openbao"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform"
)

// newTerraformProvisioner returns the OpenBao provisioner for Terraform
// apps, or nil if OpenBao is not configured
func newTerraformProvisioner(cfg *config.Config) (*terraform.Provisioner, error) {
	if cfg.OpenBaoAddr == "" {
		return nil, nil
	}
	client, err := openbao.New(openbao.Config{
		Addr:      cfg.OpenBaoAddr,
		Token:     cfg.OpenBaoToken,
		CACert:    cfg.OpenBaoCACert,
		AuthMount: cfg.OpenBaoAuthMount,
		AuthRole:  cfg.OpenBaoAuthRole,
		JWTPath:   cfg.OpenBaoJWTPath,
	})
	if err != nil {
		return nil, err
	}
	return &terraform.Provisioner{Bao: client, Opts: terraform.OpenBaoOptions{
		KVMount:         cfg.OpenBaoKVMount,
		TransitMount:    cfg.OpenBaoTransitMount,
		AWSMount:        cfg.OpenBaoAWSMount,
		KubernetesMount: cfg.OpenBaoRunnerAuthMount,
		Audience:        cfg.OpenBaoRunnerAudience,
		RunnerTokenTTL:  cfg.TerraformRunnerTokenTTL,
		STSTTL:          cfg.TerraformSTSTTL,
		AWSAccountID:    cfg.TerraformAWSAccountID,
	}}, nil
}

// terraformManifestOptions configures the runner namespaces from the config
func terraformManifestOptions(cfg *config.Config) (terraform.ManifestOptions, error) {
	opts := terraform.ManifestOptions{
		SmithdNamespace:      cfg.SmithdNamespace,
		SmithdServiceAccount: cfg.SmithdServiceAccount,
		ControllerNamespace:  cfg.TerraformControllerNamespace,
		OpenBaoNamespace:     cfg.OpenBaoK8sNamespace,
		KubeAPICIDRs:         cfg.TerraformKubeAPICIDRs,
	}
	if cfg.OpenBaoAddr == "" {
		return opts, nil
	}

	// Runners reach OpenBao on the port of its address
	addr, err := url.Parse(cfg.OpenBaoAddr)
	if err != nil {
		return opts, fmt.Errorf("invalid OPENBAO_ADDR: %w", err)
	}
	switch {
	case addr.Port() != "":
		if opts.OpenBaoPort, err = strconv.Atoi(addr.Port()); err != nil {
			return opts, fmt.Errorf("invalid OPENBAO_ADDR port: %w", err)
		}
	case addr.Scheme == "http":
		opts.OpenBaoPort = 80
	default:
		opts.OpenBaoPort = 443
	}
	return opts, nil
}

// terraformRootOptions configures the Terraform CRs from the config
func terraformRootOptions(cfg *config.Config) terraform.RootOptions {
	return terraform.RootOptions{
		SourceURL:       cfg.TerraformSourceURL,
		SourceSecret:    cfg.TerraformSourceSecret,
		SourceNamespace: cfg.FluxNamespace,
		RunnerImages:    cfg.TerraformRunnerImages,
		OpenBaoAddr:     cfg.OpenBaoAddr,
		AuthMount:       cfg.OpenBaoRunnerAuthMount,
		AWSMount:        cfg.OpenBaoAWSMount,
		TransitMount:    cfg.OpenBaoTransitMount,
		Audience:        cfg.OpenBaoRunnerAudience,
	}
}

func (s *Server) handleEnableTerraform(w http.ResponseWriter, r *http.Request) {
	var req models.EnableTerraformRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	tfEnv, err := s.svc.EnableTerraform(r.Context(), chi.URLParam(r, "appId"), req)
	if err != nil {
		writeServiceError(w, err, "Failed to enable Terraform")
		return
	}

	writeJSON(w, http.StatusCreated, tfEnv)
}

func (s *Server) handleListTerraformEnvironments(w http.ResponseWriter, r *http.Request) {
	envs, err := s.svc.ListTerraformEnvironments(chi.URLParam(r, "appId"))
	if err != nil {
		writeServiceError(w, err, "Failed to list Terraform environments")
		return
	}

	writeJSON(w, http.StatusOK, models.ListTerraformEnvironmentsResponse{Environments: envs})
}

func (s *Server) handleUpdateTerraform(w http.ResponseWriter, r *http.Request) {
	var req models.UpdateTerraformRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
		return
	}

	tfEnv, err := s.svc.UpdateTerraform(chi.URLParam(r, "appId"), chi.URLParam(r, "environment"), req)
	if err != nil {
		writeServiceError(w, err, "Failed to update Terraform")
		return
	}

	writeJSON(w, http.StatusOK, tfEnv)
}

func (s *Server) handleSyncTerraform(w http.ResponseWriter, r *http.Request) {
	var req models.SyncTerraformRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "Invalid request body")
			return
		}
	}

	envs, err := s.svc.SyncTerraform(r.Context(), chi.URLParam(r, "appId"), req.Environment)
	if err != nil {
		writeServiceError(w, err, "Failed to sync Terraform")
		return
	}

	writeJSON(w, http.StatusOK, models.ListTerraformEnvironmentsResponse{Environments: envs})
}
