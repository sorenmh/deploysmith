//go:build integration

package registry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClientListVersions(t *testing.T) {
	client := NewClient("", "") // No credentials for testing

	tests := []struct {
		name       string
		repository string
		limit      int
		expectErr  bool
	}{
		{
			name:       "invalid repository format",
			repository: "invalid-repo-format",
			limit:      10,
			expectErr:  true,
		},
		{
			name:       "valid repository format but may not exist",
			repository: "nginx",
			limit:      5,
			expectErr:  false, // This might succeed with public repo
		},
		{
			name:       "zero limit",
			repository: "nginx",
			limit:      0,
			expectErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			versions, err := client.ListVersions(tt.repository, tt.limit)

			if tt.expectErr {
				assert.Error(t, err)
				assert.Nil(t, versions)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, versions)
				if tt.limit > 0 {
					assert.LessOrEqual(t, len(versions), tt.limit)
				}

				// Verify version structure if we got results
				for _, version := range versions {
					assert.NotEmpty(t, version.Tag)
					assert.NotEmpty(t, version.Digest)
					assert.False(t, version.CreatedAt.IsZero())
					assert.False(t, version.Deployed) // Should default to false
				}
			}
		})
	}
}

func TestClientTagExists(t *testing.T) {
	client := NewClient("", "") // No credentials for testing

	tests := []struct {
		name       string
		repository string
		tag        string
		expectErr  bool
		shouldExist bool
	}{
		{
			name:       "invalid repository format",
			repository: "invalid repo",
			tag:        "latest",
			expectErr:  true,
			shouldExist: false,
		},
		{
			name:       "valid format but non-existent repo",
			repository: "nonexistent/repo12345",
			tag:        "latest",
			expectErr:  false,
			shouldExist: false,
		},
		{
			name:       "existing public image",
			repository: "nginx",
			tag:        "latest",
			expectErr:  false,
			shouldExist: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists, err := client.TagExists(tt.repository, tt.tag)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.shouldExist, exists)
			}
		})
	}
}
