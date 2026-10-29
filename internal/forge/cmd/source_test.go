package cmd

import (
	"errors"
	"testing"

	"github.com/sorenmh/deploysmith/internal/forge/client"
)

const (
	mergeSHA = "1111111111111111111111111111111111111111"
	headSHA  = "2222222222222222222222222222222222222222"
)

func TestSourceFromEnv(t *testing.T) {
	pullRequestEvent := `{"action":"synchronize","number":42,"pull_request":{"number":42,"head":{"sha":"` + headSHA + `","ref":"feature/x"}}}`

	tests := []struct {
		name   string
		env    map[string]string
		events map[string]string // event payload files by path
		want   client.VersionSource
	}{
		{"not GitHub Actions", map[string]string{}, nil, client.VersionSource{}},
		{
			"push",
			map[string]string{"GITHUB_REPOSITORY": "acme/web", "GITHUB_SHA": headSHA, "GITHUB_REF": "refs/heads/main", "GITHUB_EVENT_PATH": "/event.json"},
			map[string]string{"/event.json": `{"ref":"refs/heads/main","after":"` + headSHA + `"}`},
			client.VersionSource{Repository: "acme/web", CommitSHA: headSHA},
		},
		{
			// GITHUB_SHA is the merge commit; the PR's head is in the payload
			"pull request",
			map[string]string{"GITHUB_REPOSITORY": "acme/web", "GITHUB_SHA": mergeSHA, "GITHUB_REF": "refs/pull/42/merge", "GITHUB_EVENT_PATH": "/event.json"},
			map[string]string{"/event.json": pullRequestEvent},
			client.VersionSource{Repository: "acme/web", CommitSHA: headSHA, PullRequest: 42},
		},
		{
			// Without the payload, the ref still gives the PR number
			"pull request without readable payload",
			map[string]string{"GITHUB_REPOSITORY": "acme/web", "GITHUB_SHA": mergeSHA, "GITHUB_REF": "refs/pull/42/merge", "GITHUB_EVENT_PATH": "/missing.json"},
			nil,
			client.VersionSource{Repository: "acme/web", CommitSHA: mergeSHA, PullRequest: 42},
		},
		{
			"malformed payload",
			map[string]string{"GITHUB_REPOSITORY": "acme/web", "GITHUB_SHA": headSHA, "GITHUB_EVENT_PATH": "/event.json"},
			map[string]string{"/event.json": `not json`},
			client.VersionSource{Repository: "acme/web", CommitSHA: headSHA},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			readFile := func(path string) ([]byte, error) {
				if data, ok := tt.events[path]; ok {
					return []byte(data), nil
				}
				return nil, errors.New("no such file")
			}
			got := sourceFromEnv(func(k string) string { return tt.env[k] }, readFile)
			if got != tt.want {
				t.Errorf("sourceFromEnv() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestResolveSource(t *testing.T) {
	detected := client.VersionSource{Repository: "acme/web", CommitSHA: mergeSHA, PullRequest: 42}

	if got := resolveSource(client.VersionSource{}, "", "", 0); got != nil {
		t.Errorf("expected no source, got %+v", got)
	}
	if got := resolveSource(detected, "", "", 0); got == nil || *got != detected {
		t.Errorf("expected the detected source, got %+v", got)
	}

	got := resolveSource(detected, "acme/api", headSHA, 7)
	want := client.VersionSource{Repository: "acme/api", CommitSHA: headSHA, PullRequest: 7}
	if got == nil || *got != want {
		t.Errorf("flags should override: got %+v, want %+v", got, want)
	}
}
