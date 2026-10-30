package terraform

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/sorenmh/deploysmith/internal/smithd/models"
)

// The samples in testdata/plans are the output of `tofu show -no-color` of
// real plans made with OpenTofu 1.13.2 (terraform_data resources and a module
// with for_each), the text tofu-controller stores in the readable plan.
func TestParsePlan(t *testing.T) {
	tests := []struct {
		sample string
		want   models.TerraformPlan
	}{
		{
			sample: "add",
			want: models.TerraformPlan{
				ToAdd: 5, OutputChanges: true,
				Resources: models.TerraformPlanResources{
					Create: []string{
						"terraform_data.db",
						"terraform_data.web",
						"terraform_data.with_secret",
						`module.queues.terraform_data.item["a"]`,
						`module.queues.terraform_data.item["b"]`,
					},
				},
			},
		},
		{
			sample: "change",
			want: models.TerraformPlan{
				ToChange:  1,
				Resources: models.TerraformPlanResources{Update: []string{"terraform_data.web"}},
			},
		},
		{
			sample: "replace",
			want: models.TerraformPlan{
				ToAdd: 1, ToDestroy: 1, Destroys: true,
				Resources: models.TerraformPlanResources{Replace: []string{"terraform_data.rep"}},
			},
		},
		{
			sample: "replace-requested",
			want: models.TerraformPlan{
				ToAdd: 1, ToDestroy: 1, Destroys: true, OutputChanges: true,
				Resources: models.TerraformPlanResources{Replace: []string{"terraform_data.web"}},
			},
		},
		{
			sample: "destroy",
			want: models.TerraformPlan{
				ToDestroy: 2, Destroys: true,
				Resources: models.TerraformPlanResources{Destroy: []string{"terraform_data.db", `module.queues.terraform_data.item["b"]`}},
			},
		},
		{
			sample: "destroy-all",
			want: models.TerraformPlan{
				ToDestroy: 5, Destroys: true, OutputChanges: true,
				Resources: models.TerraformPlanResources{Destroy: []string{
					"terraform_data.new",
					"terraform_data.rep",
					"terraform_data.web",
					`module.queues.terraform_data.item["a"]`,
					`module.queues.terraform_data.item["c"]`,
				}},
			},
		},
		{
			sample: "mixed",
			want: models.TerraformPlan{
				ToAdd: 3, ToChange: 1, ToDestroy: 2, Destroys: true, OutputChanges: true,
				Resources: models.TerraformPlanResources{
					Create:  []string{"terraform_data.new", `module.queues.terraform_data.item["c"]`},
					Update:  []string{"terraform_data.web"},
					Replace: []string{"terraform_data.rep"},
					Destroy: []string{"terraform_data.with_secret"},
				},
			},
		},
		{
			// Outputs only: no "Plan:" line
			sample: "outputs",
			want:   models.TerraformPlan{OutputChanges: true},
		},
		{
			sample: "nochange",
			want:   models.TerraformPlan{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.sample, func(t *testing.T) {
			text, err := os.ReadFile("testdata/plans/" + tt.sample + ".txt")
			if err != nil {
				t.Fatal(err)
			}
			got := ParsePlan(string(text))

			if got.Text != string(text) {
				t.Error("the text was changed")
			}
			got.Text = ""
			// Empty lists, not null, in the API
			for _, list := range []*[]string{&tt.want.Resources.Create, &tt.want.Resources.Update, &tt.want.Resources.Replace, &tt.want.Resources.Destroy} {
				if *list == nil {
					*list = []string{}
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got  %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestNoChanges(t *testing.T) {
	for sample, want := range map[string]bool{"nochange": true, "outputs": false, "change": false, "destroy": false} {
		text, err := os.ReadFile("testdata/plans/" + sample + ".txt")
		if err != nil {
			t.Fatal(err)
		}
		if got := NoChanges(ParsePlan(string(text))); got != want {
			t.Errorf("NoChanges(%s) = %v, want %v", sample, got, want)
		}
	}
}

// Plans of other tools' versions and runs made with colours are read too
func TestParsePlanVariants(t *testing.T) {
	t.Run("colour codes", func(t *testing.T) {
		plan := ParsePlan("  \x1b[1m# aws_s3_bucket.logs\x1b[0m will be \x1b[1m\x1b[31mdestroyed\x1b[0m\n\nPlan: 0 to add, 0 to change, 1 to destroy.\n")
		if !reflect.DeepEqual(plan.Resources.Destroy, []string{"aws_s3_bucket.logs"}) || plan.ToDestroy != 1 || !plan.Destroys {
			t.Errorf("unexpected plan %+v", plan)
		}
		if strings.Contains(plan.Text, "\x1b") {
			t.Error("the colour codes were kept")
		}
	})

	t.Run("imports", func(t *testing.T) {
		plan := ParsePlan("Plan: 1 to import, 2 to add, 0 to change, 0 to destroy.\n")
		if plan.ToAdd != 2 || plan.ToChange != 0 || plan.ToDestroy != 0 || plan.Destroys {
			t.Errorf("unexpected plan %+v", plan)
		}
	})

	t.Run("drift and deposed objects", func(t *testing.T) {
		text := `OpenTofu detected the following changes made outside of OpenTofu since the
last "tofu apply":

  # aws_instance.web has changed
  ~ resource "aws_instance" "web" {
        id = "i-1"
    }

OpenTofu will perform the following actions:

  # aws_instance.web (deposed object 1a2b3c4d) will be destroyed
  # (left over from a failed replacement)
  - resource "aws_instance" "web" {
        # (3 unchanged attributes hidden)
    }

Plan: 0 to add, 0 to change, 1 to destroy.
`
		plan := ParsePlan(text)
		// The drift section lists what changed, not what will be done
		if !reflect.DeepEqual(plan.Resources.Destroy, []string{"aws_instance.web (deposed object 1a2b3c4d)"}) || len(plan.Resources.Update) != 0 {
			t.Errorf("unexpected resources %+v", plan.Resources)
		}
	})

	t.Run("without a Plan line the counts come from the addresses", func(t *testing.T) {
		plan := ParsePlan("  # a.b will be created\n  # c.d must be replaced\n  # e.f will be updated in-place\n")
		if plan.ToAdd != 2 || plan.ToChange != 1 || plan.ToDestroy != 1 || !plan.Destroys {
			t.Errorf("unexpected counts %+v", plan)
		}
	})

	t.Run("attribute comments are not resources", func(t *testing.T) {
		plan := ParsePlan("  # a.b will be created\n  + resource \"a\" \"b\" {\n      # (1 unchanged attribute hidden)\n      # c.d will be destroyed\n    }\n")
		if !reflect.DeepEqual(plan.Resources.Create, []string{"a.b"}) || len(plan.Resources.Destroy) != 0 {
			t.Errorf("unexpected resources %+v", plan.Resources)
		}
	})
}
