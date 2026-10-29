package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/sorenmh/deploysmith/internal/smithd/auth"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// Login with GitHub for smithctl, through the device flow. smithd talks to
// GitHub on smithctl's behalf, so the CLI needs no GitHub settings, and the
// GitHub token never leaves smithd: smithctl only ever holds the device code
// and, at the end, a smithd session token.

// handleStartDeviceLogin starts the device flow
func (s *Server) handleStartDeviceLogin(w http.ResponseWriter, r *http.Request) {
	if s.login == nil {
		writeError(w, http.StatusNotFound, "login_disabled", "Login with GitHub is not configured on this server")
		return
	}

	code, err := s.login.StartDeviceFlow(r.Context())
	if err != nil {
		log.Printf("Failed to start device login: %v", err)
		writeError(w, http.StatusBadGateway, "github_error", "Failed to start login with GitHub")
		return
	}

	writeJSON(w, http.StatusOK, models.DeviceLoginResponse{
		DeviceCode:      code.DeviceCode,
		UserCode:        code.UserCode,
		VerificationURI: code.VerificationURI,
		ExpiresIn:       code.ExpiresIn,
		Interval:        code.Interval,
	})
}

// handleDeviceToken polls the device flow: 202 while the user has not
// entered the code, 200 with a session once they have
func (s *Server) handleDeviceToken(w http.ResponseWriter, r *http.Request) {
	if s.login == nil {
		writeError(w, http.StatusNotFound, "login_disabled", "Login with GitHub is not configured on this server")
		return
	}

	var req models.DeviceTokenRequest
	if err := decodeJSON(r, &req); err != nil || req.DeviceCode == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "deviceCode is required")
		return
	}

	session, err := s.login.PollDeviceFlow(r.Context(), req.DeviceCode)
	var oauthErr *auth.OAuthError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, models.SessionResponse{Token: session.Token, Login: session.Login, ExpiresAt: session.ExpiresAt})
	case errors.As(err, &oauthErr):
		switch oauthErr.Code {
		case "authorization_pending":
			writeJSON(w, http.StatusAccepted, models.DeviceTokenPendingResponse{Status: "pending"})
		case "slow_down":
			writeJSON(w, http.StatusAccepted, models.DeviceTokenPendingResponse{Status: "pending", Interval: oauthErr.Interval})
		case "expired_token":
			writeError(w, http.StatusGone, "expired", "The code expired, run `smithctl login` again")
		case "access_denied":
			writeError(w, http.StatusForbidden, "access_denied", "Login was cancelled on GitHub")
		default:
			log.Printf("Device login failed: %v", err)
			writeError(w, http.StatusBadRequest, oauthErr.Code, "Login with GitHub failed: "+oauthErr.Error())
		}
	case errors.Is(err, auth.ErrNotAllowed):
		writeError(w, http.StatusForbidden, "not_allowed", "Your GitHub account may not sign in to this server")
	default:
		log.Printf("Device login failed: %v", err)
		writeError(w, http.StatusBadGateway, "github_error", "Login with GitHub failed")
	}
}

// handleWhoAmI returns the identity of the request
func (s *Server) handleWhoAmI(w http.ResponseWriter, r *http.Request) {
	id, _ := auth.FromContext(r.Context())
	writeJSON(w, http.StatusOK, id)
}

// handleLogout ends the session of the request's session token
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	token, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request", "Only a session can be logged out, not an API key")
		return
	}
	if err := s.login.Logout(token); err != nil {
		log.Printf("Failed to log out: %v", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "Failed to log out")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
