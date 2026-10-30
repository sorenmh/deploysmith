package web

import (
	"fmt"
	"time"

	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// ago formats a time relative to now, e.g. "5 min ago"
func ago(t any) string {
	var ts time.Time
	switch v := t.(type) {
	case time.Time:
		ts = v
	case *time.Time:
		if v == nil {
			return ""
		}
		ts = *v
	default:
		return ""
	}
	if ts.IsZero() {
		return ""
	}

	d := time.Since(ts)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "yesterday"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return ts.Format("2 Jan 2006")
	}
}

// shortSHA abbreviates a git commit SHA
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// counts formats a plan's counts compactly, e.g. "+2 ~1 -1"
func counts(plan models.TerraformPlan) string {
	return fmt.Sprintf("+%d ~%d -%d", plan.ToAdd, plan.ToChange, plan.ToDestroy)
}

// planAction is the resources a plan changes in one way
type planAction struct {
	Title     string
	Class     string // add, change or destroy, to style the list
	Resources []string
}

// actions lists the resources of a plan by action, destroys first, leaving
// out the actions without resources
func actions(plan models.TerraformPlan) []planAction {
	var result []planAction
	for _, action := range []planAction{
		{"Destroy", "destroy", plan.Resources.Destroy},
		{"Replace (destroy and create again)", "destroy", plan.Resources.Replace},
		{"Update", "change", plan.Resources.Update},
		{"Create", "add", plan.Resources.Create},
	} {
		if len(action.Resources) > 0 {
			result = append(result, action)
		}
	}
	return result
}
