package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/sorenmh/infrastructure-shared/deployment-api/models"
)

type Database struct {
	db *sql.DB
}

func New(path string) (*Database, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	d := &Database{db: db}
	if err := d.migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return d, nil
}

func (d *Database) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS deployments (
		id TEXT PRIMARY KEY,
		service_name TEXT NOT NULL,
		version TEXT NOT NULL,
		deployed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		deployed_by TEXT NOT NULL,
		git_commit TEXT NOT NULL,
		status TEXT NOT NULL,
		type TEXT NOT NULL,
		message TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_service_name ON deployments(service_name);
	CREATE INDEX IF NOT EXISTS idx_deployed_at ON deployments(deployed_at DESC);
	CREATE INDEX IF NOT EXISTS idx_status ON deployments(status);

	CREATE TABLE IF NOT EXISTS deployment_events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		deployment_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		timestamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		details TEXT,
		FOREIGN KEY (deployment_id) REFERENCES deployments(id)
	);

	CREATE INDEX IF NOT EXISTS idx_deployment_id ON deployment_events(deployment_id);
	`

	_, err := d.db.Exec(schema)
	return err
}

func (d *Database) CreateDeployment(dep *models.Deployment) error {
	_, err := d.db.Exec(`
		INSERT INTO deployments (id, service_name, version, deployed_at, deployed_by, git_commit, status, type, message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, dep.ID, dep.ServiceName, dep.Version, dep.DeployedAt, dep.DeployedBy, dep.GitCommit, dep.Status, dep.Type, dep.Message)

	return err
}

func (d *Database) UpdateDeploymentStatus(id, status string) error {
	_, err := d.db.Exec(`UPDATE deployments SET status = ? WHERE id = ?`, status, id)
	return err
}

func (d *Database) GetDeployment(id string) (*models.Deployment, error) {
	var dep models.Deployment
	err := d.db.QueryRow(`
		SELECT id, service_name, version, deployed_at, deployed_by, git_commit, status, type, message
		FROM deployments WHERE id = ?
	`, id).Scan(&dep.ID, &dep.ServiceName, &dep.Version, &dep.DeployedAt, &dep.DeployedBy, &dep.GitCommit, &dep.Status, &dep.Type, &dep.Message)

	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("deployment not found")
	}
	return &dep, err
}

func (d *Database) GetDeployments(serviceName string, limit, offset int) ([]models.Deployment, int, error) {
	// Get total count
	var total int
	err := d.db.QueryRow(`SELECT COUNT(*) FROM deployments WHERE service_name = ?`, serviceName).Scan(&total)
	if err != nil {
		return nil, 0, err
	}

	// Get deployments
	rows, err := d.db.Query(`
		SELECT id, service_name, version, deployed_at, deployed_by, git_commit, status, type, message
		FROM deployments
		WHERE service_name = ?
		ORDER BY deployed_at DESC
		LIMIT ? OFFSET ?
	`, serviceName, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var deployments []models.Deployment
	for rows.Next() {
		var dep models.Deployment
		if err := rows.Scan(&dep.ID, &dep.ServiceName, &dep.Version, &dep.DeployedAt, &dep.DeployedBy, &dep.GitCommit, &dep.Status, &dep.Type, &dep.Message); err != nil {
			return nil, 0, err
		}
		deployments = append(deployments, dep)
	}

	return deployments, total, rows.Err()
}

func (d *Database) GetCurrentDeployment(serviceName string) (*models.Deployment, error) {
	var dep models.Deployment
	err := d.db.QueryRow(`
		SELECT id, service_name, version, deployed_at, deployed_by, git_commit, status, type, message
		FROM deployments
		WHERE service_name = ? AND status = 'success'
		ORDER BY deployed_at DESC
		LIMIT 1
	`, serviceName).Scan(&dep.ID, &dep.ServiceName, &dep.Version, &dep.DeployedAt, &dep.DeployedBy, &dep.GitCommit, &dep.Status, &dep.Type, &dep.Message)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &dep, err
}

func (d *Database) AddEvent(deploymentID, eventType, details string) error {
	_, err := d.db.Exec(`
		INSERT INTO deployment_events (deployment_id, event_type, details, timestamp)
		VALUES (?, ?, ?, ?)
	`, deploymentID, eventType, details, time.Now())
	return err
}

func (d *Database) Close() error {
	return d.db.Close()
}

func (d *Database) Ping() error {
	return d.db.Ping()
}
