package service

import (
	"context"
	"fmt"
	"time"

	"github.com/sorenmh/deploysmith/internal/shared/logging"
	"github.com/sorenmh/deploysmith/internal/shared/tfbundle"
	"github.com/sorenmh/deploysmith/internal/smithd/gitops"
	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/slack"
	"github.com/sorenmh/deploysmith/internal/smithd/terraform"
)

const (
	// planSweepInterval is how often smithd looks for plan-only runs left on
	// the plans branch although no run of its records needs them, e.g. after
	// a crash between a push and recording it; and squashes the branch's
	// history and deletes old results
	planSweepInterval = time.Hour

	// maxPlansBranchCommits is how long the history of the plans branch may
	// get before it is squashed, when no run is left on it. Every run adds
	// two commits and its cleanup one.
	maxPlansBranchCommits = 100

	// planRunRetention is how long the results of plan-only runs are kept
	planRunRetention = 30 * 24 * time.Hour

	// autoPlanTriggeredBy is the triggeredBy of plan-only runs started when a
	// version is published
	autoPlanTriggeredBy = "auto-plan"
)

// TerraformPlansGitops writes the plan-only runs of Terraform apps to the
// plans branch of the gitops repository
type TerraformPlansGitops interface {
	// DeployTerraformPlan commits a run's source and then its CR, pinned to
	// the source commit
	DeployTerraformPlan(p gitops.TerraformPlanDeploy) (gitops.TerraformCommits, error)
	// CleanTerraformPlans removes the runs keep rejects, in one commit
	CleanTerraformPlans(message string, keep func(gitops.PlanEntry) bool) ([]gitops.PlanEntry, error)
	// SquashTerraformPlans replaces the branch's history with one commit if
	// it is longer than maxCommits and holds no run
	SquashTerraformPlans(maxCommits int) (bool, error)
}

// requirePlanRuns returns an error if smithd can't make plan-only runs
func (s *Service) requirePlanRuns() error {
	if err := s.requireTerraform(); err != nil {
		return err
	}
	if s.PlanRuns == nil || s.TerraformPlansGitops == nil {
		return invalid("terraform_plans_disabled", "Plan-only runs are off: smithd has no access to the plans branch of the gitops repository")
	}
	return nil
}

