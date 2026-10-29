// Package githubtest is a fake of the parts of the GitHub API that smithd
// reports to as a GitHub App: installations, installation tokens and check
// runs. It checks the app's JWTs and tokens like GitHub does.
package githubtest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// AppID is the fake app's id
const AppID = "12345"

// CheckRun is a check run as the fake stores it
type CheckRun struct {
	ID         int64
	Repo       string
	Name       string
	HeadSHA    string
	ExternalID string
	Status     string
	Conclusion string
	Title      string
	Summary    string
	Text       string
	DetailsURL string
	// History has the status (and conclusion) of each create and update
	History []string
}

type token struct {
	repo      string
	expiresAt time.Time
}

// Server is a fake GitHub API
type Server struct {
	URL string
	// PrivateKey is the app's private key (PKCS#1 PEM)
	PrivateKey string
	// TokenTTL is how long installation tokens are valid (an hour on GitHub)
	TokenTTL time.Duration

	public *rsa.PublicKey

	mu            sync.Mutex
	installations map[string]int64 // by repository
	tokens        map[string]token
	checkRuns     []*CheckRun
	requests      map[string]int // by "METHOD pattern"
}

// New starts a fake GitHub API, closed when the test ends
func New(t testing.TB) *Server {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		PrivateKey:    string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
		TokenTTL:      time.Hour,
		public:        &key.PublicKey,
		installations: map[string]int64{},
		tokens:        map[string]token{},
		requests:      map[string]int{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/{owner}/{repo}/installation", s.withJWT(s.handleInstallation))
	mux.HandleFunc("POST /app/installations/{id}/access_tokens", s.withJWT(s.handleAccessToken))
	mux.HandleFunc("POST /repos/{owner}/{repo}/check-runs", s.withToken(s.handleCreate))
	mux.HandleFunc("PATCH /repos/{owner}/{repo}/check-runs/{id}", s.withToken(s.handleUpdate))
	mux.HandleFunc("GET /repos/{owner}/{repo}/commits/{sha}/check-runs", s.withToken(s.handleList))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	s.URL = srv.URL
	return s
}

// Install installs the app on a repository (owner/name)
func (s *Server) Install(repo string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.installations[repo] = int64(1000 + len(s.installations))
}

// CheckRuns returns the check runs, oldest first
func (s *Server) CheckRuns() []CheckRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := make([]CheckRun, len(s.checkRuns))
	for i, run := range s.checkRuns {
		runs[i] = *run
		runs[i].History = append([]string(nil), run.History...)
	}
	return runs
}

// Requests returns how often an endpoint was called, by method and path
// pattern, e.g. "POST /app/installations/{id}/access_tokens"
func (s *Server) Requests(endpoint string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[endpoint]
}

func (s *Server) count(r *http.Request) {
	s.mu.Lock()
	s.requests[r.Pattern]++
	s.mu.Unlock()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"message": message})
}

// withJWT requires a valid app JWT, as the app endpoints do
func (s *Server) withJWT(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.count(r)
		if err := s.verifyJWT(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")); err != nil {
			fail(w, http.StatusUnauthorized, err.Error())
			return
		}
		next(w, r)
	}
}

func (s *Server) verifyJWT(jwt string) error {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return fmt.Errorf("not a JWT")
	}
	var header struct {
		Alg string `json:"alg"`
	}
	var claims struct {
		Iat int64           `json:"iat"`
		Exp int64           `json:"exp"`
		Iss json.RawMessage `json:"iss"`
	}
	if err := decodePart(parts[0], &header); err != nil || header.Alg != "RS256" {
		return fmt.Errorf("bad JWT header")
	}
	if err := decodePart(parts[1], &claims); err != nil {
		return fmt.Errorf("bad JWT claims")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("bad JWT signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(s.public, crypto.SHA256, digest[:], signature); err != nil {
		return fmt.Errorf("JWT signature does not verify")
	}
	now := time.Now().Unix()
	if string(claims.Iss) != AppID && string(claims.Iss) != strconv.Quote(AppID) {
		return fmt.Errorf("JWT issuer %s is not the app", claims.Iss)
	}
	if claims.Iat > now || claims.Exp <= now || claims.Exp-claims.Iat > 600 {
		return fmt.Errorf("JWT expired or valid for too long")
	}
	return nil
}

