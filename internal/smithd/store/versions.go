package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/deploysmith/deploysmith/internal/smithd/models"
	"github.com/google/uuid"
)

// VersionStore handles version database operations
type VersionStore struct {
	db *sql.DB
}

// NewVersionStore creates a new version store
func NewVersionStore(db *sql.DB) *VersionStore {
	return &VersionStore{db: db}
}

// Create creates a new version in draft status
func (s *VersionStore) Create(appID, versionID string, metadata models.VersionMetadata) (*models.Version, error) {
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
		GitSHA:            metadata.GitSHA,
		GitBranch:         metadata.GitBranch,
		GitCommitter:      metadata.GitCommitter,
		BuildNumber:       metadata.BuildNumber,
		MetadataTimestamp: metadataTimestamp,
		CreatedAt:         time.Now().UTC(),
	}

	_, err = s.db.Exec(`
		INSERT INTO versions (id, app_id, version_id, status, git_sha, git_branch, git_committer, build_number, metadata_timestamp, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, version.ID, version.AppID, version.VersionID, version.Status, version.GitSHA, version.GitBranch, version.GitCommitter, version.BuildNumber, version.MetadataTimestamp, version.CreatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to create version: %w", err)
	}

	return version, nil
}

// GetByVersionID gets a version by app ID and version ID
func (s *VersionStore) GetByVersionID(appID, versionID string) (*models.Version, error) {
	var version models.Version
	var publishedAt sql.NullTime

	err := s.db.QueryRow(`
		SELECT id, app_id, version_id, status, git_sha, git_branch, git_committer, build_number, metadata_timestamp, created_at, published_at
		FROM versions
		WHERE app_id = ? AND version_id = ?
	`, appID, versionID).Scan(&version.ID, &version.AppID, &version.VersionID, &version.Status, &version.GitSHA, &version.GitBranch, &version.GitCommitter, &version.BuildNumber, &version.MetadataTimestamp, &version.CreatedAt, &publishedAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("version not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get version: %w", err)
	}

	if publishedAt.Valid {
		version.PublishedAt = &publishedAt.Time
	}

	return &version, nil
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

// List lists versions for an application with pagination
func (s *VersionStore) List(appID string, limit, offset int) ([]models.Version, int, error) {
	// Get total count
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM versions WHERE app_id = ?", appID).Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count versions: %w", err)
	}

	// Get versions
	rows, err := s.db.Query(`
		SELECT id, app_id, version_id, status, git_sha, git_branch, git_committer, build_number, metadata_timestamp, created_at, published_at
		FROM versions
		WHERE app_id = ?
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, appID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list versions: %w", err)
	}
	defer rows.Close()

	versions := []models.Version{}
	for rows.Next() {
		var version models.Version
		var publishedAt sql.NullTime

		err := rows.Scan(&version.ID, &version.AppID, &version.VersionID, &version.Status, &version.GitSHA, &version.GitBranch, &version.GitCommitter, &version.BuildNumber, &version.MetadataTimestamp, &version.CreatedAt, &publishedAt)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan version: %w", err)
		}

		if publishedAt.Valid {
			version.PublishedAt = &publishedAt.Time
		}

		versions = append(versions, version)
	}

	return versions, total, nil
}

// GetDeployedEnvironments gets the environments where a version is deployed
func (s *VersionStore) GetDeployedEnvironments(versionID string) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT d.environment
		FROM deployments d
		JOIN versions v ON d.version_id = v.id
		WHERE v.id = ? AND d.status = 'success'
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
