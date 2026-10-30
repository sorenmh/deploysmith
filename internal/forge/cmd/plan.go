package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/sorenmh/deploysmith/internal/forge/client"
	"github.com/spf13/cobra"
)

// Exit codes of forge plan --wait, for CI to gate on
const (
	// ExitOK: the plan was made, with or without changes
	ExitOK = 0
	// ExitFailed: the plan failed, was replaced or timed out, or forge could
	// not reach smithd
	ExitFailed = 1
	// ExitPlanDestroys: the plan was made and destroys or replaces resources
	ExitPlanDestroys = 2
)

// ExitCodeError is an error that ends forge with a given exit code. With no Err
// nothing more is printed.
type ExitCodeError struct {
	Code int
	Err  error
}

func (e *ExitCodeError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitCodeError) Unwrap() error { return e.Err }

var (
	planApp          string
	planEnv          string
	planWait         bool
	planTimeout      time.Duration
	planPollInterval time.Duration
	planOutput       string
	planFull         bool
)

var planCmd = &cobra.Command{
	Use:   "plan [version]",
	Short: "Show what a terraform version would change in an environment",
	Long: `Plan a draft or published terraform version for an environment without
deploying it. smithd runs a read-only plan in the cluster (a plan-only run,
with read-only credentials and no state lock); nothing is applied.

With --wait, forge waits for the plan and prints its summary. Exit codes:

  0  the plan was made, with or without changes
  1  the plan failed, was replaced by a newer plan, or --timeout passed, or
     forge or smithd failed
  2  the plan was made and destroys or replaces resources, so CI can gate on it

Without --wait, forge starts the plan and prints the id of the run, which
returns the result in GET /api/v1/plan-runs/<id>.

With --output json forge prints the plan run as JSON (and nothing else) to
stdout.

The version defaults to the one from forge init.

Examples:
  forge plan --wait --env production                 # the version from forge init
  forge plan --wait 6996908-12 --env production
  forge plan --wait --env production --output json
  forge plan --env production                        # start it, don't wait`,
	Args: cobra.MaximumNArgs(1),
	RunE: runPlanCmd,
}

func init() {
	rootCmd.AddCommand(planCmd)

	planCmd.Flags().StringVar(&planApp, "app", "", "Application name (optional if app is bound or init was run)")
	planCmd.Flags().StringVar(&planEnv, "env", "", "Environment to plan for (required)")
	planCmd.Flags().BoolVar(&planWait, "wait", false, "Wait for the plan and print its summary")
	planCmd.Flags().DurationVar(&planTimeout, "timeout", 30*time.Minute, "How long --wait waits for the plan")
	planCmd.Flags().DurationVar(&planPollInterval, "poll-interval", 5*time.Second, "How often --wait asks smithd for the result")
	planCmd.Flags().StringVarP(&planOutput, "output", "o", "text", "Output format: text or json")
	planCmd.Flags().BoolVar(&planFull, "full", false, "Print the full plan, as tofu shows it")
	_ = planCmd.MarkFlagRequired("env")
	planCmd.SilenceUsage = true
}

func runPlanCmd(cmd *cobra.Command, args []string) error {
	if err := ValidateConfig(); err != nil {
		return err
	}
	var version string
	if len(args) == 1 {
		version = args[0]
	}
	appID, appName, version, err := ResolveVersion(version)
	if err != nil {
		return err
	}
	if planApp != "" && planApp != appName {
		if appID, err = client.NewClient(GetSmithdURL(), GetSmithdAPIKey()).GetAppIDByName(planApp); err != nil {
			return err
		}
		appName = planApp
	}

	// Ctrl-C ends the wait; the plan itself goes on in the cluster
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	return runPlan(ctx, planOptions{
		AppID:        appID,
		AppName:      appName,
		Version:      version,
		Environment:  planEnv,
		Wait:         planWait,
		Timeout:      planTimeout,
		PollInterval: planPollInterval,
		JSON:         planOutput == "json",
		Full:         planFull,
	}, client.NewClient(GetSmithdURL(), GetSmithdAPIKey()), cmd.OutOrStdout(), cmd.ErrOrStderr())
}

type planOptions struct {
	AppID, AppName, Version, Environment string
	Wait                                 bool
	Timeout, PollInterval                time.Duration
	JSON, Full                           bool
}

// planAPI is the part of the smithd client forge plan uses
type planAPI interface {
	StartPlan(appID, versionID, environment string) (*client.PlanRun, error)
	GetPlanRun(id string) (*client.PlanRun, error)
}

// maxPollErrors is how many requests in a row may fail while waiting before
// forge gives up, so that a restart of smithd doesn't fail a pipeline
const maxPollErrors = 5

