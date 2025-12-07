package db

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sorenmh/infrastructure-shared/deployment-api/models"
)

func setupTestDB(t *testing.T) *Database {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := New(dbPath)
	require.NoError(t, err)

	return db
}

func TestNew(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := New(dbPath)
	assert.NoError(t, err)
	assert.NotNil(t, db)

	// Verify database file was created
	assert.FileExists(t, dbPath)

	// Verify we can ping the database
	err = db.Ping()
	assert.NoError(t, err)

	// Clean up
	db.Close()
}

func TestNewInvalidPath(t *testing.T) {
	// Try to create database in non-existent directory without permissions
	db, err := New("/invalid/path/test.db")
	assert.Error(t, err)
	assert.Nil(t, db)
}

func TestMigrate(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := New(dbPath)
	require.NoError(t, err)
	defer db.Close()

	// Verify tables were created
	var count int
	err = db.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='deployments'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	err = db.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='deployment_events'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify indexes were created
	err = db.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_service_name'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestCreateDeployment(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	deployment := &models.Deployment{
		ID:          "test-deployment-1",
		ServiceName: "test-service",
		Version:     "v1.0.0",
		DeployedAt:  time.Now(),
		DeployedBy:  "test@example.com",
		GitCommit:   "abc123",
		Status:      "success",
		Type:        "deploy",
		Message:     "Test deployment",
	}

	err := db.CreateDeployment(deployment)
	assert.NoError(t, err)

	// Verify deployment was created
	var count int
	err = db.db.QueryRow("SELECT COUNT(*) FROM deployments WHERE id = ?", deployment.ID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

func TestGetDeployment(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	now := time.Now().Truncate(time.Second) // Truncate for comparison

	deployment := &models.Deployment{
		ID:          "test-deployment-1",
		ServiceName: "test-service",
		Version:     "v1.0.0",
		DeployedAt:  now,
		DeployedBy:  "test@example.com",
		GitCommit:   "abc123",
		Status:      "success",
		Type:        "deploy",
		Message:     "Test deployment",
	}

	// Create deployment
	err := db.CreateDeployment(deployment)
	require.NoError(t, err)

	// Get deployment
	retrieved, err := db.GetDeployment(deployment.ID)
	require.NoError(t, err)
	require.NotNil(t, retrieved)

	assert.Equal(t, deployment.ID, retrieved.ID)
	assert.Equal(t, deployment.ServiceName, retrieved.ServiceName)
	assert.Equal(t, deployment.Version, retrieved.Version)
	assert.Equal(t, deployment.DeployedBy, retrieved.DeployedBy)
	assert.Equal(t, deployment.GitCommit, retrieved.GitCommit)
	assert.Equal(t, deployment.Status, retrieved.Status)
	assert.Equal(t, deployment.Type, retrieved.Type)
	assert.Equal(t, deployment.Message, retrieved.Message)
	// Note: Time comparison might need adjustment due to SQLite timestamp precision
}

func TestGetDeploymentNotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	deployment, err := db.GetDeployment("non-existent")
	assert.Error(t, err)
	assert.Nil(t, deployment)
	assert.Contains(t, err.Error(), "deployment not found")
}

func TestUpdateDeploymentStatus(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	deployment := &models.Deployment{
		ID:          "test-deployment-1",
		ServiceName: "test-service",
		Version:     "v1.0.0",
		DeployedAt:  time.Now(),
		DeployedBy:  "test@example.com",
		GitCommit:   "abc123",
		Status:      "pending",
		Type:        "deploy",
		Message:     "Test deployment",
	}

	// Create deployment
	err := db.CreateDeployment(deployment)
	require.NoError(t, err)

	// Update status
	err = db.UpdateDeploymentStatus(deployment.ID, "success")
	assert.NoError(t, err)

	// Verify status was updated
	retrieved, err := db.GetDeployment(deployment.ID)
	require.NoError(t, err)
	assert.Equal(t, "success", retrieved.Status)
}

