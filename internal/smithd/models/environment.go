package models

import "time"

// Environment represents an environment
type Environment struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// CreateEnvironmentRequest is the request to create a new environment
type CreateEnvironmentRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
}

// UpdateEnvironmentRequest is the request to update an environment
type UpdateEnvironmentRequest struct {
	Description *string `json:"description,omitempty"`
}