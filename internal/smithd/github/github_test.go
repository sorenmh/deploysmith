package github

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/sorenmh/deploysmith/internal/smithd/github/githubtest"
	"github.com/sorenmh/deploysmith/internal/smithd/slack"
)

const (
	repo = "acme/web"
	sha  = "0123456789abcdef0123456789abcdef01234567"
)

func newTestApp(t *testing.T, gh *githubtest.Server) *App {
	t.Helper()
	app, err := NewApp(githubtest.AppID, gh.PrivateKey, gh.URL)
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestJWT(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	// PKCS#8 works as well as GitHub's PKCS#1
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))

	now := time.Unix(1_800_000_000, 0)
	for _, tt := range []struct{ id, iss string }{
		{"12345", "12345"},                   // app id: a number
		{"Iv23liExample", `"Iv23liExample"`}, // client id: a string
	} {
		app, err := NewApp(tt.id, keyPEM, "https://api.github.com")
		if err != nil {
			t.Fatal(err)
		}
		app.now = func() time.Time { return now }

		jwt, err := app.jwt()
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(jwt, ".")
		if len(parts) != 3 {
			t.Fatalf("not a JWT: %q", jwt)
		}
		signature, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
			t.Errorf("signature does not verify: %v", err)
		}

		var header map[string]string
		var claims map[string]json.RawMessage
		decode := func(part string, v any) {
			data, err := base64.RawURLEncoding.DecodeString(part)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, v); err != nil {
				t.Fatal(err)
			}
		}
		decode(parts[0], &header)
		decode(parts[1], &claims)
		if header["alg"] != "RS256" || header["typ"] != "JWT" {
			t.Errorf("unexpected header %v", header)
		}
		if string(claims["iss"]) != tt.iss {
			t.Errorf("iss = %s, want %s", claims["iss"], tt.iss)
		}
		// Backdated against clock skew, and within GitHub's 10 minutes
		if string(claims["iat"]) != "1799999940" || string(claims["exp"]) != "1800000540" {
			t.Errorf("iat = %s, exp = %s", claims["iat"], claims["exp"])
		}
	}
}

func TestNewAppRejectsBadKeys(t *testing.T) {
	for _, key := range []string{"", "not a key", "-----BEGIN RSA PRIVATE KEY-----\nAAAA\n-----END RSA PRIVATE KEY-----"} {
		if _, err := NewApp("1", key, "https://api.github.com"); err == nil {
			t.Errorf("expected an error for %q", key)
		}
	}
}

