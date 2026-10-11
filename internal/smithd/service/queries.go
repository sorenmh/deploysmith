package service

import (
	"fmt"
	"sort"

	"github.com/sorenmh/deploysmith/internal/smithd/models"
	"github.com/sorenmh/deploysmith/internal/smithd/store"
)

// maxApps bounds the overview; smithd is meant for a modest number of apps
const maxApps = 500

// Overview returns what runs where: every app with its current version and
// latest deployment in each environment
func (s *Service) Overview() (*models.Overview, error) {
	environments, err := s.Environments.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list environments: %w", err)
	}
	apps, _, err := s.Apps.List(maxApps, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to list applications: %w", err)
	}
	current, err := s.Deployments.LatestSuccessPerTarget()
	if err != nil {
		return nil, err
	}
	latest, err := s.Deployments.LatestPerTarget()
	if err != nil {
		return nil, err
	}

	overview := &models.Overview{Environments: []string{}, Apps: []models.AppOverview{}}
	for _, env := range environments {
		overview.Environments = append(overview.Environments, env.Name)
	}

	rows := map[string]*models.AppOverview{}
	for _, app := range apps {
		rows[app.ID] = &models.AppOverview{ID: app.ID, Name: app.Name, Targets: map[string]models.TargetState{}}
	}
	for _, d := range current {
		if row, ok := rows[d.AppID]; ok {
			target := row.Targets[d.Environment]
			target.CurrentVersion = d.VersionID
			row.Targets[d.Environment] = target
		}
	}
	for i := range latest {
		d := latest[i]
		if row, ok := rows[d.AppID]; ok {
			target := row.Targets[d.Environment]
			target.LastDeployment = &d
			row.Targets[d.Environment] = target
		}
	}

	for _, row := range rows {
		overview.Apps = append(overview.Apps, *row)
	}
	sort.Slice(overview.Apps, func(i, j int) bool { return overview.Apps[i].Name < overview.Apps[j].Name })
	return overview, nil
}

// AppDetail is everything shown about a single app
type AppDetail struct {
	App               *models.Application
	Environments      []string
	CurrentVersions   map[string]string                   // environment -> version id
	LastDeployments   map[string]*models.DeploymentDetail // environment -> latest attempt
	Versions          []models.Version
	TotalVersions     int
	Policies          []models.Policy
	RecentDeployments []models.DeploymentDetail
}

// RunningIn returns the environments currently running the given version
func (d *AppDetail) RunningIn(versionID string) []string {
	var envs []string
	for _, env := range d.Environments {
		if d.CurrentVersions[env] == versionID {
			envs = append(envs, env)
		}
	}
	return envs
}

// GetAppDetail returns an app with its versions (newest first, paginated),
// policies and recent deployments
func (s *Service) GetAppDetail(appID string, versionLimit, versionOffset int) (*AppDetail, error) {
	app, err := s.getApp(appID)
	if err != nil {
		return nil, err
	}

	detail := &AppDetail{App: app}

	environments, err := s.Environments.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list environments: %w", err)
	}
	for _, env := range environments {
		detail.Environments = append(detail.Environments, env.Name)
	}

	if detail.CurrentVersions, err = s.Apps.GetCurrentVersions(appID); err != nil {
		return nil, err
	}
	latest, err := s.Deployments.LatestPerTarget()
	if err != nil {
		return nil, err
	}
	detail.LastDeployments = map[string]*models.DeploymentDetail{}
	for i := range latest {
		if latest[i].AppID == appID {
			detail.LastDeployments[latest[i].Environment] = &latest[i]
		}
	}

	if detail.Versions, detail.TotalVersions, err = s.Versions.List(appID, versionLimit, versionOffset); err != nil {
		return nil, err
	}
	if detail.Policies, err = s.Policies.List(appID); err != nil {
		return nil, err
	}
	if detail.RecentDeployments, _, err = s.Deployments.ListDetailed(store.DeploymentFilter{AppID: appID}, 5, 0); err != nil {
		return nil, err
	}

	return detail, nil
}

// GetDeployment returns a deployment with app, version and policy names
func (s *Service) GetDeployment(deploymentID string) (*models.DeploymentDetail, error) {
	deployment, err := s.Deployments.GetDetailed(deploymentID)
	if err != nil {
		if err.Error() == "deployment not found" {
			return nil, notFound("Deployment not found")
		}
		return nil, err
	}
	return deployment, nil
}
