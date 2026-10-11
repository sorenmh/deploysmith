package store

import "testing"

func TestMatchesBranchPattern(t *testing.T) {
	tests := []struct {
		branch, pattern string
		want            bool
	}{
		{"main", "main", true},
		{"refs/heads/main", "main", true},
		{"main", "refs/heads/main", true},
		{"release/1.2", "release/*", true},
		{"refs/heads/release/1.2", "release/*", true},
		{"feature/x", "main", false},
		{"unknown", "main", false},
	}

	for _, tt := range tests {
		if got := matchesBranchPattern(tt.branch, tt.pattern); got != tt.want {
			t.Errorf("matchesBranchPattern(%q, %q) = %v, want %v", tt.branch, tt.pattern, got, tt.want)
		}
	}
}