// PlanVersion plans a draft or published terraform version for an
// environment without deploying it, and returns the run, which is planning.
// It writes the bundle source and a planOnly Terraform CR to the plans
// branch of the gitops repository, never to the main branch; the controller
// plans it with the read-only ServiceAccount and roles and without the state
// lock, and trackPlanRun stores the result.
//
// A plan-only run is not a deployment: it can't be approved or applied, and
// it doesn't touch the root's deployments. A newer run for the same root
// replaces the older one, which is superseded; a local plan (a temporary
// version) only replaces the same user's earlier local plan of the root. Everything that can be checked
// up front is, so a refused run leaves no record.
func (s *Service) PlanVersion(ctx context.Context, appID, versionID, environment, triggeredBy string) (*models.PlanRun, error) {
	if err := s.requirePlanRuns(); err != nil {
		return nil, err
	}
	if err := s.requireEnvironment(environment); err != nil {
		return nil, err
	}
	app, err := s.getApp(appID)
	if err != nil {
		return nil, err
	}
	version, err := s.Versions.GetByVersionID(appID, versionID)
	if err != nil {
		if err.Error() == "version not found" {
			return nil, notFound("Version not found")
		}
		return nil, fmt.Errorf("failed to get version: %w", err)
	}
	if version.ArtifactType != models.ArtifactTerraform {
		return nil, invalid("not_terraform", fmt.Sprintf("Version %s is a %s version; only terraform versions can be planned", version.VersionID, version.ArtifactType))
	}
	requestedBy := ""
	if version.Temporary {
		if requestedBy, err = s.requireLocalPlanOwner(ctx, version); err != nil {
			return nil, err
		}
	}
	tfEnv, err := s.terraformTarget(app, environment)
	if err != nil {
		return nil, err
	}
	bundle, err := s.terraformBundle(app, version)
	if err != nil {
		return nil, err
	}
	if version.Temporary {
		if err := checkLocalPlanBundle(version, bundle); err != nil {
			return nil, err
		}
	}
	image, err := s.TerraformRoots.RunnerImage(bundle.OpenTofuVersion)
	if err != nil {
		return nil, invalid("unsupported_opentofu_version", fmt.Sprintf("Version %s can't be planned: %s", version.VersionID, err.Error()))
	}

	root := defaultTerraformRoot
	run, superseded, err := s.PlanRuns.Create(app.ID, version, environment, root, triggeredBy, requestedBy)
	if err != nil {
		return nil, fmt.Errorf("failed to create plan run: %w", err)
	}
	run.AppName = app.Name

	ctx = deployCtx(ctx, app.Name, version, environment, "")
	ctx = logging.AppendLogAttrs(ctx, logging.Field("plan_run_id", run.ID))
	for i := range superseded {
		logging.LogInfoCtx(ctx, "Replacing the plan-only run of an earlier version", logging.Field("superseded_plan_run_id", superseded[i].ID))
		s.notifySupersededPlanRun(ctx, app, &superseded[i], run)
	}
	notice := s.planNotice(run, version, nil)
	s.GitHub.PlanRunStarted(ctx, notice)
	logging.LogInfoCtx(ctx, "Plan-only run started")

	name := terraform.PlanName(run.ID)
	sourcePath := gitops.TerraformPlanSourcePath(environment, app.Name, name, root)
	tfVarsFile := ""
	if _, ok := bundle.Files[tfbundle.EnvDir+"/"+environment+".tfvars"]; ok {
		tfVarsFile = sourcePath + "/" + tfbundle.EnvDir + "/" + environment + ".tfvars"
	}
	commits, err := s.TerraformPlansGitops.DeployTerraformPlan(gitops.TerraformPlanDeploy{
		Name: name,
		TerraformDeploy: gitops.TerraformDeploy{
			App:         app.Name,
			Environment: environment,
			Root:        root,
			Source:      bundle.Files,
			Manifest: func(branch, sourceCommit string) ([]byte, error) {
				return terraform.RootPlanOnlyManifest(terraform.Root{
					App:                app.Name,
					Environment:        environment,
					Name:               root,
					VersionID:          version.VersionID,
					Branch:             branch,
					SourceCommit:       sourceCommit,
					SourcePath:         sourcePath,
					TFVarsFile:         tfVarsFile,
					RunnerImage:        image,
					Backend:            tfEnv.Backend,
					EncryptionFallback: tfEnv.EncryptionFallback,
				}, run.ID, s.TerraformRoots)
			},
			SourceMessage:   fmt.Sprintf("Add terraform source of %s version %s for a plan in %s", app.Name, version.VersionID, environment),
			ManifestMessage: fmt.Sprintf("Plan %s version %s in %s (terraform root %s, plan only, run %s)", app.Name, version.VersionID, environment, root, run.ID),
		},
	})
	if err != nil {
		logging.LogErrorCtx(ctx, "Plan-only run failed: "+err.Error(), err)
		s.finishPlanRun(ctx, run, version, models.PlanRunPending, models.PlanRunFailed, "gitops: "+err.Error(), nil)
		return run, err
	}

	planID := terraform.PlanID(commits.Branch, commits.Source)
	committed, err := s.PlanRuns.SetCommitted(run.ID, commits.Manifest, commits.Source, planID)
	if err != nil {
		// The change is pushed and the controller plans it either way
		logging.LogErrorCtx(ctx, "Failed to record plan-only run commit: "+err.Error(), err)
	}
	run.GitopsCommitSHA = commits.Manifest
	run.SourceCommitSHA = commits.Source
	run.PlanID = planID
	if err == nil && !committed {
		// A newer run replaced it while it was being committed; its files
		// are removed with the next cleanup
		logging.LogInfoCtx(ctx, "Plan-only run was replaced while it was being committed")
		run.Status = models.PlanRunSuperseded
		return run, nil
	}
	run.Status = models.PlanRunPlanning

	s.FluxReconciler.Trigger(ctx)
	logging.LogInfoCtx(ctx, "Plan-only run committed, waiting for the plan",
		logging.Field("commit_sha", commits.Manifest),
		logging.Field("plan_id", planID),
	)
	return run, nil
}

