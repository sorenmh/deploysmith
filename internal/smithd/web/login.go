package web

import (
	"crypto/subtle"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/sorenmh/deploysmith/internal/smithd/auth"
)

// Signing in to the UI with GitHub's OAuth web flow: the sign-in page sends
// the browser to GitHub with a random state, which is also kept in a
// short-lived cookie; GitHub's callback must bring the same state back, so a
// sign-in can't be started by another site (login CSRF).

// stateCookie holds the state and the page to return to during sign-in
const stateCookie = "smithd_oauth_state"

// loginPage is the data of the sign-in page
type loginPage struct {
	Next  string
	Error string
}

// handleLogin shows the sign-in page
func (ui *UI) handleLogin(w http.ResponseWriter, r *http.Request) {
	next := safeNext(r.URL.Query().Get("next"))
	if _, ok := sessionIdentity(ui.auth.Login, r); ok {
		http.Redirect(w, r, next, http.StatusFound)
		return
	}
	ui.renderLogin(w, r, http.StatusOK, next, "")
}

func (ui *UI) renderLogin(w http.ResponseWriter, r *http.Request, status int, next, message string) {
	ui.render(w, r, status, "login", page{Title: "Sign in", Data: loginPage{Next: next, Error: message}})
}

// handleLoginGitHub sends the browser to GitHub to sign in
func (ui *UI) handleLoginGitHub(w http.ResponseWriter, r *http.Request) {
	state, err := auth.NewState()
	if err != nil {
		log.Printf("web: %v", err)
		ui.renderError(w, r, http.StatusInternalServerError, "Something went wrong on the server. Check the smithd logs for details.")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     stateCookie,
		Value:    url.Values{"state": {state}, "next": {safeNext(r.URL.Query().Get("next"))}}.Encode(),
		Path:     Prefix + "/auth",
		MaxAge:   10 * 60,
		HttpOnly: true,
		Secure:   ui.secure(r),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, ui.auth.Login.AuthorizeURL(ui.callbackURL(r), state), http.StatusFound)
}

// handleCallback completes signing in when GitHub sends the browser back
func (ui *UI) handleCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	// The state cookie is single-use
	var saved url.Values
	if cookie, err := r.Cookie(stateCookie); err == nil {
		saved, _ = url.ParseQuery(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Path: Prefix + "/auth", MaxAge: -1, HttpOnly: true, Secure: ui.secure(r), SameSite: http.SameSiteLaxMode})
	next := safeNext(saved.Get("next"))

	state := saved.Get("state")
	if state == "" || subtle.ConstantTimeCompare([]byte(state), []byte(query.Get("state"))) != 1 {
		ui.renderLogin(w, r, http.StatusBadRequest, next, "This sign-in expired or was not started here. Please sign in again.")
		return
	}
	if query.Get("error") != "" {
		ui.renderLogin(w, r, http.StatusForbidden, next, "Sign-in was cancelled on GitHub.")
		return
	}

	session, err := ui.auth.Login.CompleteWebLogin(r.Context(), query.Get("code"), ui.callbackURL(r))
	if errors.Is(err, auth.ErrNotAllowed) {
		ui.renderLogin(w, r, http.StatusForbidden, next, "Your GitHub account may not sign in here.")
		return
	}
	if err != nil {
		log.Printf("web: sign-in with GitHub failed: %v", err)
		ui.renderLogin(w, r, http.StatusBadGateway, next, "Signing in with GitHub failed. Please try again.")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    session.Token,
		Path:     Prefix,
		Expires:  session.ExpiresAt,
		HttpOnly: true,
		Secure:   ui.secure(r),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// handleLogout ends the session
func (ui *UI) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if err := ui.auth.Login.Logout(cookie.Value); err != nil {
			log.Printf("web: %v", err)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: Prefix, MaxAge: -1, HttpOnly: true, Secure: ui.secure(r), SameSite: http.SameSiteLaxMode})
	redirect(w, r, Prefix+"/auth/login")
}

// secure reports whether the UI is served over https, so cookies must be
// Secure
func (ui *UI) secure(r *http.Request) bool {
	if ui.auth.PublicURL != "" {
		return strings.HasPrefix(ui.auth.PublicURL, "https://")
	}
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

// callbackURL is where GitHub sends the browser back to; it must match the
// callback URL of the GitHub App
func (ui *UI) callbackURL(r *http.Request) string {
	base := strings.TrimRight(ui.auth.PublicURL, "/")
	if base == "" {
		scheme := "http"
		if ui.secure(r) {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	return base + Prefix + "/auth/callback"
}

// safeNext returns next if it is a UI page, so sign-in can't redirect
// elsewhere
func safeNext(next string) string {
	if next == Prefix || strings.HasPrefix(next, Prefix+"/") {
		return next
	}
	return Prefix + "/"
}
