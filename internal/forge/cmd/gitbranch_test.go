package cmd

import "testing"

func TestBranchFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"none", map[string]string{}, ""},
		{"github push", map[string]string{"GITHUB_REF_TYPE": "branch", "GITHUB_REF_NAME": "main"}, "main"},
		{"github tag", map[string]string{"GITHUB_REF_TYPE": "tag", "GITHUB_REF_NAME": "v1.0.0"}, ""},
		{"github pull request", map[string]string{"GITHUB_HEAD_REF": "feature/x", "GITHUB_REF_TYPE": "branch", "GITHUB_REF_NAME": "42/merge"}, "feature/x"},
		{"gitlab", map[string]string{"CI_COMMIT_BRANCH": "main"}, "main"},
		{"jenkins", map[string]string{"BRANCH_NAME": "release/1"}, "release/1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := branchFromEnv(func(k string) string { return tt.env[k] })
			if got != tt.want {
				t.Errorf("branchFromEnv() = %q, want %q", got, tt.want)
			}
		})
	}
}
