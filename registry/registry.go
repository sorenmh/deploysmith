package registry

import (
	"fmt"
	"sort"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/sorenmh/infrastructure-shared/deployment-api/models"
)

type Client struct {
	username string
	password string
}

func NewClient(username, password string) *Client {
	return &Client{
		username: username,
		password: password,
	}
}

func (c *Client) ListVersions(repository string, limit int) ([]models.ImageVersion, error) {
	ref, err := name.ParseReference(repository)
	if err != nil {
		return nil, fmt.Errorf("invalid repository: %w", err)
	}

	repo := ref.Context()

	// Set up authentication
	var opts []remote.Option
	if c.username != "" && c.password != "" {
		opts = append(opts, remote.WithAuth(&authn.Basic{
			Username: c.username,
			Password: c.password,
		}))
	} else {
		// Use default keychain (for public repos or credentials from docker config)
		opts = append(opts, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	}

	// List tags
	tags, err := remote.List(repo, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to list tags: %w", err)
	}

	// Filter out non-semantic tags if needed
	var versions []models.ImageVersion

	// Fetch metadata for each tag (up to limit)
	count := 0
	for _, tag := range tags {
		if limit > 0 && count >= limit {
			break
		}

		// Skip tags that look like sha256 digests
		if strings.HasPrefix(tag, "sha256:") {
			continue
		}

		tagRef, err := name.NewTag(fmt.Sprintf("%s:%s", repo.String(), tag))
		if err != nil {
			continue
		}

		img, err := remote.Image(tagRef, opts...)
		if err != nil {
			// Skip tags that can't be fetched
			continue
		}

		digest, err := img.Digest()
		if err != nil {
			continue
		}

		configFile, err := img.ConfigFile()
		if err != nil {
			continue
		}

		versions = append(versions, models.ImageVersion{
			Tag:       tag,
			Digest:    digest.String(),
			CreatedAt: configFile.Created.Time,
			Deployed:  false, // Will be set by caller
		})

		count++
	}

	// Sort by created time (newest first)
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].CreatedAt.After(versions[j].CreatedAt)
	})

	return versions, nil
}

func (c *Client) TagExists(repository, tag string) (bool, error) {
	ref, err := name.ParseReference(fmt.Sprintf("%s:%s", repository, tag))
	if err != nil {
		return false, fmt.Errorf("invalid reference: %w", err)
	}

	var opts []remote.Option
	if c.username != "" && c.password != "" {
		opts = append(opts, remote.WithAuth(&authn.Basic{
			Username: c.username,
			Password: c.password,
		}))
	} else {
		opts = append(opts, remote.WithAuthFromKeychain(authn.DefaultKeychain))
	}

	_, err = remote.Head(ref, opts...)
	if err != nil {
		if strings.Contains(err.Error(), "MANIFEST_UNKNOWN") || strings.Contains(err.Error(), "NOT_FOUND") {
			return false, nil
		}
		return false, err
	}

	return true, nil
}
