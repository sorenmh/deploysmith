package terraform

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

var (
	// ansiPattern matches the colour codes of output made without -no-color
	ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

	// planLine is the summary line of a plan; "to import" is only there when
	// the plan imports resources
	planLine = regexp.MustCompile(`^Plan: (?:(\d+) to import, )?(\d+) to add, (\d+) to change, (\d+) to destroy\.`)

	// resourceLine is the comment above each resource the plan changes, with
	// two spaces of indentation. Attribute comments are indented further and
	// "has changed" lines of the drift section don't end in an action.
	resourceLine = regexp.MustCompile(`^  # (.+?) (will be created|will be updated in-place|will be destroyed|must be replaced|will be replaced, as requested)(?: .*)?$`)
)

// ParsePlan summarises a readable plan, the output of `tofu show` of the
// plan file: the counts of the "Plan: X to add, Y to change, Z to destroy."
// line and the addresses of the resources by action. Text is the plan
// without colour codes. The other fields of the result are not part of the
// text and left empty.
//
// Without a "Plan:" line (no changes, or changes to outputs only) the counts
// are those of the addresses found.
func ParsePlan(text string) models.TerraformPlan {
	text = ansiPattern.ReplaceAllString(text, "")
	plan := models.TerraformPlan{
		Text: text,
		Resources: models.TerraformPlanResources{
			Create:  []string{},
			Update:  []string{},
			Replace: []string{},
			Destroy: []string{},
		},
	}

	counted := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if m := planLine.FindStringSubmatch(line); m != nil {
			counted = true
			plan.ToAdd, _ = strconv.Atoi(m[2])
			plan.ToChange, _ = strconv.Atoi(m[3])
			plan.ToDestroy, _ = strconv.Atoi(m[4])
			continue
		}
		if line == "Changes to Outputs:" {
			plan.OutputChanges = true
			continue
		}
		m := resourceLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		address := m[1]
		switch m[2] {
		case "will be created":
			plan.Resources.Create = append(plan.Resources.Create, address)
		case "will be updated in-place":
			plan.Resources.Update = append(plan.Resources.Update, address)
		case "will be destroyed":
			plan.Resources.Destroy = append(plan.Resources.Destroy, address)
		default:
			plan.Resources.Replace = append(plan.Resources.Replace, address)
		}
	}

	if !counted {
		// Not seen in plans of other OpenTofu versions; count the addresses
		r := plan.Resources
		plan.ToAdd = len(r.Create) + len(r.Replace)
		plan.ToChange = len(r.Update)
		plan.ToDestroy = len(r.Destroy) + len(r.Replace)
	}
	plan.Destroys = plan.ToDestroy > 0 || len(plan.Resources.Destroy) > 0 || len(plan.Resources.Replace) > 0
	return plan
}

// NoChanges reports whether the plan changes nothing, not even outputs
func NoChanges(plan models.TerraformPlan) bool {
	return plan.ToAdd == 0 && plan.ToChange == 0 && plan.ToDestroy == 0 && !plan.OutputChanges &&
		len(plan.Resources.Create)+len(plan.Resources.Update)+len(plan.Resources.Replace)+len(plan.Resources.Destroy) == 0
}
