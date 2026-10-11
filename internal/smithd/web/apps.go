package web

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/service"
)

const versionsPerPage = 25

func (ui *UI) handleApps(w http.ResponseWriter, r *http.Request) {
	overview, err := ui.svc.Overview()
	if err != nil {
		ui.renderServiceError(w, r, err)
		return
	}

	ui.render(w, r, http.StatusOK, "apps", page{Title: "Apps", Section: "apps", Data: overview})
}

type appPage struct {
	Detail   *service.AppDetail
	Notice   *models.DeploymentDetail // outcome of a deploy just made from this page
	PrevPage int
	NextPage int
}

func (ui *UI) handleApp(w http.ResponseWriter, r *http.Request) {
	pageNum, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || pageNum < 1 {
		pageNum = 1
	}

	detail, err := ui.svc.GetAppDetail(chi.URLParam(r, "appId"), versionsPerPage, (pageNum-1)*versionsPerPage)
	if err != nil {
		ui.renderServiceError(w, r, err)
		return
	}

	data := appPage{Detail: detail}
	if pageNum > 1 {
		data.PrevPage = pageNum - 1
	}
	if pageNum*versionsPerPage < detail.TotalVersions {
		data.NextPage = pageNum + 1
	}

	// Show the outcome of a deployment of this app just made from this page
	if id := r.URL.Query().Get("deployment"); id != "" {
		if deployment, err := ui.svc.GetDeployment(id); err == nil && deployment.AppID == detail.App.ID {
			data.Notice = deployment
		}
	}

	ui.render(w, r, http.StatusOK, "app", page{Title: detail.App.Name, Section: "apps", Data: data})
}

func (ui *UI) handleDeploy(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	user := UserFromContext(r.Context())

	deployment, err := ui.svc.DeployVersion(r.Context(), appID, chi.URLParam(r, "versionId"), r.FormValue("environment"), user)
	if deployment == nil {
		// The deployment was never started, e.g. an unknown environment
		ui.renderServiceError(w, r, err)
		return
	}

	// Success or failure, the app page reports the deployment's outcome
	redirect(w, r, Prefix+"/apps/"+appID+"?deployment="+deployment.ID)
}
