package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/sorenmh/deploysmith/internal/smithd/db"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// VersionStore handles version database operations
type VersionStore struct {
	db *db.DB
}

// NewVersionStore creates a new version store
func NewVersionStore(db *db.DB) *VersionStore {
	return &VersionStore{db: db}
}

// versionColumns are scanned by scanVersion
const versionColumns = `id, app_id, version_id, status, artifact_type, git_sha, git_branch, git_committer, build_number, metadata_timestamp, created_at, published_at, temporary, expires_at, created_by`

func scanVersion(scanner interface{ Scan(...any) error }) (*models.Version, error) {
	var version models.Version
	var publishedAt, expiresAt sql.NullTime
	err := scanner.Scan(&version.ID, &version.AppID, &version.VersionID, &version.Status, &version.ArtifactType, &version.GitSHA, &version.GitBranch, &version.GitCommitter, &version.BuildNumber, &version.MetadataTimestamp, &version.CreatedAt, &publishedAt, &version.Temporary, &expiresAt, &version.CreatedBy)
	if err != nil {
		return nil, err
	}
	if publishedAt.Valid {
		version.PublishedAt = &publishedAt.Time
	}
	if expiresAt.Valid {
		version.ExpiresAt = &expiresAt.Time
	}
	return &version, nil
}

// Create creates a new version in draft status. An empty artifactType means
// manifests.
func (s *VersionStore) Create(appID, versionID, artifactType string, metadata models.VersionMetadata) (*models.Version, error) {
	if artifactType == "" {
		artifactType = models.ArtifactManifests
	}

	// Parse metadata timestamp
	metadataTimestamp, err := time.Parse(time.RFC3339, metadata.Timestamp)
	if err != nil {
		return nil, fmt.Errorf("invalid metadata timestamp: %w", err)
	}

	version := &models.Version{
		ID:                uuid.New().String(),
		AppID:             appID,
		VersionID:         versionID,
		Status:            "draft",
		ArtifactType:      artifactType,
		GitSHA:            metadata.GitSHA,
		GitBranch:         NormalizeBranch(metadata.GitBranch),
		GitCommitter:      metadata.GitCommitter,
		BuildNumber:       metadata.BuildNumber,
		MetadataTimestamp: metadataTimestamp,
		CreatedAt:         time.Now().UTC(),
	}

	_, err = s.db.Exec(`
		INSERT INTO versions (id, app_id, version_id, status, artifact_type, git_sha, git_branch, git_committer, build_number, metadata_timestamp, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, version.ID, version.AppID, version.VersionID, version.Status, version.ArtifactType, version.GitSHA, version.GitBranch, version.GitCommitter, version.BuildNumber, version.MetadataTimestamp, version.CreatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to create version: %w", err)
	}

	return version, nil
}

// CreateTemporary creates the temporary version of a local plan: a draft of
// a terraform bundle that expires at expiresAt. It has no git metadata; its
// committer is the user.
func (s *VersionStore) CreateTemporary(appID, versionID, createdBy string, expiresAt time.Time) (*models.Version, error) {
	now := time.Now().UTC()
	expiresAt = expiresAt.UTC()
	version := &models.Version{
		ID:                uuid.New().String(),
		AppID:             appID,
		VersionID:         versionID,
		Status:            "draft",
		ArtifactType:      models.ArtifactTerraform,
		GitCommitter:      createdBy,
		MetadataTimestamp: now,
		CreatedAt:         now,
		Temporary:         true,
		ExpiresAt:         &expiresAt,
		CreatedBy:         createdBy,
	}

	_, err := s.db.Exec(`
		INSERT INTO versions (id, app_id, version_id, status, artifact_type, git_sha, git_branch, git_committer, build_number, metadata_timestamp, created_at, temporary, expires_at, created_by)
		VALUES (?, ?, ?, ?, ?, '', '', ?, '', ?, ?, ?, ?, ?)
	`, version.ID, version.AppID, version.VersionID, version.Status, version.ArtifactType, version.GitCommitter, version.MetadataTimestamp, version.CreatedAt, true, expiresAt, createdBy)
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary version: %w", err)
	}
	return version, nil
}

// ExtendTemporary moves the expiry of a temporary version, e.g. when the
// same bundle is planned again. It returns false if there is no such
// temporary version.
func (s *VersionStore) ExtendTemporary(id string, expiresAt time.Time) (bool, error) {
	result, err := s.db.Exec(`UPDATE versions SET expires_at = ? WHERE id = ? AND temporary`, expiresAt.UTC(), id)
	if err != nil {
		return false, fmt.Errorf("failed to extend temporary version: %w", err)
	}
	return rowsChanged(result)
}

// ListExpiredTemporary returns the temporary versions that expired before
// now and have no plan-only run that is still being planned
func (s *VersionStore) ListExpiredTemporary(now time.Time) ([]models.Version, error) {
	rows, err := s.db.Query(`
		SELECT `+versionColumns+`
		FROM versions
		WHERE temporary AND expires_at < ?
		AND NOT EXISTS (SELECT 1 FROM plan_runs r WHERE r.version_id = versions.id AND r.status IN `+activePlanRunStatuses+`)
		ORDER BY expires_at
	`, now.UTC())
	if err != nil {
		return nil, fmt.Errorf("failed to list expired temporary versions: %w", err)
	}
	defer rows.Close()

	versions := []models.Version{}
	for rows.Next() {
		version, err := scanVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan version: %w", err)
		}
		versions = append(versions, *version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read versions: %w", err)
	}
	return versions, nil
}

// DeleteTemporary deletes a temporary version with its plan-only runs. It
// never deletes a version that is not temporary.
func (s *VersionStore) DeleteTemporary(id string) error {
	if _, err := s.db.Exec(`DELETE FROM versions WHERE id = ? AND temporary`, id); err != nil {
		return fmt.Errorf("failed to delete temporary version: %w", err)
	}
	return nil
}

// GetByID gets a version by its internal ID
func (s *VersionStore) GetByID(id string) (*models.Version, error) {
	version, err := scanVersion(s.db.QueryRow(`SELECT `+versionColumns+` FROM versions WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("version not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get version: %w", err)
	}
	return version, nil
}

