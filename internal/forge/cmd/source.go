package cmd

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"

	"github.com/sorenmh/deploysmith/internal/forge/client"
)

// pullRequestRef matches GITHUB_REF for pull request events
var pullRequestRef = regexp.MustCompile(`^refs/pull/(\d+)/(merge|head)$`)

// detectSource determines the repository, commit and pull request a version
// is built from, from the GitHub Actions environment. Fields it cannot
// determine are left empty.
func detectSource() client.VersionSource {
	return sourceFromEnv(os.Getenv, os.ReadFile)
}

// sourceFromEnv reads the source from GitHub Actions environment variables
// and the event payload in GITHUB_EVENT_PATH
func sourceFromEnv(getenv func(string) string, readFile func(string) ([]byte, error)) client.VersionSource {
	source := client.VersionSource{
		Repository: getenv("GITHUB_REPOSITORY"),
		CommitSHA:  getenv("GITHUB_SHA"),
	}
	if m := pullRequestRef.FindStringSubmatch(getenv("GITHUB_REF")); m != nil {
		source.PullRequest, _ = strconv.Atoi(m[1])
	}

	// For pull_request events GITHUB_SHA is a merge commit GitHub made for
	// the run, which is not on the PR branch. The PR's checks belong on its
	// head commit, which only the event payload has.
	if path := getenv("GITHUB_EVENT_PATH"); path != "" {
		if data, err := readFile(path); err == nil {
			var event struct {
				PullRequest *struct {
					Number int `json:"number"`
					Head   struct {
						SHA string `json:"sha"`
					} `json:"head"`
				} `json:"pull_request"`
			}
			if json.Unmarshal(data, &event) == nil && event.PullRequest != nil {
				if event.PullRequest.Head.SHA != "" {
					source.CommitSHA = event.PullRequest.Head.SHA
				}
				if event.PullRequest.Number > 0 {
					source.PullRequest = event.PullRequest.Number
				}
			}
		}
	}

	return source
}

// resolveSource applies the flag overrides to the detected source. It
// returns nil if nothing is known, so no source is sent.
func resolveSource(detected client.VersionSource, repo, sha string, pr int) *client.VersionSource {
	source := detected
	if repo != "" {
		source.Repository = repo
	}
	if sha != "" {
		source.CommitSHA = sha
	}
	if pr > 0 {
		source.PullRequest = pr
	}
	if source == (client.VersionSource{}) {
		return nil
	}
	return &source
}