func TestInstallationToken(t *testing.T) {
	ctx := context.Background()

	t.Run("cached until near expiry", func(t *testing.T) {
		gh := githubtest.New(t)
		gh.Install(repo)
		app := newTestApp(t, gh)

		first, err := app.Token(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		second, err := app.Token(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		if first != second {
			t.Errorf("token not cached: %q, %q", first, second)
		}
		if n := gh.Requests("GET /repos/{owner}/{repo}/installation"); n != 1 {
			t.Errorf("looked up installation %d times, want 1", n)
		}
		if n := gh.Requests("POST /app/installations/{id}/access_tokens"); n != 1 {
			t.Errorf("created %d tokens, want 1", n)
		}

		// Shortly before expiry a new token is fetched; the installation
		// is still known
		app.mu.Lock()
		app.tokens[repo] = installationToken{token: first, expiresAt: time.Now().Add(tokenRefreshMargin - time.Minute)}
		app.mu.Unlock()
		third, err := app.Token(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		if third == first {
			t.Error("expected a new token near expiry")
		}
		if n := gh.Requests("POST /app/installations/{id}/access_tokens"); n != 2 {
			t.Errorf("created %d tokens, want 2", n)
		}
		if n := gh.Requests("GET /repos/{owner}/{repo}/installation"); n != 1 {
			t.Errorf("looked up installation %d times, want 1", n)
		}
	})

	t.Run("tokens per repository", func(t *testing.T) {
		gh := githubtest.New(t)
		gh.Install(repo)
		gh.Install("acme/api")
		app := newTestApp(t, gh)

		web, err := app.Token(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		api, err := app.Token(ctx, "acme/api")
		if err != nil {
			t.Fatal(err)
		}
		if web == api {
			t.Error("expected a token per repository")
		}
	})

	t.Run("not installed", func(t *testing.T) {
		gh := githubtest.New(t)
		app := newTestApp(t, gh)
		if _, err := app.Token(ctx, repo); !errors.Is(err, errNotInstalled) {
			t.Errorf("err = %v, want errNotInstalled", err)
		}
	})

	t.Run("wrong key", func(t *testing.T) {
		gh := githubtest.New(t)
		gh.Install(repo)
		other := githubtest.New(t)
		app, err := NewApp(githubtest.AppID, other.PrivateKey, gh.URL)
		if err != nil {
			t.Fatal(err)
		}
		var apiErr *APIError
		if _, err := app.Token(ctx, repo); !errors.As(err, &apiErr) || apiErr.Status != 401 {
			t.Errorf("err = %v, want a 401", err)
		}
	})
}

func TestCheckRuns(t *testing.T) {
	ctx := context.Background()
	gh := githubtest.New(t)
	gh.Install(repo)
	app := newTestApp(t, gh)

	run := CheckRun{Repo: repo, SHA: sha, Name: "deploysmith/deploy-production", ExternalID: "d1", Status: StatusQueued, Title: "Deploying", Summary: "Requested."}
	id, err := app.CreateCheckRun(ctx, run)
	if err != nil {
		t.Fatal(err)
	}

	found, err := app.FindCheckRun(ctx, run)
	if err != nil || found != id {
		t.Errorf("FindCheckRun = %d, %v, want %d", found, err, id)
	}
	other := run
	other.ExternalID = "d2"
	if found, err := app.FindCheckRun(ctx, other); err != nil || found != 0 {
		t.Errorf("FindCheckRun for another deployment = %d, %v, want 0", found, err)
	}

	run.Status, run.Conclusion, run.Title = StatusCompleted, ConclusionSuccess, "Deployed"
	run.DetailsURL = "https://smithd.example.com/ui/deployments/d1"
	if err := app.UpdateCheckRun(ctx, id, run); err != nil {
		t.Fatal(err)
	}

	runs := gh.CheckRuns()
	if len(runs) != 1 {
		t.Fatalf("got %d check runs, want 1", len(runs))
	}
	got := runs[0]
	if got.HeadSHA != sha || got.Status != "completed" || got.Conclusion != "success" || got.Title != "Deployed" || got.DetailsURL != run.DetailsURL {
		t.Errorf("unexpected check run %+v", got)
	}
	if strings.Join(got.History, ",") != "queued,completed:success" {
		t.Errorf("history = %v", got.History)
	}
	// One token served all calls
	if n := gh.Requests("POST /app/installations/{id}/access_tokens"); n != 1 {
		t.Errorf("created %d tokens, want 1", n)
	}
}

func TestTruncate(t *testing.T) {
	long := strings.Repeat("é", maxOutput)
	got := truncate(long)
	if len(got) > maxOutput || !strings.HasSuffix(got, "(truncated)") || !strings.HasPrefix(got, "éé") {
		t.Errorf("unexpected truncation to %d bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Error("truncation split a character")
	}
	if short := "## Plan"; truncate(short) != short {
		t.Error("short output changed")
	}
}

func deploy() slack.Deploy {
	return slack.Deploy{
		App:          "hello",
		Version:      "v1",
		Environment:  "production",
		TriggeredBy:  "sorenmh",
		URL:          "https://smithd.example.com/ui/deployments/d1",
		DeploymentID: "d1",
		SourceRepo:   repo,
		SourceSHA:    sha,
	}
}

func TestReporterDeployment(t *testing.T) {
	ctx := context.Background()

	t.Run("requested, committed and applied", func(t *testing.T) {
		gh := githubtest.New(t)
		gh.Install(repo)
		r := newReporter(newTestApp(t, gh))

		d := deploy()
		r.Requested(ctx, d)
		d.CommitSHA = "fedcba9876543210"
		r.Committed(ctx, d, true)
		r.Applied(ctx, d, 42*time.Second)

		runs := gh.CheckRuns()
		if len(runs) != 1 {
			t.Fatalf("got %d check runs, want 1: %+v", len(runs), runs)
		}
		run := runs[0]
		if run.Name != "deploysmith/deploy-production" || run.HeadSHA != sha || run.ExternalID != "d1" || run.DetailsURL != d.URL {
			t.Errorf("unexpected check run %+v", run)
		}
		if strings.Join(run.History, ",") != "queued,in_progress,completed:success" {
			t.Errorf("history = %v", run.History)
		}
		if run.Title != "Deployed v1 to production" {
			t.Errorf("title = %q", run.Title)
		}
		for _, part := range []string{"Applied by Flux in 42s", "`hello`", "`v1`", "`production`", "sorenmh", "`fedcba9`", "[View deployment](" + d.URL + ")"} {
			if !strings.Contains(run.Summary, part) {
				t.Errorf("summary %q does not contain %q", run.Summary, part)
			}
		}
		// The run is known, so it is not looked up
		if n := gh.Requests("GET /repos/{owner}/{repo}/commits/{sha}/check-runs"); n != 1 {
			t.Errorf("listed check runs %d times, want 1 (before creating)", n)
		}
	})

	t.Run("failed", func(t *testing.T) {
		gh := githubtest.New(t)
		gh.Install(repo)
		r := newReporter(newTestApp(t, gh))

		d := deploy()
		d.Policy = "main-to-prod"
		r.Requested(ctx, d)
		r.Failed(ctx, d, "gitops", "push rejected")

		run := gh.CheckRuns()[0]
		if strings.Join(run.History, ",") != "queued,completed:failure" {
			t.Errorf("history = %v", run.History)
		}
		if run.Title != "Deployment to production failed (gitops)" || !strings.Contains(run.Text, "push rejected") || !strings.Contains(run.Summary, "policy `main-to-prod`") {
			t.Errorf("unexpected check run %+v", run)
		}
	})

	t.Run("without Flux, committing completes it", func(t *testing.T) {
		gh := githubtest.New(t)
		gh.Install(repo)
		r := newReporter(newTestApp(t, gh))

		d := deploy()
		r.Requested(ctx, d)
		r.Committed(ctx, d, false)
		if run := gh.CheckRuns()[0]; strings.Join(run.History, ",") != "queued,completed:success" {
			t.Errorf("history = %v", run.History)
		}
	})

	t.Run("after a restart the run is found again", func(t *testing.T) {
		gh := githubtest.New(t)
		gh.Install(repo)
		d := deploy()
		newReporter(newTestApp(t, gh)).Requested(ctx, d)
		newReporter(newTestApp(t, gh)).Applied(ctx, d, time.Second)

		runs := gh.CheckRuns()
		if len(runs) != 1 || strings.Join(runs[0].History, ",") != "queued,completed:success" {
			t.Errorf("unexpected check runs %+v", runs)
		}
	})

	t.Run("each deployment gets its own run", func(t *testing.T) {
		gh := githubtest.New(t)
		gh.Install(repo)
		r := newReporter(newTestApp(t, gh))
		first, second := deploy(), deploy()
		second.DeploymentID = "d2"
		r.Requested(ctx, first)
		r.Requested(ctx, second)
		if runs := gh.CheckRuns(); len(runs) != 2 {
			t.Errorf("got %d check runs, want 2", len(runs))
		}
	})

	t.Run("nothing without a source, a nil reporter or an installation", func(t *testing.T) {
		gh := githubtest.New(t)
		r := newReporter(newTestApp(t, gh))

		d := deploy()
		d.SourceSHA = ""
		r.Requested(ctx, d)
		if n := gh.Requests("GET /repos/{owner}/{repo}/installation"); n != 0 {
			t.Errorf("called GitHub without a source")
		}

		var none *Reporter
		none.Requested(ctx, deploy())
		none.Committed(ctx, deploy(), true)
		none.Applied(ctx, deploy(), time.Second)
		none.Failed(ctx, deploy(), "gitops", "boom")
		none.ReportPlan(ctx, Plan{Repo: repo, SHA: sha})

		// Not installed: logged, nothing created
		r.Requested(ctx, deploy())
		if runs := gh.CheckRuns(); len(runs) != 0 {
			t.Errorf("got %d check runs, want 0", len(runs))
		}
	})
}

func TestNewReporterDisabled(t *testing.T) {
	r, err := NewReporter("", "", "https://api.github.com")
	if r != nil || err != nil {
		t.Errorf("NewReporter = %v, %v, want nil, nil", r, err)
	}
	if _, err := NewReporter("1", "not a key", "https://api.github.com"); err == nil {
		t.Error("expected an error for a bad key")
	}
}

func TestReportPlan(t *testing.T) {
	ctx := context.Background()
	gh := githubtest.New(t)
	gh.Install(repo)
	r := newReporter(newTestApp(t, gh))

	plan := Plan{Repo: repo, SHA: sha, Environment: "production", RunID: "p1", Status: StatusInProgress, Title: "Planning", Summary: "Running `tofu plan`."}
	r.ReportPlan(ctx, plan)
	plan.Status, plan.Conclusion = StatusCompleted, ConclusionNeutral
	plan.Title = "1 to add, 0 to change, 1 to destroy"
	plan.Summary = "**1 to destroy**: `aws_s3_bucket.old`"
	plan.URL = "https://smithd.example.com/ui/deployments/p1"
	r.ReportPlan(ctx, plan)

	runs := gh.CheckRuns()
	if len(runs) != 1 {
		t.Fatalf("got %d check runs, want 1", len(runs))
	}
	run := runs[0]
	if run.Name != "deploysmith/plan-production" || run.ExternalID != "p1" || run.Title != plan.Title || run.Summary != plan.Summary || run.DetailsURL != plan.URL {
		t.Errorf("unexpected check run %+v", run)
	}
	if strings.Join(run.History, ",") != "in_progress,completed:neutral" {
		t.Errorf("history = %v", run.History)
	}
}