// runPlan starts a plan-only run and, with Wait, waits for it. Progress and
// the result go to out as text, or only the plan run as JSON; messages go to
// errOut. It returns an *ExitCodeError to set the exit code.
func runPlan(ctx context.Context, opts planOptions, c planAPI, out, errOut io.Writer) error {
	if opts.Environment == "" {
		return errors.New("--env is required")
	}
	if opts.Wait && opts.Timeout <= 0 {
		return errors.New("--timeout must be positive")
	}
	logf := func(format string, args ...any) {
		if !opts.JSON {
			fmt.Fprintf(out, format+"\n", args...)
		}
	}

	logf("Planning %s for %s of %s...", opts.Version, opts.Environment, opts.AppName)
	run, err := c.StartPlan(opts.AppID, opts.Version, opts.Environment)
	if err != nil {
		return fmt.Errorf("failed to start the plan: %w", describeAPIError(err))
	}
	logf("  Plan run %s started", run.ID)

	if !opts.Wait {
		if opts.JSON {
			return printJSON(out, run)
		}
		fmt.Fprintf(out, "Not waiting; the result is at GET /api/v1/plan-runs/%s\n", run.ID)
		return nil
	}

	deadline := time.Now().Add(opts.Timeout)
	failures := 0
	for !run.Done() {
		if time.Now().After(deadline) {
			return &ExitCodeError{Code: ExitFailed, Err: fmt.Errorf("timed out after %s waiting for the plan (plan run %s is still %s)", opts.Timeout, run.ID, run.Status)}
		}
		select {
		case <-ctx.Done():
			return &ExitCodeError{Code: ExitFailed, Err: fmt.Errorf("stopped waiting for the plan (plan run %s is still %s)", run.ID, run.Status)}
		case <-time.After(opts.PollInterval):
		}

		next, err := c.GetPlanRun(run.ID)
		if err != nil {
			if failures++; failures >= maxPollErrors {
				return &ExitCodeError{Code: ExitFailed, Err: fmt.Errorf("failed to get plan run %s: %w", run.ID, describeAPIError(err))}
			}
			fmt.Fprintf(errOut, "Warning: failed to get plan run %s, trying again: %v\n", run.ID, err)
			continue
		}
		failures = 0
		run = next
	}

	if opts.JSON {
		if err := printJSON(out, run); err != nil {
			return err
		}
	} else {
		printPlanRun(out, run, opts)
	}
	switch {
	case run.Status == client.PlanRunFailed:
		return &ExitCodeError{Code: ExitFailed, Err: fmt.Errorf("the plan failed: %s", firstLine(run.ErrorMessage))}
	case run.Status == client.PlanRunSuperseded:
		return &ExitCodeError{Code: ExitFailed, Err: fmt.Errorf("the plan was replaced by a newer one: %s", run.ErrorMessage)}
	case run.Plan != nil && (run.Plan.Destroys || run.Plan.ToDestroy > 0):
		return &ExitCodeError{Code: ExitPlanDestroys, Err: errors.New("the plan destroys or replaces resources")}
	}
	return nil
}

// describeAPIError makes smithd's error response readable
func describeAPIError(err error) error {
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal([]byte(apiErr.Body), &body) == nil {
		if body.Error.Message != "" {
			return fmt.Errorf("smithd returned %d: %s", apiErr.Status, body.Error.Message)
		}
		if body.Message != "" {
			return fmt.Errorf("smithd returned %d: %s", apiErr.Status, body.Message)
		}
	}
	return err
}

func printJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// maxListed is how many resources of an action the text output lists
const maxListed = 50

// printPlanRun prints the result of a plan-only run as text
func printPlanRun(out io.Writer, run *client.PlanRun, opts planOptions) {
	switch run.Status {
	case client.PlanRunFailed:
		fmt.Fprintf(out, "\nPlan of %s for %s failed:\n%s\n", opts.Version, opts.Environment, strings.TrimSpace(run.ErrorMessage))
		return
	case client.PlanRunSuperseded:
		fmt.Fprintf(out, "\nPlan of %s for %s was replaced: %s\n", opts.Version, opts.Environment, run.ErrorMessage)
		return
	}
	plan := run.Plan
	if plan == nil {
		fmt.Fprintf(out, "\nPlan of %s for %s has no result\n", opts.Version, opts.Environment)
		return
	}

	if plan.ToAdd == 0 && plan.ToChange == 0 && plan.ToDestroy == 0 {
		fmt.Fprintf(out, "\nPlan of %s for %s: no changes. The infrastructure matches the configuration.\n", opts.Version, opts.Environment)
	} else {
		fmt.Fprintf(out, "\nPlan of %s for %s: %d to add, %d to change, %d to destroy\n", opts.Version, opts.Environment, plan.ToAdd, plan.ToChange, plan.ToDestroy)
	}
	if plan.Destroys || plan.ToDestroy > 0 {
		fmt.Fprintln(out, "\nWARNING: this plan destroys or replaces resources.")
	}
	for _, list := range []struct {
		title     string
		addresses []string
	}{
		{"Destroyed", plan.Resources.Destroy},
		{"Replaced (destroyed and created again)", plan.Resources.Replace},
		{"Created", plan.Resources.Create},
		{"Updated in place", plan.Resources.Update},
	} {
		if len(list.addresses) == 0 {
			continue
		}
		fmt.Fprintf(out, "\n%s (%d):\n", list.title, len(list.addresses))
		for i, address := range list.addresses {
			if i == maxListed {
				fmt.Fprintf(out, "  ... and %d more\n", len(list.addresses)-maxListed)
				break
			}
			fmt.Fprintf(out, "  %s\n", address)
		}
	}
	if plan.OutputChanges {
		fmt.Fprintln(out, "\nOutputs change.")
	}
	if opts.Full && plan.Text != "" {
		fmt.Fprintf(out, "\n%s\n", strings.TrimRight(plan.Text, "\n"))
	}
}
