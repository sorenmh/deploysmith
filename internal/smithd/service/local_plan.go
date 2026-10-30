package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/sorenmh/deploysmith/internal/shared/logging"
	"github.com/sorenmh/deploysmith/internal/shared/tfbundle"
	"github.com/sorenmh/deploysmith/internal/smithd/auth"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

const (
	// defaultLocalPlanTTL is how long the temporary version of a local plan
	// lives, when it is not configured (TERRAFORM_LOCAL_PLAN_TTL)
	defaultLocalPlanTTL = 24 * time.Hour

	// localHashLength is how much of the bundle's content hash is in the id
	// of its temporary version
	localHashLength = 12
)

var (
	contentHashPattern = regexp.MustCompile(`^[0-9a-f]{12,64}$`)
	unsafeLoginChars   = regexp.MustCompile(`[^a-z0-9-]+`)
)

// BundleDeleter deletes the files of a draft version
type BundleDeleter interface {
	DeleteVersion(appName, versionID string, published bool) error
}

func (s *Service) localPlanTTL() time.Duration {
	if s.TerraformLocalPlanTTL > 0 {
		return s.TerraformLocalPlanTTL
	}
	return defaultLocalPlanTTL
}

// LocalVersionID returns the id of the temporary version for a user's
// bundle: local-<user>-<content hash>, the same for the same files
func LocalVersionID(login, contentHash string) string {
	login = strings.Trim(unsafeLoginChars.ReplaceAllString(strings.ToLower(login), "-"), "-")
	return "local-" + login + "-" + contentHash[:localHashLength]
}

// requireLocalPlanUser returns the login of the person making the request.
// Local plans are for people, signed in to smithd, not for CI's API keys:
// the point is knowing who ran the plan.
func requireLocalPlanUser(ctx context.Context) (string, error) {
	id, ok := auth.FromContext(ctx)
	if !ok || id.Kind != auth.KindUser || id.Login == "" {
		return "", forbidden("login_required", "Local plans need a signed-in user, not an API key: run `smithctl login` (CI plans a published version with `forge plan <version>`)")
	}
	return id.Login, nil
}

// requireLocalPlanOwner checks that the request is by the user who made a
// temporary version, and returns the user
func (s *Service) requireLocalPlanOwner(ctx context.Context, version *models.Version) (string, error) {
	login, err := requireLocalPlanUser(ctx)
	if err != nil {
		return "", err
	}
	if login != version.CreatedBy {
		return "", forbidden("not_your_local_plan", fmt.Sprintf("Version %s is the local plan of %s", version.VersionID, version.CreatedBy))
	}
	if version.ExpiresAt != nil && version.ExpiresAt.Before(time.Now()) {
		return "", invalid("local_plan_expired", fmt.Sprintf("Version %s has expired; run the local plan again", version.VersionID))
	}
	return login, nil
}

// checkLocalPlanBundle checks that the bundle uploaded for a temporary
// version is the one it is named after, so that the id says what was planned
func checkLocalPlanBundle(version *models.Version, bundle *tfbundle.Bundle) error {
	hash := tfbundle.ContentHash(bundle.Files)[:localHashLength]
	if !strings.HasSuffix(version.VersionID, "-"+hash) {
		return invalid("bundle_mismatch", fmt.Sprintf("The bundle uploaded for %s does not have the content hash %s it is named after; run the local plan again", version.VersionID, hash))
	}
	return nil
}

// PrepareLocalPlan makes the temporary version for the bundle with the given
// content hash, for the signed-in user, to upload the bundle to and then
// plan (PlanVersion). The id is local-<user>-<content hash>, so planning the
// same files again uses the same version, whose expiry is moved. Everything
// that can be checked before the bundle is uploaded is: Terraform enabled
// and provisioned for the app in the environment, and plan-only runs on.
func (s *Service) PrepareLocalPlan(ctx context.Context, appID string, req models.LocalPlanRequest) (*models.Application, *models.Version, error) {
	login, err := requireLocalPlanUser(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := s.requirePlanRuns(); err != nil {
		return nil, nil, err
	}
	if err := s.requireEnvironment(req.Environment); err != nil {
		return nil, nil, err
	}
	app, err := s.getApp(appID)
	if err != nil {
		return nil, nil, err
	}
	if _, err := s.terraformTarget(app, req.Environment); err != nil {
		return nil, nil, err
	}
	if !contentHashPattern.MatchString(req.ContentHash) {
		return nil, nil, invalid("invalid_request", "contentHash must be the lowercase hex SHA-256 of the bundle")
	}

	versionID := LocalVersionID(login, req.ContentHash)
	expiresAt := time.Now().Add(s.localPlanTTL())
	version, err := s.Versions.GetByVersionID(app.ID, versionID)
	switch {
	case err == nil:
		if !version.Temporary || version.CreatedBy != login {
			return nil, nil, conflict(fmt.Sprintf("Version '%s' already exists", versionID))
		}
		if ok, err := s.Versions.ExtendTemporary(version.ID, expiresAt); err != nil {
			return nil, nil, err
		} else if !ok {
			return nil, nil, fmt.Errorf("temporary version %s disappeared", versionID)
		}
		version.ExpiresAt = &expiresAt
	case err.Error() == "version not found":
		if version, err = s.Versions.CreateTemporary(app.ID, versionID, login, expiresAt); err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, fmt.Errorf("failed to get version: %w", err)
	}

	logging.LogInfoCtx(ctx, "Prepared a local plan",
		logging.Field("app", app.Name),
		logging.Field("version", versionID),
		logging.Field("environment", req.Environment),
		logging.Field("user", login),
	)
	return app, version, nil
}

// sweepTemporaryVersions deletes the temporary versions of local plans that
// expired, with their bundles and plan-only runs. A version whose plan is
// still running is kept until it is done. Several smithd replicas may do
// this at the same time: deleting is repeatable, and the bundle is deleted
// before the record, so a failure is retried by the next sweep.
func (s *Service) sweepTemporaryVersions(ctx context.Context) {
	if s.BundleDeleter == nil {
		return
	}
	expired, err := s.Versions.ListExpiredTemporary(time.Now())
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to list expired temporary versions: "+err.Error(), err)
		return
	}
	deleted := 0
	for i := range expired {
		version := &expired[i]
		app, err := s.Apps.GetByID(version.AppID)
		if err != nil {
			logging.LogErrorCtx(ctx, "Failed to get the application of a temporary version: "+err.Error(), err)
			continue
		}
		if err := s.BundleDeleter.DeleteVersion(app.Name, version.VersionID, version.Status == "published"); err != nil {
			logging.LogWarnCtx(ctx, fmt.Sprintf("Failed to delete the bundle of %s of %s: %v", version.VersionID, app.Name, err))
			continue
		}
		if err := s.Versions.DeleteTemporary(version.ID); err != nil {
			logging.LogErrorCtx(ctx, "Failed to delete a temporary version: "+err.Error(), err)
			continue
		}
		deleted++
	}
	if deleted > 0 {
		logging.LogInfoCtx(ctx, fmt.Sprintf("Deleted %d expired temporary versions", deleted))
	}
}
