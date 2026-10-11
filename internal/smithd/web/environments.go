package web

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/service"
)

type environmentsPage struct {
	Environments []*models.Environment
	Error        string // a failed change to an existing environment
	Form         models.CreateEnvironmentRequest
	FormError    string
}

func (ui *UI) handleEnvironments(w http.ResponseWriter, r *http.Request) {
	ui.renderEnvironments(w, r, http.StatusOK, environmentsPage{})
}

func (ui *UI) renderEnvironments(w http.ResponseWriter, r *http.Request, status int, data environmentsPage) {
	environments, err := ui.svc.ListEnvironments()
	if err != nil {
		ui.renderServiceError(w, r, err)
		return
	}
	data.Environments = environments

	// Keep the page's own URL rather than the form action's
	w.Header().Set("HX-Push-Url", Prefix+"/environments")
	ui.render(w, r, status, "environments", page{Title: "Environments", Section: "environments", Data: data})
}

// optional returns nil for an empty string, so an empty description is
// stored as no description
func optional(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func (ui *UI) handleCreateEnvironment(w http.ResponseWriter, r *http.Request) {
	req := models.CreateEnvironmentRequest{
		Name:        r.FormValue("name"),
		Description: optional(r.FormValue("description")),
	}

	if _, err := ui.svc.CreateEnvironment(req); err != nil {
		if svcErr, ok := service.AsError(err); ok {
			w.Header().Set("HX-Reswap", "innerHTML show:#add-environment:top")
			ui.renderEnvironments(w, r, http.StatusUnprocessableEntity, environmentsPage{Form: req, FormError: svcErr.Message})
			return
		}
		ui.renderServiceError(w, r, err)
		return
	}

	redirect(w, r, Prefix+"/environments")
}

func (ui *UI) handleUpdateEnvironment(w http.ResponseWriter, r *http.Request) {
	description := strings.TrimSpace(r.FormValue("description"))
	if _, err := ui.svc.UpdateEnvironment(chi.URLParam(r, "environmentId"), models.UpdateEnvironmentRequest{Description: &description}); err != nil {
		ui.renderEnvironmentError(w, r, err)
		return
	}

	redirect(w, r, Prefix+"/environments")
}

func (ui *UI) handleDeleteEnvironment(w http.ResponseWriter, r *http.Request) {
	if err := ui.svc.DeleteEnvironment(chi.URLParam(r, "environmentId")); err != nil {
		ui.renderEnvironmentError(w, r, err)
		return
	}

	redirect(w, r, Prefix+"/environments")
}

// renderEnvironmentError shows a rejected change above the environments list
func (ui *UI) renderEnvironmentError(w http.ResponseWriter, r *http.Request, err error) {
	if svcErr, ok := service.AsError(err); ok {
		status := http.StatusConflict
		if svcErr.Kind == service.KindNotFound {
			status = http.StatusNotFound
		}
		ui.renderEnvironments(w, r, status, environmentsPage{Error: svcErr.Message})
		return
	}
	ui.renderServiceError(w, r, err)
}
