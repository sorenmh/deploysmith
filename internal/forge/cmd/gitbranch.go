package cmd

import (
	"os"
	"os/exec"
	"strings"
)

// detectGitBranch determines the current branch from well-known CI
// environment variables, falling back to the local git checkout. It returns
// "" if the branch cannot be determined (e.g. a tag build or detached HEAD).
func detectGitBranch() string {
	if branch := branchFromEnv(os.Getenv); branch != "" {
		return branch
	}

	out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	if branch := strings.TrimSpace(string(out)); branch != "HEAD" {
		return branch
	}
	return ""
}

// branchFromEnv reads the branch name from CI environment variables
func branchFromEnv(getenv func(string) string) string {
	// GitHub Actions: GITHUB_HEAD_REF is the source branch of a pull request,
	// GITHUB_REF_NAME is the branch (or tag) that triggered the run
	if branch := getenv("GITHUB_HEAD_REF"); branch != "" {
		return branch
	}
	if getenv("GITHUB_REF_TYPE") == "branch" {
		if branch := getenv("GITHUB_REF_NAME"); branch != "" {
			return branch
		}
	}

	for _, name := range []string{
		"CI_COMMIT_BRANCH",                    // GitLab branch pipelines
		"CI_MERGE_REQUEST_SOURCE_BRANCH_NAME", // GitLab merge request pipelines
		"BUILDKITE_BRANCH",                    // Buildkite
		"CIRCLE_BRANCH",                       // CircleCI
		"BRANCH_NAME",                         // Jenkins multibranch
	} {
		if branch := getenv(name); branch != "" {
			return branch
		}
	}

	return ""
}
