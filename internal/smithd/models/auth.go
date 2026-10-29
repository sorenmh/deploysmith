package models

import "time"

// DeviceLoginResponse starts a smithctl login: the user enters UserCode at
// VerificationURI, while the CLI polls with DeviceCode
type DeviceLoginResponse struct {
	DeviceCode      string `json:"deviceCode"`
	UserCode        string `json:"userCode"`
	VerificationURI string `json:"verificationUri"`
	ExpiresIn       int    `json:"expiresIn"` // seconds
	Interval        int    `json:"interval"`  // seconds between polls
}

// DeviceTokenRequest polls for the outcome of a smithctl login
type DeviceTokenRequest struct {
	DeviceCode string `json:"deviceCode"`
}

// DeviceTokenPendingResponse is the answer to a poll while the user has not
// entered the code yet; poll again after Interval seconds
type DeviceTokenPendingResponse struct {
	Status   string `json:"status"` // "pending"
	Interval int    `json:"interval"`
}

// SessionResponse is a new smithd session
type SessionResponse struct {
	Token     string    `json:"token"`
	Login     string    `json:"login"`
	ExpiresAt time.Time `json:"expiresAt"`
}
