package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GitHub talks to GitHub's OAuth endpoints and REST API as a GitHub App or
// OAuth App, with the user's token
type GitHub struct {
	ClientID     string
	ClientSecret string
	// BaseURL is GitHub's web URL, https://github.com or a GitHub Enterprise
	// Server URL
	BaseURL string
	// APIURL is GitHub's REST API URL, https://api.github.com or
	// <server>/api/v3
	APIURL string
	HTTP   *http.Client
}

// NewGitHub returns a GitHub client for the app with the given credentials
func NewGitHub(clientID, clientSecret, baseURL, apiURL string) *GitHub {
	return &GitHub{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		BaseURL:      strings.TrimRight(baseURL, "/"),
		APIURL:       strings.TrimRight(apiURL, "/"),
		HTTP:         &http.Client{Timeout: 15 * time.Second},
	}
}

// DeviceCode is GitHub's answer to starting the device flow
type DeviceCode struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// GitHubUser is the GitHub account a user token belongs to
type GitHubUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Name  string `json:"name"`
}

// OAuthError is an error from GitHub's OAuth token endpoint, e.g.
// "authorization_pending" while the user has not entered the device code yet
type OAuthError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
	// Interval is the new minimum polling interval with "slow_down"
	Interval int `json:"interval"`
}

func (e *OAuthError) Error() string {
	if e.Description != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Description)
	}
	return e.Code
}

// RequestDeviceCode starts the device flow
func (g *GitHub) RequestDeviceCode(ctx context.Context, scope string) (*DeviceCode, error) {
	form := url.Values{"client_id": {g.ClientID}}
	if scope != "" {
		form.Set("scope", scope)
	}

	var code DeviceCode
	if err := g.postForm(ctx, "/login/device/code", form, &code); err != nil {
		return nil, err
	}
	if code.DeviceCode == "" {
		return nil, fmt.Errorf("GitHub returned no device code")
	}
	return &code, nil
}

// PollDeviceToken asks whether the user has entered the device code yet. It
// returns the user's token, or an *OAuthError such as
// "authorization_pending", "slow_down", "expired_token" or "access_denied".
func (g *GitHub) PollDeviceToken(ctx context.Context, deviceCode string) (string, error) {
	return g.requestToken(ctx, url.Values{
		"client_id":   {g.ClientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	})
}

// AuthorizeURL is where the web flow sends the browser to sign in
func (g *GitHub) AuthorizeURL(redirectURI, state, scope string) string {
	query := url.Values{
		"client_id":    {g.ClientID},
		"redirect_uri": {redirectURI},
		"state":        {state},
	}
	if scope != "" {
		query.Set("scope", scope)
	}
	return g.BaseURL + "/login/oauth/authorize?" + query.Encode()
}

// ExchangeCode exchanges the web flow's code for the user's token
func (g *GitHub) ExchangeCode(ctx context.Context, code, redirectURI string) (string, error) {
	return g.requestToken(ctx, url.Values{
		"client_id":     {g.ClientID},
		"client_secret": {g.ClientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	})
}

func (g *GitHub) requestToken(ctx context.Context, form url.Values) (string, error) {
	var resp struct {
		AccessToken string `json:"access_token"`
		OAuthError
	}
	if err := g.postForm(ctx, "/login/oauth/access_token", form, &resp); err != nil {
		return "", err
	}
	if resp.Code != "" {
		return "", &resp.OAuthError
	}
	if resp.AccessToken == "" {
		return "", fmt.Errorf("GitHub returned no access token")
	}
	return resp.AccessToken, nil
}

// postForm posts to one of GitHub's OAuth endpoints, which answer errors
// with 200 OK and an error field
func (g *GitHub) postForm(ctx context.Context, path string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.BaseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := g.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("GitHub returned %d for %s: %s", resp.StatusCode, path, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("failed to decode GitHub response: %w", err)
	}
	return nil
}

// User returns the GitHub account of the token
func (g *GitHub) User(ctx context.Context, token string) (*GitHubUser, error) {
	var user GitHubUser
	status, err := g.getJSON(ctx, token, "/user", &user)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK || user.ID == 0 || user.Login == "" {
		return nil, fmt.Errorf("GitHub returned %d for the user", status)
	}
	return &user, nil
}

// IsOrgMember reports whether the token's user is an active member of the
// organization
func (g *GitHub) IsOrgMember(ctx context.Context, token, org string) (bool, error) {
	return g.activeMembership(ctx, token, "/user/memberships/orgs/"+url.PathEscape(org))
}

// IsTeamMember reports whether the user is an active member of the team
// (by slug) in the organization; the token must be allowed to see the team
func (g *GitHub) IsTeamMember(ctx context.Context, token, org, team, login string) (bool, error) {
	return g.activeMembership(ctx, token, fmt.Sprintf("/orgs/%s/teams/%s/memberships/%s",
		url.PathEscape(org), url.PathEscape(team), url.PathEscape(login)))
}

// activeMembership reads a membership: 200 with state "active" means
// member, 404 (and 403, when the user can't see the org) means not
func (g *GitHub) activeMembership(ctx context.Context, token, path string) (bool, error) {
	var membership struct {
		State string `json:"state"`
	}
	status, err := g.getJSON(ctx, token, path, &membership)
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusOK:
		return membership.State == "active", nil
	case http.StatusNotFound, http.StatusForbidden:
		return false, nil
	default:
		return false, fmt.Errorf("GitHub returned %d for %s", status, path)
	}
}

// getJSON gets an API path with the user's token and decodes a 200 response
// into out
func (g *GitHub) getJSON(ctx context.Context, token, path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.APIURL+path, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := g.HTTP.Do(req)
	if err != nil {
		return 0, fmt.Errorf("GitHub request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return 0, fmt.Errorf("failed to decode GitHub response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

// RevokeToken revokes the user's token once smithd has no more use for it
func (g *GitHub) RevokeToken(ctx context.Context, token string) error {
	body, err := json.Marshal(map[string]string{"access_token": token})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, g.APIURL+"/applications/"+url.PathEscape(g.ClientID)+"/token", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(g.ClientID, g.ClientSecret)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("GitHub request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("GitHub returned %d revoking the token", resp.StatusCode)
	}
	return nil
}
