package models

import "time"

// Application represents a registered application
type Application struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// RegisterAppRequest is the request to register a new application
type RegisterAppRequest struct {
	Name string `json:"name"`
}

// ListAppsResponse is the response for listing applications
type ListAppsResponse struct {
	Apps   []Application `json:"apps"`
	Total  int           `json:"total"`
	Limit  int           `json:"limit"`
	Offset int           `json:"offset"`
}

// GetAppResponse is the response for getting an application
type GetAppResponse struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	CreatedAt      time.Time         `json:"createdAt"`
	CurrentVersion map[string]string `json:"currentVersion,omitempty"`
}