// TriggerAutoPlans starts a plan-only run, in the background, of a version
// that was just published, for every environment its app has Terraform
// enabled in, if the version's source is known: the plan is reported as the
// check run deploysmith/plan-<env> on its commit (DEP-24). The plan is
// read-only, so this is safe to do for every published version; it can be
// switched off (TERRAFORM_AUTO_PLAN).
func (s *Service) TriggerAutoPlans(ctx context.Context, app *models.Application, version *models.Version) {
	if !s.TerraformAutoPlan || version.ArtifactType != models.ArtifactTerraform || s.requirePlanRuns() != nil {
		return
	}
	source, err := s.Versions.GetSource(version.ID)
	if err != nil || !source.Reportable() {
		logging.LogInfoCtx(ctx, "Skipping auto-plan: no repository and commit known for the version")
		return
	}
	tfEnvs, err := s.TerraformEnvironments.List(app.ID)
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to list terraform environments for auto-plan: "+err.Error(), err)
		return
	}
	for _, tfEnv := range tfEnvs {
		if tfEnv.ProvisionedAt == nil {
			continue
		}
		// Detach from the caller (e.g. an HTTP request) so the run outlives it
		go func(environment string) {
			ctx := context.WithoutCancel(ctx)
			// PlanVersion records and reports failures itself
			if _, err := s.PlanVersion(ctx, app.ID, version.VersionID, environment, autoPlanTriggeredBy); err != nil {
				logging.LogWarnCtx(ctx, fmt.Sprintf("Auto-plan of %s for %s failed: %v", version.VersionID, environment, err))
			}
		}(tfEnv.Environment)
	}
}

// GetPlanRun returns a plan-only run with its plan
func (s *Service) GetPlanRun(id string) (*models.PlanRun, error) {
	if s.PlanRuns == nil {
		return nil, notFound("Plan run not found")
	}
	run, err := s.PlanRuns.Get(id)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, notFound("Plan run not found")
	}
	return run, nil
}

// GetVersionPlan returns the newest plan-only run of a version for an
// environment with its plan
func (s *Service) GetVersionPlan(appID, versionID, environment string) (*models.PlanRun, error) {
	if s.PlanRuns == nil {
		return nil, notFound("Plan run not found")
	}
	if _, err := s.getApp(appID); err != nil {
		return nil, err
	}
	run, err := s.PlanRuns.Latest(appID, versionID, environment)
	if err != nil {
		return nil, err
	}
	if run == nil {
		return nil, notFound(fmt.Sprintf("Version %s has no plan for %s", versionID, environment))
	}
	return run, nil
}

// pollPlanRuns looks at every plan-only run that waits for its plan once,
// and cleans up after the ones that are done
func (s *Service) pollPlanRuns(ctx context.Context) {
	if s.PlanRuns == nil {
		return
	}
	runs, err := s.PlanRuns.ListPlanning()
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to list plan-only runs: "+err.Error(), err)
		return
	}
	for i := range runs {
		s.trackPlanRun(ctx, &runs[i])
	}
	s.cleanupPlanRuns(ctx)
}

// trackPlanRun compares a plan-only run with the status of its Terraform CR
// and finishes it when the controller has planned the source, or failed to.
// Several smithd replicas may do this at the same time; only the replica
// that finishes the run in the database reports it.
func (s *Service) trackPlanRun(ctx context.Context, run *models.PlanRun) {
	if run.SourceCommitSHA == "" {
		return
	}
	app, err := s.Apps.GetByID(run.AppID)
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to get the application of a plan-only run: "+err.Error(), err)
		return
	}
	version, err := s.Versions.GetByVersionID(run.AppID, run.VersionID)
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to get the version of a plan-only run: "+err.Error(), err)
		return
	}
	run.AppName = app.Name
	ctx = deployCtx(ctx, app.Name, version, run.Environment, "")
	ctx = logging.AppendLogAttrs(ctx, logging.Field("plan_run_id", run.ID))

	namespace, cr := terraform.Namespace(app.Name), terraform.PlanName(run.ID)
	cluster, err := s.TerraformReader.Run(ctx, namespace, cr)
	if err != nil {
		logging.LogWarnCtx(ctx, "Error polling Terraform CR: "+err.Error())
		return
	}
	if cluster == nil || cluster.PlanRunID != run.ID {
		s.planRunWaiting(ctx, run, version, "Flux has not applied the Terraform CR of this plan-only run")
		return
	}

	source := run.SourceCommitSHA
	ready := cluster.Condition(terraform.ConditionReady)
	switch {
	case cluster.AttemptedRevision == source && ready.Status == "False":
		s.finishPlanRun(ctx, run, version, models.PlanRunPlanning, models.PlanRunFailed, s.terraformFailure(ctx, namespace, cr, cluster), nil)
		return
	case cluster.PlannedRevision == source:
		plan := cluster.Condition(terraform.ConditionPlan)
		withChanges := plan.Status == "True" && plan.Reason == terraform.ReasonPlannedWithChanges
		noChanges := ready.Status == "True" && ready.Reason == terraform.ReasonPlannedNoChanges
		if withChanges || noChanges {
			s.planRunPlanned(ctx, run, version, cluster, namespace, cr, noChanges)
			return
		}
	}
	s.planRunWaiting(ctx, run, version, "the controller has not planned this plan-only run")
}

