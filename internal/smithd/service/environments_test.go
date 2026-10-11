package service

import (
	"context"
	"testing"

	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

func TestNameValidation(t *testing.T) {
	f := newFixture(t)

	for _, name := range []string{"", "..", ".hidden", "a/b", "../etc", "has space", "x\\y"} {
		_, err := f.svc.CreateEnvironment(models.CreateEnvironmentRequest{Name: name})
		requireKind(t, err, KindInvalid)
		_, err = f.svc.RegisterApp(name)
		requireKind(t, err, KindInvalid)
	}
	for _, name := range []string{"prod-eu", "staging_2", "v1.qa", "A1"} {
		if _, err := f.svc.CreateEnvironment(models.CreateEnvironmentRequest{Name: name}); err != nil {
			t.Errorf("CreateEnvironment(%q): %v", name, err)
		}
	}

	_, err := f.svc.CreateEnvironment(models.CreateEnvironmentRequest{Name: "staging"})
	requireKind(t, err, KindConflict)
	_, err = f.svc.RegisterApp("hello")
	requireKind(t, err, KindConflict)
}

func TestDeleteEnvironmentInUse(t *testing.T) {
	f := newFixture(t)

	envs, err := f.svc.ListEnvironments()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]string{}
	for _, env := range envs {
		byName[env.Name] = env.ID
	}

	if _, err := f.svc.DeployVersion(context.Background(), f.app.ID, "v1", "production", "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreatePolicy(f.app.ID, models.CreatePolicyRequest{Name: "p", GitBranchPattern: "main", TargetEnvironment: "staging"}); err != nil {
		t.Fatal(err)
	}

	requireKind(t, f.svc.DeleteEnvironment(byName["production"]), KindConflict)
	requireKind(t, f.svc.DeleteEnvironment(byName["staging"]), KindConflict)

	unused, err := f.svc.CreateEnvironment(models.CreateEnvironmentRequest{Name: "qa"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.DeleteEnvironment(unused.ID); err != nil {
		t.Fatalf("deleting unused environment: %v", err)
	}
	requireKind(t, f.svc.DeleteEnvironment(unused.ID), KindNotFound)
}
