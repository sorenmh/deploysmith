package store

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// EnvironmentStore handles environment data operations
type EnvironmentStore struct {
	db *sql.DB
}

// NewEnvironmentStore creates a new environment store
func NewEnvironmentStore(db *sql.DB) *EnvironmentStore {
	return &EnvironmentStore{db: db}
}

// Create creates a new environment
func (s *EnvironmentStore) Create(req models.CreateEnvironmentRequest) (*models.Environment, error) {
	env := &models.Environment{
		ID:          uuid.New().String(),
		Name:        req.Name,
		Description: req.Description,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	query := `
		INSERT INTO environments (id, name, description, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`

	_, err := s.db.Exec(query, env.ID, env.Name, env.Description, env.CreatedAt, env.UpdatedAt)
	if err != nil {
		return nil, err
	}

	return env, nil
}

// GetByName gets an environment by name
func (s *EnvironmentStore) GetByName(name string) (*models.Environment, error) {
	env := &models.Environment{}
	query := `
		SELECT id, name, description, created_at, updated_at
		FROM environments
		WHERE name = ?
	`

	err := s.db.QueryRow(query, name).Scan(
		&env.ID,
		&env.Name,
		&env.Description,
		&env.CreatedAt,
		&env.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return env, nil
}

// GetByID gets an environment by ID
func (s *EnvironmentStore) GetByID(id string) (*models.Environment, error) {
	env := &models.Environment{}
	query := `
		SELECT id, name, description, created_at, updated_at
		FROM environments
		WHERE id = ?
	`

	err := s.db.QueryRow(query, id).Scan(
		&env.ID,
		&env.Name,
		&env.Description,
		&env.CreatedAt,
		&env.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return env, nil
}

// List gets all environments
func (s *EnvironmentStore) List() ([]*models.Environment, error) {
	query := `
		SELECT id, name, description, created_at, updated_at
		FROM environments
		ORDER BY name
	`

	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var environments []*models.Environment
	for rows.Next() {
		env := &models.Environment{}
		err := rows.Scan(
			&env.ID,
			&env.Name,
			&env.Description,
			&env.CreatedAt,
			&env.UpdatedAt,
		)
		if err != nil {
			return nil, err
		}
		environments = append(environments, env)
	}

	return environments, nil
}

// Update updates an environment
func (s *EnvironmentStore) Update(id string, req models.UpdateEnvironmentRequest) (*models.Environment, error) {
	// First, get the current environment
	env, err := s.GetByID(id)
	if err != nil {
		return nil, err
	}
	if env == nil {
		return nil, sql.ErrNoRows
	}

	// Update fields
	if req.Description != nil {
		env.Description = req.Description
	}
	env.UpdatedAt = time.Now()

	query := `
		UPDATE environments
		SET description = ?, updated_at = ?
		WHERE id = ?
	`

	_, err = s.db.Exec(query, env.Description, env.UpdatedAt, id)
	if err != nil {
		return nil, err
	}

	return env, nil
}

// Delete deletes an environment
func (s *EnvironmentStore) Delete(id string) error {
	query := `DELETE FROM environments WHERE id = ?`
	result, err := s.db.Exec(query, id)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return sql.ErrNoRows
	}

	return nil
}

// Exists checks if an environment exists by name
func (s *EnvironmentStore) Exists(name string) (bool, error) {
	var count int
	query := `SELECT COUNT(*) FROM environments WHERE name = ?`
	err := s.db.QueryRow(query, name).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}