func TestGetDeployments(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	serviceName := "test-service"

	// Create multiple deployments
	for i := 0; i < 5; i++ {
		deployment := &models.Deployment{
			ID:          fmt.Sprintf("deployment-%d", i),
			ServiceName: serviceName,
			Version:     fmt.Sprintf("v1.0.%d", i),
			DeployedAt:  time.Now().Add(time.Duration(i) * time.Minute),
			DeployedBy:  "test@example.com",
			GitCommit:   fmt.Sprintf("commit%d", i),
			Status:      "success",
			Type:        "deploy",
			Message:     fmt.Sprintf("Test deployment %d", i),
		}

		err := db.CreateDeployment(deployment)
		require.NoError(t, err)
	}

	// Get deployments with pagination
	deployments, total, err := db.GetDeployments(serviceName, 3, 0)
	require.NoError(t, err)

	assert.Equal(t, 5, total)
	assert.Len(t, deployments, 3)

	// Verify they are ordered by deployed_at DESC (most recent first)
	for i := 0; i < len(deployments)-1; i++ {
		assert.True(t, deployments[i].DeployedAt.After(deployments[i+1].DeployedAt) ||
			deployments[i].DeployedAt.Equal(deployments[i+1].DeployedAt))
	}

	// Test second page
	deployments, total, err = db.GetDeployments(serviceName, 3, 3)
	require.NoError(t, err)

	assert.Equal(t, 5, total)
	assert.Len(t, deployments, 2) // Remaining 2 deployments
}

func TestGetCurrentDeployment(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	serviceName := "test-service"

	// Create deployments with different statuses
	deployments := []*models.Deployment{
		{
			ID:          "old-deployment",
			ServiceName: serviceName,
			Version:     "v1.0.0",
			DeployedAt:  time.Now().Add(-2 * time.Hour),
			DeployedBy:  "test@example.com",
			GitCommit:   "old123",
			Status:      "success",
			Type:        "deploy",
		},
		{
			ID:          "failed-deployment",
			ServiceName: serviceName,
			Version:     "v1.0.1",
			DeployedAt:  time.Now().Add(-1 * time.Hour),
			DeployedBy:  "test@example.com",
			GitCommit:   "failed123",
			Status:      "failed",
			Type:        "deploy",
		},
		{
			ID:          "current-deployment",
			ServiceName: serviceName,
			Version:     "v1.0.2",
			DeployedAt:  time.Now(),
			DeployedBy:  "test@example.com",
			GitCommit:   "current123",
			Status:      "success",
			Type:        "deploy",
		},
	}

	for _, deployment := range deployments {
		err := db.CreateDeployment(deployment)
		require.NoError(t, err)
	}

	// Get current deployment (should be the most recent successful one)
	current, err := db.GetCurrentDeployment(serviceName)
	require.NoError(t, err)
	require.NotNil(t, current)

	assert.Equal(t, "current-deployment", current.ID)
	assert.Equal(t, "v1.0.2", current.Version)
	assert.Equal(t, "success", current.Status)
}

func TestGetCurrentDeploymentNotFound(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	current, err := db.GetCurrentDeployment("non-existent-service")
	assert.NoError(t, err)
	assert.Nil(t, current)
}

func TestAddEvent(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	deploymentID := "test-deployment-1"
	deployment := &models.Deployment{
		ID:          deploymentID,
		ServiceName: "test-service",
		Version:     "v1.0.0",
		DeployedAt:  time.Now(),
		DeployedBy:  "test@example.com",
		GitCommit:   "abc123",
		Status:      "success",
		Type:        "deploy",
	}

	// Create deployment
	err := db.CreateDeployment(deployment)
	require.NoError(t, err)

	// Add event
	err = db.AddEvent(deploymentID, "validation", "Service definition validated successfully")
	assert.NoError(t, err)

	// Verify event was created
	var count int
	err = db.db.QueryRow("SELECT COUNT(*) FROM deployment_events WHERE deployment_id = ?", deploymentID).Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify event details
	var eventType, details string
	var timestamp time.Time
	err = db.db.QueryRow(`
		SELECT event_type, details, timestamp
		FROM deployment_events
		WHERE deployment_id = ?
	`, deploymentID).Scan(&eventType, &details, &timestamp)
	require.NoError(t, err)

	assert.Equal(t, "validation", eventType)
	assert.Equal(t, "Service definition validated successfully", details)
	assert.True(t, timestamp.Before(time.Now().Add(time.Second))) // Should be recent
}

func TestClose(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	db, err := New(dbPath)
	require.NoError(t, err)

	// Close the database
	err = db.Close()
	assert.NoError(t, err)

	// Verify we can't use the database after closing
	err = db.Ping()
	assert.Error(t, err)
}

func TestPing(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	err := db.Ping()
	assert.NoError(t, err)
}