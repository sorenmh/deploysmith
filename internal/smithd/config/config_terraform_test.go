package config

import (
	"maps"
	"testing"
)

func TestSSHURL(t *testing.T) {
	for in, want := range map[string]string{
		"git@github.com:sorenmh/gitops.git":       "ssh://git@github.com/sorenmh/gitops.git",
		"ssh://git@github.com/sorenmh/gitops.git": "ssh://git@github.com/sorenmh/gitops.git",
		"https://github.com/sorenmh/gitops.git":   "https://github.com/sorenmh/gitops.git",
		"/srv/git/gitops.git":                     "/srv/git/gitops.git",
	} {
		if got := sshURL(in); got != want {
			t.Errorf("sshURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseRunnerImages(t *testing.T) {
	images, err := parseRunnerImages("1.12.9=ghcr.io/x/runner:a, 1.13.2 = ghcr.io/x/runner:b@sha256:00")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"1.12.9": "ghcr.io/x/runner:a", "1.13.2": "ghcr.io/x/runner:b@sha256:00"}
	if !maps.Equal(images, want) {
		t.Errorf("images = %v, want %v", images, want)
	}

	for _, bad := range []string{"1.12.9", "=image", "1.12.9="} {
		if _, err := parseRunnerImages(bad); err == nil {
			t.Errorf("parseRunnerImages(%q) succeeded", bad)
		}
	}
}

func TestLoadPlanOnlyRuns(t *testing.T) {
	t.Run("on by default, on the plans branch", func(t *testing.T) {
		setRequired(t)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if !cfg.TerraformAutoPlan || cfg.TerraformPlansBranch != "plans" {
			t.Errorf("auto plan = %v, plans branch = %q", cfg.TerraformAutoPlan, cfg.TerraformPlansBranch)
		}
	})

	t.Run("configured", func(t *testing.T) {
		setRequired(t)
		t.Setenv("TERRAFORM_AUTO_PLAN", "false")
		t.Setenv("TERRAFORM_PLANS_BRANCH", "plan-only")
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.TerraformAutoPlan || cfg.TerraformPlansBranch != "plan-only" {
			t.Errorf("auto plan = %v, plans branch = %q", cfg.TerraformAutoPlan, cfg.TerraformPlansBranch)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		setRequired(t)
		t.Setenv("TERRAFORM_AUTO_PLAN", "sometimes")
		if _, err := Load(); err == nil {
			t.Error("Load accepted TERRAFORM_AUTO_PLAN=sometimes")
		}
	})
}
