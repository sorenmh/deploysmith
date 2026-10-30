package web

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/service"
)

// decider approves and rejects the plans of terraform deployments; the
// service implements it
type decider interface {
	ApproveTerraform(ctx context.Context, deploymentID string, confirmDestroy bool) (*models.DeploymentDetail, error)
	RejectTerraform(ctx context.Context, deploymentID, reason string) (*models.DeploymentDetail, error)
}

// deploymentPage is the data of the deployment page: the deployment, and
// the problem with a decision the user just tried to make
type deploymentPage struct {
	*models.DeploymentDetail
	// SourceURL links the commit the version was built from, if its
	// repository is known
	SourceURL string
	// Error is why an approval or rejection was refused
	Error string
}

// pollsDeployment reports whether a deployment page or listing keeps
// refreshing: while the deployment is moving on its own. A plan awaiting
// approval waits for the user, and refreshing would discard what they typed.
func pollsDeployment(status string) bool {
	switch status {
	case models.DeploymentPending, models.DeploymentPlanning, models.DeploymentApplying:
		return true
	}
	return false
}

// Polls reports whether the page should keep refreshing
func (p deploymentPage) Polls() bool { return pollsDeployment(p.Status) }

// CanDecide reports whether the plan can be approved or rejected: only while
// it is awaiting approval
func (p deploymentPage) CanDecide() bool {
	return p.Terraform != nil && p.Status == models.DeploymentAwaitingApproval
}

// NeedsConfirmation reports whether approving needs the environment name
// typed, because the plan destroys or replaces resources
func (p deploymentPage) NeedsConfirmation() bool {
	return p.CanDecide() && p.Terraform.Plan != nil && p.Terraform.Plan.Destroys
}

// deploymentPageData loads what the page of a deployment shows besides the
// deployment itself
func (ui *UI) deploymentPageData(deployment *models.DeploymentDetail) deploymentPage {
	data := deploymentPage{DeploymentDetail: deployment}
	if deployment.Terraform == nil {
		return data
	}
	if version, err := ui.svc.Versions.GetByVersionID(deployment.AppID, deployment.VersionID); err == nil {
		if source, err := ui.svc.Versions.GetSource(version.ID); err == nil && source.Repository != "" && source.CommitSHA != "" {
			data.SourceURL = "https://github.com/" + source.Repository + "/commit/" + source.CommitSHA
		}
	}
	return data
}

// renderDeployment shows the deployment page, with the reason a decision
// was refused if there is one
func (ui *UI) renderDeployment(w http.ResponseWriter, r *http.Request, status int, deployment *models.DeploymentDetail, problem string) {
	data := ui.deploymentPageData(deployment)
	data.Error = problem
	title := deployment.AppName + " " + deployment.VersionID + " to " + deployment.Environment
	ui.render(w, r, status, "deployment", page{Title: title, Section: "deployments", Data: data})
}

func (ui *UI) handleApprove(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "deploymentId")
	deployment, err := ui.svc.GetDeployment(id)
	if err != nil {
		ui.renderServiceError(w, r, err)
		return
	}

	// A plan that destroys or replaces resources needs the environment name
	// typed. The API's own confirmation is sent after that.
	confirmDestroy := deploymentPage{DeploymentDetail: deployment}.NeedsConfirmation()
	if confirmDestroy && strings.TrimSpace(r.FormValue("confirm")) != deployment.Environment {
		ui.renderDeployment(w, r, http.StatusUnprocessableEntity, deployment,
			"This plan destroys or replaces resources. Type the environment name ("+deployment.Environment+") to approve it.")
		return
	}

	if _, err := ui.decisions.ApproveTerraform(r.Context(), id, confirmDestroy); err != nil {
		ui.renderDecisionError(w, r, id, err)
		return
	}
	redirect(w, r, Prefix+"/deployments/"+id)
}

func (ui *UI) handleReject(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "deploymentId")

	if _, err := ui.decisions.RejectTerraform(r.Context(), id, r.FormValue("reason")); err != nil {
		ui.renderDecisionError(w, r, id, err)
		return
	}
	redirect(w, r, Prefix+"/deployments/"+id)
}

// renderDecisionError shows the deployment again with why the decision was
// refused. A plan that is not the current one, or a deployment that moved on,
// means the page the user decided on was out of date.
func (ui *UI) renderDecisionError(w http.ResponseWriter, r *http.Request, id string, err error) {
	svcErr, ok := service.AsError(err)
	if !ok || svcErr.Kind == service.KindNotFound {
		ui.renderServiceError(w, r, err)
		return
	}

	deployment, getErr := ui.svc.GetDeployment(id)
	if getErr != nil {
		ui.renderServiceError(w, r, getErr)
		return
	}

	status, message := http.StatusUnprocessableEntity, svcErr.Message
	switch {
	case svcErr.Kind == service.KindConflict && svcErr.Code == "plan_not_current":
		status, message = http.StatusConflict, "The plan changed, so nothing was approved. "+svcErr.Message+" Reload the page and review the plan again."
	case svcErr.Kind == service.KindConflict:
		status, message = http.StatusConflict, svcErr.Message+" Reload the page to see where it is now."
	}
	ui.renderDeployment(w, r, status, deployment, message)
}
