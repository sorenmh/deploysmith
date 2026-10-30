package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/sorenmh/deploysmith/internal/forge/client"
	"github.com/sorenmh/deploysmith/internal/shared/config"
	"github.com/sorenmh/deploysmith/internal/shared/tfbundle"
	"github.com/spf13/cobra"
)

// localPlanAPI is the part of the smithd client a local plan uses
type localPlanAPI interface {
	planAPI
	PrepareLocalPlan(appID, environment, contentHash string) (*client.LocalPlan, error)
}

// uploadFunc uploads a file to a presigned URL
type uploadFunc func(presignedURL, filename string, content []byte) error

// errNotSignedIn tells how to sign in, for the people who run local plans
func errNotSignedIn(url, reason string) error {
	return fmt.Errorf("%s: a local plan needs you to be signed in, run `smithctl login` (smithd at %s)", reason, url)
}

// loadSession returns the session of the user who is signed in to smithd.
// Local plans are a person's: forge never falls back to an API key, which
// smithd refuses for them anyway.
func loadSession(url string) (*config.Session, error) {
	session, err := config.LoadSession(url)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, errNotSignedIn(url, "you are not signed in")
	}
	if session.Expired() {
		return nil, errNotSignedIn(url, "your session has expired")
	}
	return session, nil
}

// runLocalPlanCmd plans the working copy: it needs the session of the user
// who is signed in, and waits for the plan
func runLocalPlanCmd(cmd *cobra.Command) error {
	if err := config.ValidateURL(); err != nil {
		return err
	}
	session, err := loadSession(GetSmithdURL())
	if err != nil {
		return err
	}
	c := client.NewClient(GetSmithdURL(), "").WithSessionToken(session.Token)

	appID, appName, err := resolveLocalPlanApp(c)
	if err != nil {
		return err
	}

	// Ctrl-C ends the wait; the plan itself goes on in the cluster
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	return runLocalPlan(ctx, planOptions{
		AppID:        appID,
		AppName:      appName,
		Environment:  planEnv,
		Wait:         true,
		Timeout:      planTimeout,
		PollInterval: planPollInterval,
		JSON:         planOutput == "json",
		Full:         planFull,
	}, planTerraform, c, uploadContent, cmd.OutOrStdout(), cmd.ErrOrStderr())
}

// resolveLocalPlanApp returns the app of a local plan: --app, or the app
// bound to this directory (forge app-bind), or the one of forge init
func resolveLocalPlanApp(c *client.Client) (string, string, error) {
	if planApp != "" {
		appID, err := c.GetAppIDByName(planApp)
		if err != nil {
			return "", "", err
		}
		return appID, planApp, nil
	}
	if appConfig, err := LoadAppConfig(); err == nil {
		return appConfig.AppID, appConfig.AppName, nil
	}
	if versionInfo, err := LoadVersionInfo(); err == nil {
		return versionInfo.AppID, versionInfo.App, nil
	}
	return "", "", errors.New("no app: use --app, or run 'forge app-bind' in this directory")
}

// runLocalPlan validates and packs the bundle in dir like 'forge upload
// --terraform', uploads it as the temporary version smithd makes for it, and
// plans that version as runPlan does, waiting for the result. opts.Version is
// set to the temporary version.
func runLocalPlan(ctx context.Context, opts planOptions, dir string, c localPlanAPI, upload uploadFunc, out, errOut io.Writer) error {
	if opts.Environment == "" {
		return errors.New("--env is required")
	}
	logf := func(format string, args ...any) {
		if !opts.JSON {
			say(out, format+"\n", args...)
		}
	}

	logf("Creating terraform bundle from %s...", dir)
	files, err := tfbundle.Collect(dir)
	if err != nil {
		return err
	}
	bundle, err := tfbundle.Validate(files)
	if err != nil {
		return err
	}
	archive, err := tfbundle.Pack(bundle.Files)
	if err != nil {
		return err
	}
	logf("  %d files, OpenTofu %s (%.1f KB)", len(bundle.Files), bundle.OpenTofuVersion, float64(len(archive))/1024)

	local, err := c.PrepareLocalPlan(opts.AppID, opts.Environment, tfbundle.ContentHash(bundle.Files))
	if err != nil {
		return fmt.Errorf("failed to prepare the local plan: %w", describeAPIError(err))
	}
	logf("Uploading terraform bundle as %s (kept until %s)...", local.VersionID, local.ExpiresAt.Local().Format(time.DateTime))
	if err := upload(local.UploadURL, tfbundle.ArchiveName, archive); err != nil {
		return fmt.Errorf("failed to upload bundle: %w", err)
	}

	opts.Version = local.VersionID
	return runPlan(ctx, opts, c, out, errOut)
}
