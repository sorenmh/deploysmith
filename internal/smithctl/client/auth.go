package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// DeviceLogin is a started login: the user enters UserCode at
// VerificationURI while the CLI polls with DeviceCode
type DeviceLogin struct {
	DeviceCode      string `json:"deviceCode"`
	UserCode        string `json:"userCode"`
	VerificationURI string `json:"verificationUri"`
	ExpiresIn       int    `json:"expiresIn"`
	Interval        int    `json:"interval"`
}

// LoginSession is the smithd session a completed login gives
type LoginSession struct {
	Token     string    `json:"token"`
	Login     string    `json:"login"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Identity is who smithd sees making the requests
type Identity struct {
	Kind      string     `json:"kind"` // "user" or "api-key"
	Login     string     `json:"login,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

// LoginPendingError is returned by PollLogin while the user has not entered
// the code yet
type LoginPendingError struct {
	// Interval is a new, longer polling interval in seconds, or 0
	Interval int
}

func (e *LoginPendingError) Error() string {
	return "login pending"
}

// apiError reads smithd's error response into an error
func apiError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var errResp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &errResp) == nil && errResp.Error.Message != "" {
		return fmt.Errorf("%s", errResp.Error.Message)
	}
	return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
}

// StartLogin starts signing in with GitHub (device flow)
func (c *Client) StartLogin() (*DeviceLogin, error) {
	httpReq, err := http.NewRequest("POST", c.joinURL("api/v1/auth/device"), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp)
	}

	var login DeviceLogin
	if err := json.NewDecoder(resp.Body).Decode(&login); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &login, nil
}

// PollLogin asks smithd whether the login completed. It returns a
// *LoginPendingError until the user entered the code.
func (c *Client) PollLogin(deviceCode string) (*LoginSession, error) {
	body, err := json.Marshal(map[string]string{"deviceCode": deviceCode})
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", c.joinURL("api/v1/auth/device/token"), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var session LoginSession
		if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}
		return &session, nil
	case http.StatusAccepted:
		var pending struct {
			Interval int `json:"interval"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&pending)
		return nil, &LoginPendingError{Interval: pending.Interval}
	default:
		return nil, apiError(resp)
	}
}

// WaitForLogin polls until the user entered the code, the code expired, or
// login failed. sleep waits between polls (time.Sleep outside tests).
func (c *Client) WaitForLogin(login *DeviceLogin, sleep func(time.Duration)) (*LoginSession, error) {
	interval := time.Duration(login.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	deadline := time.Duration(login.ExpiresIn) * time.Second

	for waited := time.Duration(0); deadline <= 0 || waited < deadline; waited += interval {
		sleep(interval)

		session, err := c.PollLogin(login.DeviceCode)
		var pending *LoginPendingError
		if errors.As(err, &pending) {
			if pending.Interval > 0 {
				interval = time.Duration(pending.Interval) * time.Second
			}
			continue
		}
		return session, err
	}
	return nil, fmt.Errorf("the code expired, run `smithctl login` again")
}

// WhoAmI returns who smithd sees making the requests
func (c *Client) WhoAmI() (*Identity, error) {
	httpReq, err := http.NewRequest("GET", c.joinURL("api/v1/auth/whoami"), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	c.setAuth(httpReq)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, apiError(resp)
	}

	var id Identity
	if err := json.NewDecoder(resp.Body).Decode(&id); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	return &id, nil
}

// Logout ends the client's session on smithd
func (c *Client) Logout() error {
	httpReq, err := http.NewRequest("POST", c.joinURL("api/v1/auth/logout"), nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	c.setAuth(httpReq)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		return apiError(resp)
	}
	return nil
}