// planRunWaiting leaves a run alone, until it has waited too long
func (s *Service) planRunWaiting(ctx context.Context, run *models.PlanRun, version *models.Version, reason string) {
	if time.Since(run.StartedAt) < terraformPlanTimeout {
		return
	}
	s.finishPlanRun(ctx, run, version, models.PlanRunPlanning, models.PlanRunFailed, fmt.Sprintf("%s within %s", reason, terraformPlanTimeout), nil)
}

// planRunPlanned stores the plan of a run, copied from the cluster, and
// finishes it
func (s *Service) planRunPlanned(ctx context.Context, run *models.PlanRun, version *models.Version, cluster *terraform.Run, namespace, cr string, noChanges bool) {
	readable, err := s.TerraformReader.ReadablePlan(ctx, namespace, cr)
	if err != nil {
		logging.LogWarnCtx(ctx, "Failed to read the plan: "+err.Error())
		return
	}
	var plan models.TerraformPlan
	switch {
	case readable != nil:
		plan = terraform.ParsePlan(readable.Text)
		plan.PlanHash = readable.Hash
	case noChanges:
		// The controller may keep no plan when there is nothing to show
		plan = terraform.ParsePlan("No changes. Your infrastructure matches the configuration.\n")
	default:
		s.planRunWaiting(ctx, run, version, "the readable plan is not available")
		return
	}
	plan.PlanID = run.PlanID
	if cluster.PendingPlan != "" {
		plan.PlanID = cluster.PendingPlan
	}
	if !cluster.PlannedAt.IsZero() {
		at := cluster.PlannedAt
		plan.PlannedAt = &at
	}
	s.finishPlanRun(ctx, run, version, models.PlanRunPlanning, models.PlanRunPlanned, "", &plan)
}

// finishPlanRun records the end of a run and reports it. It does nothing if
// the run was not in the from state any more, i.e. another replica finished
// it, which then reports it.
func (s *Service) finishPlanRun(ctx context.Context, run *models.PlanRun, version *models.Version, from, to, errorMsg string, plan *models.TerraformPlan) {
	errorMsg = truncateMessage(errorMsg, maxRunError)
	moved, err := s.PlanRuns.Finish(run.ID, from, to, errorMsg, plan)
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to record the plan-only run state: "+err.Error(), err)
		return
	}
	if !moved {
		return
	}
	run.Status, run.ErrorMessage, run.Plan = to, errorMsg, plan
	notice := s.planNotice(run, version, plan)

	switch to {
	case models.PlanRunPlanned:
		logging.LogInfoCtx(ctx, "Plan-only run planned",
			logging.Field("to_add", plan.ToAdd),
			logging.Field("to_change", plan.ToChange),
			logging.Field("to_destroy", plan.ToDestroy),
		)
		s.GitHub.PlanRunPlanned(ctx, notice)
	case models.PlanRunFailed:
		logging.LogWarnCtx(ctx, "Plan-only run failed: "+errorMsg)
		s.GitHub.PlanRunFailed(ctx, notice, errorMsg)
	}
}

