package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/deploysmith/deploysmith/internal/smithd/models"
	"github.com/google/uuid"
)

// ApplicationStore handles application database operations
type ApplicationStore struct {
	db *sql.DB
}

// NewApplicationStore creates a new application store
func NewApplicationStore(db *sql.DB) *ApplicationStore {
	return &ApplicationStore{db: db}
}

// Create creates a new application
func (s *ApplicationStore) Create(name string) (*models.Application, error) {
	// Check if app already exists
	var exists bool
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM applications WHERE name = ?)", name).Scan(&exists)
	if err != nil {
		return nil, fmt.Errorf("failed to check if app exists: %w", err)
	}
	if exists {
		return nil, fmt.Errorf("application with name '%s' already exists", name)
	}

	app := &models.Application{
		ID:        uuid.New().String(),
		Name:      name,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}

	_, err = s.db.Exec(`
		INSERT INTO applications (id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?)
	`, app.ID, app.Name, app.CreatedAt, app.UpdatedAt)

	if err != nil {
		return nil, fmt.Errorf("failed to create application: %w", err)
	}

	return app, nil
}

// List lists all applications with pagination
func (s *ApplicationStore) List(limit, offset int) ([]models.Application, int, error) {
	// Get total count
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM applications").Scan(&total)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count applications: %w", err)
	}

	// Get applications
	rows, err := s.db.Query(`
		SELECT id, name, created_at, updated_at
		FROM applications
		ORDER BY created_at DESC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list applications: %w", err)
	}
	defer rows.Close()

	apps := []models.Application{}
	for rows.Next() {
		var app models.Application
		err := rows.Scan(&app.ID, &app.Name, &app.CreatedAt, &app.UpdatedAt)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan application: %w", err)
		}
		apps = append(apps, app)
	}

	return apps, total, nil
}

// GetByID gets an application by ID
func (s *ApplicationStore) GetByID(id string) (*models.Application, error) {
	var app models.Application
	err := s.db.QueryRow(`
		SELECT id, name, created_at, updated_at
		FROM applications
		WHERE id = ?
	`, id).Scan(&app.ID, &app.Name, &app.CreatedAt, &app.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("application not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get application: %w", err)
	}

	return &app, nil
}

// GetByName gets an application by name
func (s *ApplicationStore) GetByName(name string) (*models.Application, error) {
	var app models.Application
	err := s.db.QueryRow(`
		SELECT id, name, created_at, updated_at
		FROM applications
		WHERE name = ?
	`, name).Scan(&app.ID, &app.Name, &app.CreatedAt, &app.UpdatedAt)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("application not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get application: %w", err)
	}

	return &app, nil
}

// GetCurrentVersions gets the currently deployed version for each environment
func (s *ApplicationStore) GetCurrentVersions(appID string) (map[string]string, error) {
	rows, err := s.db.Query(`
		SELECT d.environment, v.version_id
		FROM deployments d
		JOIN versions v ON d.version_id = v.id
		WHERE d.app_id = ?
		  AND d.status = 'success'
		  AND d.completed_at = (
			SELECT MAX(d2.completed_at)
			FROM deployments d2
			WHERE d2.app_id = d.app_id
			  AND d2.environment = d.environment
			  AND d2.status = 'success'
		  )
		ORDER BY d.environment
	`, appID)

	if err != nil {
		return nil, fmt.Errorf("failed to get current versions: %w", err)
	}
	defer rows.Close()

	versions := make(map[string]string)
	for rows.Next() {
		var env, version string
		if err := rows.Scan(&env, &version); err != nil {
			return nil, fmt.Errorf("failed to scan version: %w", err)
		}
		versions[env] = version
	}

	return versions, nil
}