// GetByVersionID gets a version by app ID and version ID
func (s *VersionStore) GetByVersionID(appID, versionID string) (*models.Version, error) {
	version, err := scanVersion(s.db.QueryRow(`SELECT `+versionColumns+` FROM versions WHERE app_id = ? AND version_id = ?`, appID, versionID))
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("version not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get version: %w", err)
	}
	return version, nil
}

// SetSource records where a version was built from
func (s *VersionStore) SetSource(id string, source models.VersionSource) error {
	result, err := s.db.Exec(`
		UPDATE versions SET source_repo = ?, source_sha = ?, source_pr = ? WHERE id = ?
	`, source.Repository, source.CommitSHA, source.PullRequest, id)
	if err != nil {
		return fmt.Errorf("failed to set version source: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("version not found")
	}
	return nil
}

// GetSource returns where a version was built from, empty if not known
func (s *VersionStore) GetSource(id string) (models.VersionSource, error) {
	var source models.VersionSource
	err := s.db.QueryRow(`
		SELECT source_repo, source_sha, source_pr FROM versions WHERE id = ?
	`, id).Scan(&source.Repository, &source.CommitSHA, &source.PullRequest)
	if err == sql.ErrNoRows {
		return source, fmt.Errorf("version not found")
	}
	if err != nil {
		return source, fmt.Errorf("failed to get version source: %w", err)
	}
	return source, nil
}

// UpdateStatus updates the version status
func (s *VersionStore) UpdateStatus(id, status string) error {
	result, err := s.db.Exec(`
		UPDATE versions
		SET status = ?, published_at = CASE WHEN ? = 'published' THEN CURRENT_TIMESTAMP ELSE published_at END
		WHERE id = ?
	`, status, status, id)

	if err != nil {
		return fmt.Errorf("failed to update version status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("version not found")
	}

	return nil
}

// List lists versions for an application with pagination. Temporary
// versions (local plans) are left out unless includeTemporary is set.
func (s *VersionStore) List(appID string, limit, offset int, includeTemporary bool) ([]models.Version, int, error) {
	filter := " AND NOT temporary"
	if includeTemporary {
		filter = ""
	}

	// Get total count
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM versions WHERE app_id = ?"+filter, appID).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count versions: %w", err)
	}

	// Get versions
	rows, err := s.db.Query(`
		SELECT `+versionColumns+`
		FROM versions
		WHERE app_id = ?`+filter+`
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, appID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list versions: %w", err)
	}
	defer rows.Close()

	versions := []models.Version{}
	for rows.Next() {
		version, err := scanVersion(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan version: %w", err)
		}
		versions = append(versions, *version)
	}

	return versions, total, nil
}

// GetDeployedEnvironments gets the environments where a version is deployed
func (s *VersionStore) GetDeployedEnvironments(versionID string) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT d.environment
		FROM deployments d
		JOIN versions v ON d.version_id = v.id
		WHERE v.id = ? AND d.status IN `+deployedStatuses+`
		ORDER BY d.environment
	`, versionID)

	if err != nil {
		return nil, fmt.Errorf("failed to get deployed environments: %w", err)
	}
	defer rows.Close()

	environments := []string{}
	for rows.Next() {
		var env string
		if err := rows.Scan(&env); err != nil {
			return nil, fmt.Errorf("failed to scan environment: %w", err)
		}
		environments = append(environments, env)
	}

	return environments, nil
}