func decodePart(part string, v any) error {
	data, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// withToken requires an unexpired installation token for the repository
func (s *Server) withToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.count(r)
		repo := r.PathValue("owner") + "/" + r.PathValue("repo")
		s.mu.Lock()
		tok, ok := s.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
		s.mu.Unlock()
		switch {
		case !ok || time.Now().After(tok.expiresAt):
			fail(w, http.StatusUnauthorized, "Bad credentials")
		case tok.repo != repo:
			fail(w, http.StatusNotFound, "Not Found")
		default:
			next(w, r)
		}
	}
}

func (s *Server) handleInstallation(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	id, ok := s.installations[r.PathValue("owner")+"/"+r.PathValue("repo")]
	s.mu.Unlock()
	if !ok {
		fail(w, http.StatusNotFound, "Not Found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) handleAccessToken(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var req struct {
		Repositories []string `json:"repositories"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Repositories) != 1 {
		fail(w, http.StatusUnprocessableEntity, "expected one repository")
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	repo := ""
	for name, installation := range s.installations {
		if installation == id && strings.HasSuffix(name, "/"+req.Repositories[0]) {
			repo = name
		}
	}
	if repo == "" {
		fail(w, http.StatusNotFound, "Not Found")
		return
	}
	value := fmt.Sprintf("ghs_%d_%d", id, len(s.tokens))
	expiresAt := time.Now().Add(s.TokenTTL).UTC().Truncate(time.Second)
	s.tokens[value] = token{repo: repo, expiresAt: expiresAt}
	writeJSON(w, http.StatusCreated, map[string]any{"token": value, "expires_at": expiresAt.Format(time.RFC3339)})
}

type checkRunRequest struct {
	Name       string `json:"name"`
	HeadSHA    string `json:"head_sha"`
	ExternalID string `json:"external_id"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	DetailsURL string `json:"details_url"`
	Output     struct {
		Title   string `json:"title"`
		Summary string `json:"summary"`
		Text    string `json:"text"`
	} `json:"output"`
}

func (req checkRunRequest) validate() string {
	switch {
	case req.Status != "queued" && req.Status != "in_progress" && req.Status != "completed":
		return "invalid status"
	case (req.Status == "completed") != (req.Conclusion != ""):
		return "conclusion is required with, and only with, status completed"
	case req.Output.Title == "" || req.Output.Summary == "":
		return "output needs a title and a summary"
	}
	return ""
}

func (run *CheckRun) apply(req checkRunRequest) {
	run.ExternalID = req.ExternalID
	run.Status = req.Status
	run.Conclusion = req.Conclusion
	run.Title = req.Output.Title
	run.Summary = req.Output.Summary
	run.Text = req.Output.Text
	run.DetailsURL = req.DetailsURL
	state := req.Status
	if req.Conclusion != "" {
		state += ":" + req.Conclusion
	}
	run.History = append(run.History, state)
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req checkRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "Problems parsing JSON")
		return
	}
	if msg := req.validate(); msg != "" || req.Name == "" || len(req.HeadSHA) != 40 {
		fail(w, http.StatusUnprocessableEntity, "Validation Failed "+msg)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	run := &CheckRun{
		ID:      int64(len(s.checkRuns) + 1),
		Repo:    r.PathValue("owner") + "/" + r.PathValue("repo"),
		Name:    req.Name,
		HeadSHA: req.HeadSHA,
	}
	run.apply(req)
	s.checkRuns = append(s.checkRuns, run)
	writeJSON(w, http.StatusCreated, map[string]any{"id": run.ID})
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	var req checkRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(w, http.StatusBadRequest, "Problems parsing JSON")
		return
	}
	if msg := req.validate(); msg != "" {
		fail(w, http.StatusUnprocessableEntity, "Validation Failed "+msg)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	repo := r.PathValue("owner") + "/" + r.PathValue("repo")

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.checkRuns {
		if run.ID == id && run.Repo == repo {
			run.apply(req)
			writeJSON(w, http.StatusOK, map[string]any{"id": run.ID})
			return
		}
	}
	fail(w, http.StatusNotFound, "Not Found")
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	repo := r.PathValue("owner") + "/" + r.PathValue("repo")
	name := r.URL.Query().Get("check_name")

	s.mu.Lock()
	defer s.mu.Unlock()
	runs := []map[string]any{}
	for _, run := range s.checkRuns {
		if run.Repo == repo && run.HeadSHA == r.PathValue("sha") && (name == "" || run.Name == name) {
			runs = append(runs, map[string]any{"id": run.ID, "name": run.Name, "external_id": run.ExternalID})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"total_count": len(runs), "check_runs": runs})
}
