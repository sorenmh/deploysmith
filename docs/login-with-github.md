# Login with GitHub

smithd can sign people in with their GitHub account, so it knows who
deploys and who approves a Terraform plan. People log in to `smithctl` with
the OAuth device flow and to the web UI with GitHub's normal sign-in page.
Machines such as CI keep using API keys.

Login is off unless `GITHUB_CLIENT_ID` is set; smithd then only accepts API
keys, and the web UI is only served behind an authenticating proxy
(`UI_USER_HEADER`), as before.

## How it works

- **smithctl** (`smithctl login`): smithctl asks smithd to start the device
  flow; smithd asks GitHub for a code and passes it back. smithctl prints the
  code and `https://github.com/login/device`, and polls smithd, which polls
  GitHub, until you have entered it. smithctl talks only to smithd: it needs
  no GitHub settings and never sees a GitHub token or the client secret.
- **Web UI**: `/ui` sends you to a sign-in page, then to GitHub, which sends
  you back to `/ui/auth/callback`. A random `state` value, also kept in a
  short-lived cookie, must come back unchanged, so another site can't sign
  you in.
- Either way smithd uses the GitHub token only to read who you are and
  whether you may sign in, then revokes it. It then issues its **own**
  session token, valid for `SESSION_TTL` (12h by default). The database
  stores only a SHA-256 hash of it.
  - smithctl stores the session in `~/.deploysmith/sessions.json` (mode
    0600, one entry per smithd URL) and sends it as `Authorization: Bearer`
    instead of the API key until it expires. `smithctl logout` ends it,
    `smithctl whoami` shows who smithd sees.
  - The web UI keeps it in an `HttpOnly`, `SameSite=Lax` cookie, `Secure`
    when served over https. **Sign out** in the header ends it.
- Requests authenticated either way carry an identity in their context
  (`auth.FromContext`, `auth.Actor` gives the GitHub login or `api-key`).
  Deployments started by a signed-in user record their login.

## Who may sign in

Everyone has a GitHub account, so smithd requires at least one of:

| Setting | Lets in |
| --- | --- |
| `GITHUB_ALLOWED_USERS` | these GitHub logins (comma-separated) |
| `GITHUB_ALLOWED_ORG` | active members of this organization |
| `GITHUB_ALLOWED_TEAM` | with `GITHUB_ALLOWED_ORG`: only members of this team (its slug) |

A login in `GITHUB_ALLOWED_USERS` may always sign in; anyone else must pass
the org (and team) check. Membership is checked at sign-in, so someone who
leaves the org keeps access until their session expires.

## Create the GitHub App

A GitHub App is recommended (the same app can later report check runs,
DEP-24). An OAuth App works as well.

1. **Organization settings → Developer settings → GitHub Apps → New GitHub
   App** (or your personal settings, for an app owned by you).
2. **Homepage URL**: your smithd URL, e.g. `https://smithd.example.com`.
3. **Callback URL**: `https://smithd.example.com/ui/auth/callback`. It must
   match `UI_PUBLIC_URL` + `/ui/auth/callback` exactly.
4. Leave **Expire user authorization tokens** on; smithd revokes them
   anyway.
5. Check **Enable Device Flow** (needed by `smithctl login`).
6. **Webhook**: uncheck **Active**; login needs none.
7. **Permissions**: with `GITHUB_ALLOWED_ORG`, give it **Organization
   permissions → Members: Read-only**, so smithd can read org and team
   membership. Nothing else is needed for login.
8. **Where can this GitHub App be installed?**: *Only on this account*.
9. Create it, note the **Client ID**, and **Generate a new client secret**.
10. With `GITHUB_ALLOWED_ORG`: **Install App** on that organization, or the
    membership checks fail and nobody but `GITHUB_ALLOWED_USERS` gets in.

With an **OAuth App** instead (Developer settings → OAuth Apps): set the same
homepage and callback URL and check **Enable Device Flow**. smithd asks for
the `read:org` scope when `GITHUB_ALLOWED_ORG` is set, so the user grants it
at sign-in; if the organization restricts OAuth App access, an owner must
approve the app.

## Configure smithd

```bash
GITHUB_CLIENT_ID=Iv23liExample
GITHUB_CLIENT_SECRET=...            # keep it in a secret
GITHUB_ALLOWED_ORG=acme             # and/or GITHUB_ALLOWED_USERS=octocat,hubot
GITHUB_ALLOWED_TEAM=deployers       # optional
SESSION_TTL=12h                     # optional
UI_PUBLIC_URL=https://smithd.example.com
# GitHub Enterprise Server only:
# GITHUB_URL=https://github.example.com   (API defaults to <url>/api/v3)
# GITHUB_API_URL=...
```

`UI_PUBLIC_URL` gives the callback URL and decides whether cookies are
`Secure`; without it both are derived from the request (`X-Forwarded-Proto`
counts as https). With login configured the web UI is served on the API
port without a proxy, so don't also set `UI_USER_HEADER` (smithd refuses
both), and don't put oauth2-proxy in front of it.

With the Helm chart, see "Login with GitHub" in
[charts/smithd/README.md](../charts/smithd/README.md).

## Use it

```bash
smithctl login --url https://smithd.example.com
# Open https://github.com/login/device and enter the code: ABCD-1234
smithctl whoami
smithctl deploy my-app v1.2.3 --env production   # recorded as your login
smithctl logout
```

`--url` (or `SMITHD_URL`, or the config file) picks the server; a session is
only used for the URL it was created for. CI keeps `SMITHD_API_KEY`.

## What the identity is used for

- **Who deployed**: an API deploy by a signed-in user records the user as `triggeredBy`.
- **Who approved or rejected a Terraform plan**: `smithctl deployment approve|reject` and the web UI's buttons record your GitHub login, the time and, for destroys, that you confirmed them ([terraform.md](terraform.md#operator-workflow)). With only an API key the approver is recorded as `api-key`. Everyone who may sign in may approve; there is no approver allowlist yet.
- **Local plans**: `forge plan --terraform` needs a session, not an API key, so smithd knows whose temporary version it is (forge reads the same `~/.deploysmith/sessions.json`; [forge.md](forge.md#local-plan-forge-plan---terraform)).

## API

| Endpoint | Auth | |
| --- | --- | --- |
| `POST /api/v1/auth/device` | none | starts the device flow: `deviceCode`, `userCode`, `verificationUri`, `expiresIn`, `interval` |
| `POST /api/v1/auth/device/token` | none | `{"deviceCode": ...}`: 202 `{"status":"pending","interval":n}` until the code is entered, then 200 `{"token","login","expiresAt"}`; 403 if cancelled or not allowed, 410 when expired |
| `GET /api/v1/auth/whoami` | key or session | `{"kind":"user","login":...,"expiresAt":...}` or `{"kind":"api-key"}` |
| `POST /api/v1/auth/logout` | session | ends the session, 204 |

A request with an expired session gets 401 with code `session_expired`.
