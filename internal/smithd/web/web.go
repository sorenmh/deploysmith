// Package web serves smithd's browser UI: server-rendered html/template
// pages enhanced with htmx, embedded in the binary and mounted at /ui.
//
// Handlers stay thin and call the service layer, which the JSON API uses
// too, so the UI never gains capabilities the API lacks.
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"path"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithd/service"
)

//go:embed templates static
var assets embed.FS

// Prefix is the path the UI is mounted at
const Prefix = "/ui"

// UI serves the web interface
type UI struct {
	svc       *service.Service
	decisions decider
	auth      AuthConfig
	pages     map[string]*template.Template
}

// page is the data passed to every page template
type page struct {
	Title   string
	Section string // highlighted navigation entry
	User    string
	Logout  bool   // the user can sign out (signed in with GitHub)
	PushKey string // VAPID public key; empty if push is not configured
	Data    any
}

// New returns the UI handler, to be mounted at Prefix
func New(svc *service.Service, authCfg AuthConfig) (http.Handler, error) {
	return newHandler(svc, svc, authCfg)
}

// newHandler is New with the plans approved and rejected through decisions
func newHandler(svc *service.Service, decisions decider, authCfg AuthConfig) (http.Handler, error) {
	authenticate, err := Authenticate(authCfg)
	if err != nil {
		return nil, err
	}

	pages, err := parsePages()
	if err != nil {
		return nil, err
	}

	ui := &UI{svc: svc, decisions: decisions, auth: authCfg, pages: pages}

	static, err := fs.Sub(assets, "static")
	if err != nil {
		return nil, err
	}

	r := chi.NewRouter()

	// Static assets are public (home screen icons and the manifest are
	// fetched without cookies). URLs from asset() are cache-busted, so they
	// can be cached for a long time; the manifest refers to its icons by
	// plain URL, so those must be revalidated.
	r.Handle("/static/*", http.StripPrefix(Prefix+"/static/", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Has("v") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		http.FileServerFS(static).ServeHTTP(w, req)
	})))

	// The service worker must be served from the UI's root to control all of
	// it; it must always be revalidated so updates are picked up
	r.Get("/sw.js", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		http.ServeFileFS(w, req, static, "sw.js")
	})

	// Signing in with GitHub, before there is a session
	if authCfg.Login != nil {
		r.Get("/auth/login", ui.handleLogin)
		r.Get("/auth/github", ui.handleLoginGitHub)
		r.Get("/auth/callback", ui.handleCallback)
	}

	r.Group(func(r chi.Router) {
		r.Use(authenticate)
		if authCfg.Login != nil {
			r.Post("/auth/logout", ui.handleLogout)
		}
		r.Post("/push/subscribe", ui.handlePushSubscribe)
		r.Post("/push/unsubscribe", ui.handlePushUnsubscribe)
		r.Get("/", ui.handleHome)
		r.Get("/apps", ui.handleApps)
		r.Get("/apps/{appId}", ui.handleApp)
		r.Post("/apps/{appId}/versions/{versionId}/deploy", ui.handleDeploy)
		r.Post("/apps/{appId}/policies", ui.handleCreatePolicy)
		r.Post("/apps/{appId}/policies/{policyId}/enabled", ui.handleSetPolicyEnabled)
		r.Post("/apps/{appId}/policies/{policyId}/delete", ui.handleDeletePolicy)
		r.Get("/deployments", ui.handleDeployments)
		r.Get("/environments", ui.handleEnvironments)
		r.Post("/environments", ui.handleCreateEnvironment)
		r.Post("/environments/{environmentId}", ui.handleUpdateEnvironment)
		r.Post("/environments/{environmentId}/delete", ui.handleDeleteEnvironment)
		r.Get("/deployments/{deploymentId}", ui.handleDeployment)
		r.Post("/deployments/{deploymentId}/approve", ui.handleApprove)
		r.Post("/deployments/{deploymentId}/reject", ui.handleReject)
	})

	return r, nil
}

// parsePages parses each page template together with the layout, so every
// page can define its own "content" block
func parsePages() (map[string]*template.Template, error) {
	funcs, err := templateFuncs()
	if err != nil {
		return nil, err
	}

	files, err := fs.Glob(assets, "templates/pages/*.html")
	if err != nil {
		return nil, err
	}

	pages := make(map[string]*template.Template, len(files))
	for _, file := range files {
		name := path.Base(file)
		tmpl, err := template.New(name).Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/partials.html", file)
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", file, err)
		}
		pages[name[:len(name)-len(".html")]] = tmpl
	}
	return pages, nil
}

func templateFuncs() (template.FuncMap, error) {
	// Version static asset URLs by content hash, so they can be cached forever
	versions := map[string]string{}
	err := fs.WalkDir(assets, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := assets.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		versions[path.Base(p)] = hex.EncodeToString(sum[:4])
		return nil
	})
	if err != nil {
		return nil, err
	}

	return template.FuncMap{
		"ago":      ago,
		"shortSHA": shortSHA,
		"counts":   counts,
		"actions":  actions,
		"asset": func(name string) (string, error) {
			version, ok := versions[name]
			if !ok {
				return "", fmt.Errorf("unknown asset %q", name)
			}
			return Prefix + "/static/" + name + "?v=" + version, nil
		},
	}, nil
}

// render executes a page into a buffer first, so a template error results
// in an error page instead of a half-written response
func (ui *UI) render(w http.ResponseWriter, r *http.Request, status int, name string, p page) {
	tmpl, ok := ui.pages[name]
	if !ok {
		log.Printf("web: unknown page %q", name)
		http.Error(w, "Page not found", http.StatusInternalServerError)
		return
	}

	p.User = UserFromContext(r.Context())
	p.Logout = ui.auth.Login != nil && p.User != ""
	p.PushKey = ui.svc.PushPublicKey()

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "layout", p); err != nil {
		log.Printf("web: failed to render %s: %v", name, err)
		if name != "error" {
			ui.renderError(w, r, http.StatusInternalServerError, "This page could not be displayed. Check the smithd logs for details.")
			return
		}
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

// renderError shows an error page with a message for the user
func (ui *UI) renderError(w http.ResponseWriter, r *http.Request, status int, message string) {
	ui.render(w, r, status, "error", page{Title: http.StatusText(status), Data: message})
}

// renderServiceError shows the right error page for a service error
func (ui *UI) renderServiceError(w http.ResponseWriter, r *http.Request, err error) {
	if svcErr, ok := service.AsError(err); ok {
		status := http.StatusBadRequest
		switch svcErr.Kind {
		case service.KindNotFound:
			status = http.StatusNotFound
		case service.KindConflict:
			status = http.StatusConflict
		}
		ui.renderError(w, r, status, svcErr.Message)
		return
	}

	log.Printf("web: %s %s: %v", r.Method, r.URL.Path, err)
	ui.renderError(w, r, http.StatusInternalServerError, "Something went wrong on the server. Check the smithd logs for details.")
}

// redirect sends the browser to url after a form submission. htmx requests
// get a full-page redirect so the address bar and history stay correct.
func redirect(w http.ResponseWriter, r *http.Request, url string) {
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", url)
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, url, http.StatusSeeOther)
}

func (ui *UI) handleHome(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, Prefix+"/apps", http.StatusFound)
}