// notifySupersededPlanRun closes the check run of a plan-only run that a
// newer run replaced
func (s *Service) notifySupersededPlanRun(ctx context.Context, app *models.Application, old, by *models.PlanRun) {
	version, err := s.Versions.GetByVersionID(old.AppID, old.VersionID)
	if err != nil {
		logging.LogWarnCtx(ctx, "Failed to get the version of a replaced plan-only run: "+err.Error())
		return
	}
	old.AppName = app.Name
	s.GitHub.PlanRunSuperseded(ctx, s.planNotice(old, version, nil), "replaced by a newer plan of "+by.VersionID)
}

// planNotice describes a plan-only run for GitHub: the deployment id is the
// run's, which identifies its check run
func (s *Service) planNotice(run *models.PlanRun, version *models.Version, plan *models.TerraformPlan) slack.Deploy {
	notice := slack.Deploy{
		App:          run.AppName,
		Version:      version.VersionID,
		Environment:  run.Environment,
		TriggeredBy:  run.TriggeredBy,
		DeploymentID: run.ID,
		Terraform:    true,
	}
	if s.GitHub != nil {
		if source, err := s.Versions.GetSource(version.ID); err == nil {
			notice.SourceRepo = source.Repository
			notice.SourceSHA = source.CommitSHA
		}
	}
	if plan != nil {
		notice.Plan = planSummary(plan)
	}
	return notice
}

// cleanupPlanRuns removes the files of finished plan-only runs from the
// plans branch, once their results are stored: Flux then prunes the CRs
// and the controller garbage-collects their plans. It only touches the
// branch when there is something to remove, in one commit. Once an hour it
// also removes what no run of the database needs (a run records itself
// before it pushes, so everything on the branch belongs to a run), squashes
// the history of the branch if it grew long, deletes old results and the
// expired temporary versions of local plans.
func (s *Service) cleanupPlanRuns(ctx context.Context) {
	if s.TerraformPlansGitops == nil {
		return
	}
	s.planSweepMu.Lock()
	sweep := time.Since(s.planSweepAt) >= planSweepInterval
	if sweep {
		s.planSweepAt = time.Now()
	}
	s.planSweepMu.Unlock()

	// Before the plans branch is swept, so that the leftovers of the plans
	// of the versions deleted here go with it
	if sweep {
		s.sweepTemporaryVersions(ctx)
	}

	uncleaned, err := s.PlanRuns.CountUncleaned()
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to count plan-only runs to clean up: "+err.Error(), err)
		return
	}
	if uncleaned == 0 && !sweep {
		return
	}

	// Runs that end after this are cleaned up next time
	cutoff := time.Now()
	// The active runs are read once the branch is, so that a run another
	// smithd has just started, which records itself before it pushes, is
	// seen. When in doubt a run is kept.
	keep := func(e gitops.PlanEntry) bool {
		active, err := s.PlanRuns.ListActive()
		if err != nil {
			logging.LogErrorCtx(ctx, "Failed to list plan-only runs: "+err.Error(), err)
			return true
		}
		for _, run := range active {
			if terraform.PlanName(run.ID) == e.Name {
				return true
			}
		}
		return false
	}
	removed, err := s.TerraformPlansGitops.CleanTerraformPlans("Remove finished plan-only runs", keep)
	if err != nil {
		logging.LogErrorCtx(ctx, "Failed to clean up plan-only runs: "+err.Error(), err)
		return
	}
	if len(removed) > 0 {
		logging.LogInfoCtx(ctx, fmt.Sprintf("Removed %d finished plan-only runs from the plans branch", len(removed)))
	}
	if err := s.PlanRuns.MarkCleaned(cutoff); err != nil {
		logging.LogErrorCtx(ctx, "Failed to record the clean-up of plan-only runs: "+err.Error(), err)
	}
	if !sweep {
		return
	}

	if squashed, err := s.TerraformPlansGitops.SquashTerraformPlans(maxPlansBranchCommits); err != nil {
		logging.LogWarnCtx(ctx, "Failed to squash the plans branch: "+err.Error())
	} else if squashed {
		logging.LogInfoCtx(ctx, "Squashed the history of the plans branch")
	}
	if deleted, err := s.PlanRuns.DeleteEndedBefore(time.Now().Add(-planRunRetention)); err != nil {
		logging.LogWarnCtx(ctx, "Failed to delete old plan-only runs: "+err.Error())
	} else if deleted > 0 {
		logging.LogInfoCtx(ctx, fmt.Sprintf("Deleted %d old plan-only runs", deleted))
	}
}
