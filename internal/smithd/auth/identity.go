// Package auth identifies who makes a request to smithd, a machine with a
// shared API key or a person signed in with GitHub, and manages the smithd
// sessions of the people who signed in.
package auth

import (
	"context"
	"time"
)

// Kind is how a request was authenticated
type Kind string

const (
	// KindAPIKey is a machine (e.g. CI) with a shared API key
	KindAPIKey Kind = "api-key"
	// KindUser is a person, signed in with GitHub or through the UI's
	// authenticating proxy
	KindUser Kind = "user"
)

// Identity is who made a request
type Identity struct {
	Kind Kind `json:"kind"`
	// Login is the user's GitHub login (or the proxy's user name); empty for
	// API keys
	Login string `json:"login,omitempty"`
	// ExpiresAt is when the user's session ends; nil for API keys and proxy
	// users
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// Actor names the identity in records of who did something: the user's
// login, or "api-key"
func (id Identity) Actor() string {
	if id.Kind == KindUser {
		return id.Login
	}
	return string(id.Kind)
}

type identityKey struct{}

// WithIdentity returns a copy of ctx carrying the identity
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// FromContext returns the identity of the request, if it was authenticated
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// Actor returns who made the request (a user's login, or "api-key"), or ""
// if it was not authenticated
func Actor(ctx context.Context) string {
	if id, ok := FromContext(ctx); ok {
		return id.Actor()
	}
	return ""
}
