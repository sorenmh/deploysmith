# Deploy status on GitHub (check runs)

smithd can report each deployment back to GitHub as a **check run** on the
commit the version was built from, so a PR or commit shows where it is
deployed and whether that worked:

| Check run | When |
| --- | --- |
| `deploysmith/deploy-<env>` | every deployment of the version to `<env>`, manual or auto-deploy |
| `deploysmith/plan-<env>` | the plan of a Terraform deployment (DEP-19), and the plan-only run of a terraform version, made by `forge plan` or automatically when the version is published (DEP-22) |

A deployment's check run is **queued** when the deployment is requested,
**in progress** while Flux applies it (with `FLUX_KUSTOMIZATION_NAME`), and
**completed** with *success* or *failure*. Its summary names the app,
version, environment, who or which policy started it and the gitops commit;
on failure the error is shown below it. With `UI_PUBLIC_URL` set, **Details**
links to the deployment in the web UI. Each deployment gets its own check
run; GitHub shows the latest one per name.

A plan-only run's `deploysmith/plan-<env>` check is **in progress** until the
controller has made the plan, then **completed**: *success* for a plan without
destroys (including no changes), *neutral* if it destroys or replaces
resources, and *failure* if the plan failed. A newer plan of the same app and
environment ends the older check as *neutral* ("Plan replaced"). The summary
has the counts and the resources by action, not the plan text; a plan-only run
has no deploy check, since nothing is deployed.

Reporting is a side channel: if GitHub is down, the app is not installed on
the repository or anything else goes wrong, smithd logs it and the
deployment carries on.

Reporting is off unless `GITHUB_APP_ID` and `GITHUB_APP_PRIVATE_KEY` are set.

## How it works

1. In CI, `forge init` sends where the version is built from: the
   repository, the commit and the pull request, detected from the GitHub
   Actions environment (for `pull_request` events the PR's **head** commit
   from the event payload, not the merge commit GitHub builds). See
   `forge init` in [forge.md](forge.md); `--source-repo`, `--source-sha` and
   `--pr` override it. smithd stores it on the version
   (`GET /api/v1/apps/{app}/versions/{version}` returns it as `source`).
2. smithd authenticates as a **GitHub App**: it signs a JWT (RS256, valid for
   9 minutes) with the app's private key, looks up the app's installation on
   the repository and asks for an installation token limited to that one
   repository. Tokens last an hour and are cached until five minutes before
   they expire. No personal token is involved.
3. Next to the Slack and Web Push notifications, smithd creates and then
   updates the check run with that token. A version without a repository and
   commit (e.g. built outside GitHub Actions without the flags) is not
   reported.

## Set up the GitHub App

Use the **same GitHub App** as for [login with GitHub](login-with-github.md),
if you use that: login needs its client id and secret, reporting its App ID
and a private key. To create one just for reporting:

1. **Organization settings → Developer settings → GitHub Apps → New GitHub
   App**.
2. **Homepage URL**: your smithd URL. **Webhook**: uncheck **Active**;
   reporting needs none.
3. **Permissions → Repository permissions**:
   - **Checks: Read and write** (create and update check runs)
   - **Metadata: Read-only** (always granted)

   For an existing login app, add **Checks** to its permissions; GitHub asks
   the organization to accept the new permission.
4. **Where can this GitHub App be installed?**: *Only on this account*.
5. Create it and note the **App ID** on its settings page (a number, not the
   client id; the client id works as well).
6. **Private keys → Generate a private key**: GitHub downloads a `.pem`.
7. **Install App** on the organization, choosing **Only select
   repositories** and the repositories that deploy with DeploySmith.
   Repositories without the app are skipped (logged at info level).

## Configure smithd

```bash
GITHUB_APP_ID=123456
GITHUB_APP_PRIVATE_KEY="$(cat deploysmith.2026-10-29.private-key.pem)"   # keep it in a secret
UI_PUBLIC_URL=https://smithd.example.com   # optional, for the Details link
# GitHub Enterprise Server only:
# GITHUB_API_URL=https://github.example.com/api/v3
```

`GITHUB_APP_PRIVATE_KEY` is the PEM itself; where an environment variable
can't hold newlines, write them as `\n`. smithd refuses to start with only
one of the two set, or with a key it can't read.

With the Helm chart:

```yaml
config:
  ui:
    publicUrl: https://smithd.example.com
  github:
    appId: "123456"

secrets:
  github:
    privateKey: |
      -----BEGIN RSA PRIVATE KEY-----
      ...
      -----END RSA PRIVATE KEY-----
  # or github-app-private-key in existingSecret
```

## CI

Nothing changes in a GitHub Actions workflow that already runs `forge init`.
To report on pull requests, run it for `pull_request` events too (e.g. with
an auto-deploy policy for PR branches to a preview environment). In a
`container:` job the event payload is mounted, so detection works there as
well; when running forge with `docker run`, pass the `GITHUB_*` variables
and mount `$GITHUB_EVENT_PATH`, or use the flags.

## Terraform deployments

A Terraform deployment is planned before anything is applied, and both are
reported on the version's commit:

| Check run | States |
| --- | --- |
| `deploysmith/plan-<env>` | **in progress** while the controller plans; **completed** with *neutral* when the plan has changes and waits for approval (title: `3 to add, 1 to change, 2 to destroy`, plus `(destroys resources)`), *success* when there are no changes or after the apply, *failure* if the plan failed, *neutral* if it went stale |
| `deploysmith/deploy-<env>` | **in progress** until the plan is applied (planning, waiting for approval, applying), then *success*; *failure* if the plan or apply failed (the error is below the summary); *neutral* if it went stale |

The summary of the plan check names the destroyed and replaced resources
first, in a warning, then the created and updated ones, and links to the
deployment in the web UI. The **plan text itself is not sent to GitHub**: it
can show infrastructure details, and it is in the web UI and
`smithctl deployment show --plan`, behind login.

Plan-only runs (`forge plan`, a publish, DEP-22) report the same
`deploysmith/plan-<env>` check as described at the top, without a deploy
check. Local plans (`forge plan --terraform`) report nothing.

## Several apps on one commit

The check names carry the environment but not the app. An app with manifests
and a Terraform app built from the same repository and commit (see
[terraform.md](terraform.md#an-app-with-manifests-and-terraform)) each get
their own check run named `deploysmith/deploy-<env>` on that commit, told apart
by the app in the summary. smithd creates and updates them separately (one per
deployment). How GitHub's checks list displays several runs that share a name
was not checked; it may show only the latest.

## Terraform guide

How plans appear on pull requests, and the rest of the Terraform workflow:
[terraform.md](terraform.md#the-plan-on-the-pull-request).
