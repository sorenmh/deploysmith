package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/store"
)

// tokenPrefix marks smithd session tokens, so they are recognisable (e.g.
// by secret scanners)
const tokenPrefix = "smithd_"

var (
	// ErrNotAllowed is returned when the GitHub user may not sign in
	ErrNotAllowed = errors.New("not allowed to sign in")
	// ErrInvalidSession is returned for an unknown session token
	ErrInvalidSession = errors.New("invalid session token")
	// ErrSessionExpired is returned for a session token that has expired
	ErrSessionExpired = errors.New("session expired")
)

// Config configures login with GitHub
type Config struct {
	GitHub *GitHub
	// AllowedOrg lets the members of this organization sign in
	AllowedOrg string
	// AllowedTeam narrows AllowedOrg to the members of this team (slug)
	AllowedTeam string
	// AllowedUsers lets these GitHub logins sign in, whether or not they are
	// in AllowedOrg
	AllowedUsers []string
	// SessionTTL is how long a smithd session lasts
	SessionTTL time.Duration
}

// Login signs people in with GitHub and keeps their smithd sessions. The
// GitHub token is only used during sign-in, to identify the user and check
// that they may sign in, and is revoked right after.
type Login struct {
	cfg      Config
	users    *store.UserStore
	sessions *store.SessionStore
	now      func() time.Time
}

// NewLogin returns the login service
func NewLogin(cfg Config, users *store.UserStore, sessions *store.SessionStore) *Login {
	return &Login{cfg: cfg, users: users, sessions: sessions, now: time.Now}
}

// Session is a new smithd session. The token is only known here; smithd
// stores its hash.
type Session struct {
	Token     string
	Login     string
	ExpiresAt time.Time
}

// scope is the OAuth scope to ask for: reading org and team memberships
// needs read:org with an OAuth App. GitHub Apps ignore it and use the app's
// "Members" permission instead.
func (l *Login) scope() string {
	if l.cfg.AllowedOrg != "" {
		return "read:org"
	}
	return ""
}

// StartDeviceFlow starts signing in a CLI user. smithctl shows the user
// code and polls PollDeviceFlow with the device code.
func (l *Login) StartDeviceFlow(ctx context.Context) (*DeviceCode, error) {
	return l.cfg.GitHub.RequestDeviceCode(ctx, l.scope())
}

// PollDeviceFlow completes the device flow once the user entered the code.
// Until then it returns GitHub's *OAuthError ("authorization_pending",
// "slow_down", ...).
func (l *Login) PollDeviceFlow(ctx context.Context, deviceCode string) (*Session, error) {
	token, err := l.cfg.GitHub.PollDeviceToken(ctx, deviceCode)
	if err != nil {
		return nil, err
	}
	return l.signIn(ctx, token, store.SessionCLI)
}

// AuthorizeURL is where the web UI sends the browser to sign in
func (l *Login) AuthorizeURL(redirectURI, state string) string {
	return l.cfg.GitHub.AuthorizeURL(redirectURI, state, l.scope())
}

// CompleteWebLogin signs in a web UI user with the code from GitHub's
// callback
func (l *Login) CompleteWebLogin(ctx context.Context, code, redirectURI string) (*Session, error) {
	token, err := l.cfg.GitHub.ExchangeCode(ctx, code, redirectURI)
	if err != nil {
		return nil, err
	}
	return l.signIn(ctx, token, store.SessionWeb)
}

// signIn identifies the user of a GitHub token, checks they may sign in and
// creates a smithd session
func (l *Login) signIn(ctx context.Context, githubToken, kind string) (*Session, error) {
	defer l.revoke(githubToken)

	ghUser, err := l.cfg.GitHub.User(ctx, githubToken)
	if err != nil {
		return nil, err
	}

	allowed, err := l.allowed(ctx, githubToken, ghUser.Login)
	if err != nil {
		return nil, err
	}
	if !allowed {
		log.Printf("auth: GitHub user %s may not sign in", ghUser.Login)
		return nil, fmt.Errorf("%w: %s", ErrNotAllowed, ghUser.Login)
	}

	user, err := l.users.Upsert(ghUser.ID, ghUser.Login, ghUser.Name)
	if err != nil {
		return nil, err
	}

	// Sweep expired sessions now and then, without a background job
	if err := l.sessions.DeleteExpired(l.now()); err != nil {
		log.Printf("auth: %v", err)
	}

	token, err := newToken()
	if err != nil {
		return nil, err
	}
	expiresAt := l.now().Add(l.cfg.SessionTTL).UTC().Truncate(time.Second)
	if err := l.sessions.Create(hashToken(token), user.ID, kind, expiresAt); err != nil {
		return nil, err
	}

	log.Printf("auth: %s signed in (%s)", user.Login, kind)
	return &Session{Token: token, Login: user.Login, ExpiresAt: expiresAt}, nil
}

// allowed reports whether the GitHub user may sign in: listed in
// AllowedUsers, or a member of AllowedOrg (and AllowedTeam)
func (l *Login) allowed(ctx context.Context, githubToken, login string) (bool, error) {
	for _, allowed := range l.cfg.AllowedUsers {
		if strings.EqualFold(strings.TrimSpace(allowed), login) {
			return true, nil
		}
	}

	if l.cfg.AllowedOrg == "" {
		return false, nil
	}
	if l.cfg.AllowedTeam != "" {
		return l.cfg.GitHub.IsTeamMember(ctx, githubToken, l.cfg.AllowedOrg, l.cfg.AllowedTeam, login)
	}
	return l.cfg.GitHub.IsOrgMember(ctx, githubToken, l.cfg.AllowedOrg)
}

// revoke revokes the GitHub token; failing to is not fatal, it expires on
// its own
func (l *Login) revoke(githubToken string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.cfg.GitHub.RevokeToken(ctx, githubToken); err != nil {
		log.Printf("auth: failed to revoke GitHub token: %v", err)
	}
}

// Authenticate returns the identity of a session token
func (l *Login) Authenticate(token string) (Identity, error) {
	if !strings.HasPrefix(token, tokenPrefix) {
		return Identity{}, ErrInvalidSession
	}

	session, err := l.sessions.GetByTokenHash(hashToken(token))
	if errors.Is(err, store.ErrSessionNotFound) {
		return Identity{}, ErrInvalidSession
	}
	if err != nil {
		return Identity{}, err
	}
	if !l.now().Before(session.ExpiresAt) {
		return Identity{}, ErrSessionExpired
	}

	expiresAt := session.ExpiresAt
	return Identity{Kind: KindUser, Login: session.Login, ExpiresAt: &expiresAt}, nil
}

// Logout ends the session of the token
func (l *Login) Logout(token string) error {
	return l.sessions.DeleteByTokenHash(hashToken(token))
}

// newToken returns a random session token
func newToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate session token: %w", err)
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// hashToken is how a session token is stored: a leaked database doesn't
// leak usable tokens
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewState returns a random value for the web flow's state parameter
func NewState() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
