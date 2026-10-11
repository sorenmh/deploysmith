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
	Detail     *service.AppDetail
	Notice     *models.DeploymentDetail // outcome of a deploy just made from this page
	PolicyForm policyForm
	PrevPage   int
	NextPage   int
}

// policyForm holds the new-policy form's values and validation error, so a
// rejected submission is shown again with what the user entered
type policyForm struct {
	Name              string
	GitBranchPattern  string
	TargetEnvironment string
	Error             string
}

func (ui *UI) handleApp(w http.ResponseWriter, r *http.Request) {
	ui.renderApp(w, r, http.StatusOK, policyForm{})
}

func (ui *UI) renderApp(w http.ResponseWriter, r *http.Request, status int, form policyForm) {
	pageNum, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || pageNum < 1 {
		pageNum = 1
	}

	detail, err := ui.svc.GetAppDetail(chi.URLParam(r, "appId"), versionsPerPage, (pageNum-1)*versionsPerPage)
	if err != nil {
		ui.renderServiceError(w, r, err)
		return
	}

	data := appPage{Detail: detail, PolicyForm: form}
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

	ui.render(w, r, status, "app", page{Title: detail.App.Name, Section: "apps", Data: data})
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

func (ui *UI) handleCreatePolicy(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	form := policyForm{
		Name:              r.FormValue("name"),
		GitBranchPattern:  r.FormValue("gitBranchPattern"),
		TargetEnvironment: r.FormValue("targetEnvironment"),
	}

	_, err := ui.svc.CreatePolicy(appID, models.CreatePolicyRequest{
		Name:              form.Name,
		GitBranchPattern:  form.GitBranchPattern,
		TargetEnvironment: form.TargetEnvironment,
	})
	if svcErr, ok := service.AsError(err); ok && svcErr.Kind != service.KindNotFound {
		// Show the form again with the problem and the entered values. Keep
		// the app page's URL (not the form's) and scroll to the form.
		form.Error = svcErr.Message
		w.Header().Set("HX-Push-Url", Prefix+"/apps/"+appID)
		w.Header().Set("HX-Reswap", "innerHTML show:#add-policy:top")
		ui.renderApp(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	if err != nil {
		ui.renderServiceError(w, r, err)
		return
	}

	redirect(w, r, Prefix+"/apps/"+appID+"#policies")
}

func (ui *UI) handleSetPolicyEnabled(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")
	enabled := r.FormValue("enabled") == "true"

	if _, err := ui.svc.UpdatePolicy(appID, chi.URLParam(r, "policyId"), models.UpdatePolicyRequest{Enabled: &enabled}); err != nil {
		ui.renderServiceError(w, r, err)
		return
	}

	redirect(w, r, Prefix+"/apps/"+appID+"#policies")
}

func (ui *UI) handleDeletePolicy(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appId")

	if err := ui.svc.DeletePolicy(appID, chi.URLParam(r, "policyId")); err != nil {
		ui.renderServiceError(w, r, err)
		return
	}

	redirect(w, r, Prefix+"/apps/"+appID+"#policies")
}
