package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewClient(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
	}{
		{
			name:     "with credentials",
			username: "testuser",
			password: "testpass",
		},
		{
			name:     "without credentials",
			username: "",
			password: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.username, tt.password)
			assert.NotNil(t, client)
			assert.Equal(t, tt.username, client.username)
			assert.Equal(t, tt.password, client.password)
		})
	}
}

func TestClientCredentials(t *testing.T) {
	tests := []struct {
		name     string
		username string
		password string
	}{
		{
			name:     "empty credentials",
			username: "",
			password: "",
		},
		{
			name:     "with username only",
			username: "user",
			password: "",
		},
		{
			name:     "with both credentials",
			username: "user",
			password: "pass",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewClient(tt.username, tt.password)
			assert.Equal(t, tt.username, client.username)
			assert.Equal(t, tt.password, client.password)
		})
	}
}

func TestInvalidRepositoryFormats(t *testing.T) {
	client := NewClient("", "")

	invalidRepositories := []string{
		"",
		" ",
		"invalid repo name",
		"UPPERCASE/repo", // Most registries don't allow uppercase
		"repo:with:multiple:colons:tag",
	}

	for _, repo := range invalidRepositories {
		t.Run(repo, func(t *testing.T) {
			// ListVersions should fail with invalid repository format
			versions, err := client.ListVersions(repo, 1)
			assert.Error(t, err)
			assert.Nil(t, versions)

			// TagExists should fail with invalid repository format
			exists, err := client.TagExists(repo, "latest")
			assert.Error(t, err)
			assert.False(t, exists)
		})
	}
}

func TestLimitBehavior(t *testing.T) {
	client := NewClient("", "")

	tests := []struct {
		name  string
		limit int
	}{
		{
			name:  "negative limit",
			limit: -1,
		},
		{
			name:  "zero limit",
			limit: 0,
		},
		{
			name:  "positive limit",
			limit: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This test verifies that the function doesn't panic with different limit values
			// We use a potentially non-existent repository to avoid network dependencies
			versions, err := client.ListVersions("test/nonexistent", tt.limit)

			// We expect this to fail for a non-existent repository, but it shouldn't panic
			if err == nil {
				// If it somehow succeeds (shouldn't happen with test/nonexistent)
				assert.NotNil(t, versions)
				if tt.limit > 0 {
					assert.LessOrEqual(t, len(versions), tt.limit)
				}
			} else {
				// Expected case - repository doesn't exist
				assert.NotNil(t, err)
			}
		})
	}
}
