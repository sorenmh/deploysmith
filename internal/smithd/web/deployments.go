package web

import (
	"bytes"
	"log"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
)

const deploymentsPerPage = 50

type deploymentsPage struct {
	Filter       store.DeploymentFilter
	Filtered     bool
	Apps         []models.Application
	Environments []*models.Environment
	Deployments  []models.DeploymentDetail
	Plans        map[string]models.TerraformPlan // plan counts by deployment id
	AnyPending   bool
	RefreshURL   string
	PrevPage     string
	NextPage     string
}

func (ui *UI) handleDeployments(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := store.DeploymentFilter{
		AppID:       query.Get("app"),
		Environment: query.Get("environment"),
		Status:      query.Get("status"),
	}
	pageNum, err := strconv.Atoi(query.Get("page"))
	if err != nil || pageNum < 1 {
		pageNum = 1
	}

	deployments, total, err := ui.svc.ListDeployments(filter, deploymentsPerPage, (pageNum-1)*deploymentsPerPage)
	if err != nil {
		ui.renderServiceError(w, r, err)
		return
	}

	data := deploymentsPage{
		Filter:      filter,
		Filtered:    filter != (store.DeploymentFilter{}),
		Deployments: deployments,
	}
	var terraformIDs []string
	for _, d := range deployments {
		if pollsDeployment(d.Status) {
			data.AnyPending = true
		}
		if d.Terraform != nil {
			terraformIDs = append(terraformIDs, d.ID)
		}
	}
	if data.Plans, err = ui.svc.TerraformPlanSummaries(terraformIDs); err != nil {
		ui.renderServiceError(w, r, err)
		return
	}

	pageURL := func(n int, partial bool) string {
		q := url.Values{}
		for key, value := range map[string]string{"app": filter.AppID, "environment": filter.Environment, "status": filter.Status} {
			if value != "" {
				q.Set(key, value)
			}
		}
		if n > 1 {
			q.Set("page", strconv.Itoa(n))
		}
		if partial {
			q.Set("partial", "1")
		}
		if len(q) == 0 {
			return Prefix + "/deployments"
		}
		return Prefix + "/deployments?" + q.Encode()
	}
	if pageNum > 1 {
		data.PrevPage = pageURL(pageNum-1, false)
	}
	if pageNum*deploymentsPerPage < total {
		data.NextPage = pageURL(pageNum+1, false)
	}
	data.RefreshURL = pageURL(pageNum, true)

	if query.Get("partial") != "" {
		ui.renderFragment(w, "deployments", "deployments-table", data)
		return
	}

	if data.Apps, err = ui.svc.ListApps(); err != nil {
		ui.renderServiceError(w, r, err)
		return
	}
	if data.Environments, err = ui.svc.ListEnvironments(); err != nil {
		ui.renderServiceError(w, r, err)
		return
	}

	ui.render(w, r, http.StatusOK, "deployments", page{Title: "Deployments", Section: "deployments", Data: data})
}

func (ui *UI) handleDeployment(w http.ResponseWriter, r *http.Request) {
	deployment, err := ui.svc.GetDeployment(chi.URLParam(r, "deploymentId"))
	if err != nil {
		ui.renderServiceError(w, r, err)
		return
	}

	if r.URL.Query().Get("partial") != "" {
		ui.renderFragment(w, "deployment", "deployment-detail", ui.deploymentPageData(deployment))
		return
	}

	ui.renderDeployment(w, r, http.StatusOK, deployment, "")
}

// renderFragment renders a named template of a page without the layout,
// for htmx partial updates
func (ui *UI) renderFragment(w http.ResponseWriter, pageName, fragment string, data any) {
	tmpl, ok := ui.pages[pageName]
	if !ok {
		http.Error(w, "Unknown page", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, fragment, data); err != nil {
		log.Printf("web: failed to render %s/%s: %v", pageName, fragment, err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}
