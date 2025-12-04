# DeploySmith

This is a service, which sits inside a Kubernetes cluster and controls app deployments. It takes a gitops approach and collaborates with flux, which then takes care of the reconciliation. The service is multi-tenant, so it can control deployments for multiple applications - even if they are running in different clusters.

## Use case

The product is aiming to be open sourced and utilised in different environments eventually, but for a start it will be applied to a few hobby projects with nearly no load.

## Components

DeploySmith consist of 3 components:

**smithd** The server component, which runs inside k8s. It exposes an API and mutates the gitops state to match the requested state.

**forge** The CI component, it helps "forge" new versions by transforming a YAML file into Kubernetes manifest files and delivers them to smithd.

**smithctl** The controller which the user/developer interacts with for controlling which version to run.

## Deployment flow

smithd does not deal with manifest mutations itself, but it enables the CI pipelines of other applications to publish new versions of itself which contains the k8s manifests.

Although smithd does not build manifest files, there is an auxiliary application, **forge**, which can help build manifest files from a much simpler yaml file.

**smithd** will then decide if the new version should be deployed automatically - or the user can manually command that it wants the version deployed by using the Rest API.

1. CI pipeline drafts version → gets pre-signed S3 URL → uploads manifests → publishes version
2. smithd (optionally auto-deploys based on policy) → fetches from S3 → writes to gitops repo
3. Flux reconciles the gitops repo → deploys to Kubernetes

## API

smithd exposes APIs which can:

1. List versions available
   The list will contain git sha, committer/user, datetime and status (currently deployed or not)
2. Initiate a deployment of a specific version
3. Specify auto deployment policies based on git patterns such as branch names or tags. Ie. auto deploy master branch to prod.
4. Draft a new version
5. Publish a drafted version
6. Register application to be controlled by DeploySmith

## Drafting and publishing a version

New versions are usually created by a CI pipeline which builds k8s manifests in whatever way makes sense in that case.
Along with the k8s manifest files, there will also be a version.yml file, which describes the version with some metadata, such as git sha, git branch, git committer, build no., date and time.

1. The CI pipeline calls the version draft API endpoint of smithd, which will register the version in its database and return a pre-signed URL to a blob storage.

2. The CI pipeline then starts uploading k8s manifests to the blob storage returned by smithd.

3. Once the new version is ready to go, the pipeline calls the publish version endpoint of smithd, which will wrap up the version and make it immutable. From there on the version is available to be deployed.

4. If there is a matching auto deploy policy defined for this application, then a deployment of the new version will be initiated automatically.

## Deployment

When smithd wants to deploy a new version, it will fetch it from its version storage and upload the files to the gitops repo. Flux will take it from there.

## Authentication

Initially we are going to use a cheap way of authenticating use an API key, but we may want to extend this to OIDC with third party IDP's at a later point.

## Mechanics

### Version

A version is a set of manifest files with a version.yml file to describe the version.

The yml will have at the very least:
git sha, git branch, git committer, build no., date and time.

### Deployment status

Initially the server will keep track of which version it deployed the last and naively believe that it is the currently deployed version.
Eventually we are going to listen to the state from flux and/or k8s to give a more trustworthy answer.

## Client apps

There will be a few auxiliary CLI apps available to interact with the APIs:

1. **forge** (Version Builder)
   This is used in a CI pipeline, it can interact with the draft and publish APIs of smithd.
   It can also upload files to the pre-signed URL blob storage.
   And it may help generate and validate the version metadata file. Finally, it may eventually feature a command which can build k8s manifest files based on a simpler service yaml file, so the user does not need to know that much about k8s.

2. **smithctl** (Deployment Manager)
   smithctl is used by the developer to list available versions and initiate deployments (including rollbacks).
   It can also register new apps to be controlled by DeploySmith.
   Eventually it may also be possible to get feedback on the status of a given deployment.

## Tech notes

### Hosting

The application will be running in k8s hosted on Hetzner. We do have an AWS account available, which can also be utilised.

### Interface

The API should be a REST API over HTTPS.

### Code language

The code should be written in Golang.

### CI

The project will be built using Earthly in Github Actions. We will use gorelaser and Github Releases for publishing new versions.
It will publish Docker images for each of the binaries.

### Database

The server will need a database backend. For the MVP, we are going to use SQLite, but we will move to a better technology eventually. It should be configurable, so we are prepared for supporting multiple database backends.

### Version storage

We are going to use AWS S3 for version storage. The pre-signed URL's point into one place in a bucket (drafts). Once the version is published, it is moved into another place in the same bucket (published).
The drafts prefix in the bucket are going to be deleted automatically after a week. The published versions are staying forever.

### Gitops repo

The gitops repo structure looks like this:

gitops repo/
├── environments/
│ ├── staging/
│ │ ├── infrastructure/
│ │ │ ├── kustomization.yaml
│ │ │ ├── traefik/
│ │ │ ├── cert-manager/
│ │ │ └── namespaces/
│ │ └── apps/
│ │ ├── kustomization.yaml
│ │ ├── hello-world/
│ │ └── api-service/
│ └── production/
│ ├── infrastructure/
│ └── apps/
└── clusters/
├── staging/
│ ├── infrastructure.yaml # Points to environments/staging/infrastructure
│ └── apps.yaml # Points to environments/staging/apps
└── production/
├── infrastructure.yaml
└── apps.yaml

This means the manifest files should be written into:
`environments/${environment}/apps/${app_name}/`

## Clarifications

1. ~~Which blob storage can we use for this purpose?~~
   Options:

   - AWS S3 (easy pre-signed URLs, mature)
   - Hetzner Object Storage (cheaper, same S3-compatible API)
   - Minio (self-hosted, full control)
     We are going to use AWS S3 for a start to keep it simple.

2. ~~Can we revoke access to the pre-signed URL once the version has been published?~~
   Yes with S3/compatible storage - after publishing, you could delete the object and move it elsewhere, making the URL invalid.

3. ~~Should the uploaded manifests be moved somewhere else once the version is published?~~
   Yes, recommended approach is to move published versions to a different location (different bucket or prefix) to clearly separate draft vs published state.

4. ~~Which database technology should we use? Will Sqlite do?~~
   SQLite could work initially but consider:
   SQLite: Fine for single-instance, but problematic for HA
   PostgreSQL: Better for production, HA support
   Consider using Kubernetes CRDs as your "database" - native to k8s

5. ~~Can we think of a better name for this application and possible also for the auxilary apps?~~
   DeploySmith

   - Server: **smithd**
   - Version Builder: **forge**
   - Deployment Manager: **smithctl**

6. ~~How can we feed the status of the deployment back to the user?~~
   A) ~~Can we hook up to flux or k8s?~~
   Yes! Watch Flux Kustomization resources or Deployment status in k8s

   B) ~~Can be feedback to Slack?~~
   Easy to add after you have the event data

7. ~~Multi-environment strategy: How will different environments (dev/staging/prod) be modeled? Separate applications? Separate deployment targets? Different gitops repos?~~
   They will in different directories in the same gitops repo. Look at the **Gitops** section.

8. ~~Application lifecycle: How are applications registered/onboarded with the deployment controller? Is there a separate API for this?~~
   Yes, there will be an API and the CLI app intended for developer workstations will be able to interact with that API.

9. ~~Version retention: How long should versions be kept in blob storage? Should there be automatic cleanup policies?~~
   Yes, the drafted versions will only be kept for a few days, so they expire automatically in case they did not finish.
   Published versions are never expired.

10. ~~Manifest validation: Should the controller validate manifests before publishing versions? What about namespace restrictions or resource quotas?~~
    Yes, it should validate them. There are no restrictions on namespace or resources.

11. ~~Deployment windows: Should auto-deployment support maintenance windows or deployment schedules (e.g., only deploy during business hours)?~~
    No, that is not going to be needed.

12. ~~Audit trail: Beyond version metadata, should all deployment actions (manual triggers, auto-deploys, API calls) be logged/auditable?~~
    This might be needed down the line, but not right now.

13. ~~Flux dependencies: What's the failure mode if Flux is unavailable or the gitops repo is unreachable? Should deployments queue?~~
    If gitops repo is unavailable, we will fail hard and report that back to the user. If flux is unavailable, we can live with a silent failure.

14. ~~Versioning scheme: Does the system enforce any version numbering scheme (semver, sequential, etc.) or accept arbitrary version identifiers?~~
    It will accept arbitrary version identifiers. But forge (the version builder CLI) will be using the git sha (and pipeline no if available) for building a version ID, such as: "42540c4-123" where 123 is the pipeline no.

15. ~~Concurrent applications: Can the same smithd instance manage multiple applications? If so, how are they isolated?~~
    Yes, smithd is multi-tenant and manages multiple applications.

    **Isolation:** Applications work independently - each app's deployment operations don't affect other apps. Flux handles the reconciliation of each app's manifests independently.

    **Resource concerns:** Deployments could be put in an in-memory queue and handled one at a time, but this depends on complexity and cost of implementation (nice-to-have for MVP).

    **Storage organization:** S3 uses per-app prefixes:

    - Drafts: `s3://bucket/drafts/${app_name}/${version_id}/`
    - Published: `s3://bucket/published/${app_name}/${version_id}/`

16. ~~Webhook notifications: Beyond Slack (in #6), should the system support webhooks for deployment events?~~
    That is a great future idea - just like Slack, this is not in scope for the initial MVP.

17. ~~How are going to deliver this into our own cluster? Should we build it into this repo? Or consider this an open-source reusable repo and put it in a separate repo?~~
    Yes, we are going to keep this repo generic, so it can be used in other contexts at a later stage.

### Architecture & Design

1. ~~Version storage location - You mention "version storage" and "blob storage" separately. It seems like drafted versions go to blob storage, but where do published versions live? Same place or moved to the gitops repo immediately?~~
   Both go to the same S3 bucket, but in different places. Drafted versions are moved into "published" after they are published.

2. ~~Concurrency & race conditions - What happens if:~~
   ~~Two versions are published simultaneously with the same auto-deploy policy?~~
   ~~A manual deployment is triggered while an auto-deployment is in progress?~~
   ~~Multiple deployments are requested for the same app?~~
   For all of the cases, we accept that this will cause strange behaviour. If cheap and simple to implement, we could add a queue, so the deploys will be processed one by one, but this is out of scope for the MVP.

3. ~~Rollback semantics - Does rolling back mean deploying a previous version? How do you track "current" vs "deployed" state?~~
   Yes, rollbacks are simply deploys of previous versions. We will keep track of which version is deployed, so we can make it visible to the user that it is a rollback. No special logic is needed around rollbacks.

4. ~~Multi-environment support - The vision mentions "auto deploy master branch to prod" but doesn't discuss how multiple environments (dev/staging/prod) are handled. Do you have multiple gitops repos? Multiple directories in one repo?~~
   It has been clarified in the gitops section that there are multiple directories within the same gitops repo.

5. ~~Application registration - How does the controller know about applications? Is there an onboarding API?~~
   This has been clarified elsewhere. The user will be calling an API to register a new application. This can also be done using the CLI app.

### Security Considerations

1. ~~Pre-signed URL expiration - How long are pre-signed URLs valid? (relates to your clarification #2)~~
   5 minutes should be sufficient.

2. ~~API key scoping - Will API keys be scoped per application? Global admin keys?~~
   For a start the authentication will be really simple, so any API key will have access to anything. This is subject to change in the future.

3. ~~Malicious manifests - No validation mentioned for uploaded manifests. Could someone deploy arbitrary resources?~~
   The manifests should be validated to ensure that they are valid k8s manifests. Other than that, we will trust the user.

### Operational Concerns

1. ~~Failed deployments - What happens if Flux fails to apply manifests? How does the user know?~~
   Eventually we will build a notifier mechanism, which will subscribe to flux events to feed back the status to the user in Slack, the CLI app or using webhooks. But this is out of scope for a start.

2. ~~Partial uploads - If a CI pipeline crashes mid-upload, you have a drafted version with incomplete manifests~~
   Yes, and that is perfectly fine, because it will be deleted automatically by the bucket lifecycle policy.

3. ~~Storage cleanup - How/when do you clean up old versions from blob storage?~~
   This has been clarified elsewhere.

4. ~~Disaster recovery - If the database is lost, can you reconstruct state from the gitops repo?~~
   The gitops repo has all the manifests it needs to run the applications. We won't be able to reconstruct the database from that, but that is ok. We would just need to onboard every application and deploy a new version of it to get to a working state, which is acceptable for now.

## Out of scope MVP

As mentioned in the clarifications above, the following features are out of scope for the MVP:

1. **Deployment windows/schedules** - No support for maintenance windows or time-based deployment restrictions
2. **Audit trails** - Full audit logging of deployment actions (may be needed later)
3. **PostgreSQL support** - Starting with SQLite, but must prepare for it by making database engine configurable
4. **Deployment notifications** - Slack notifications, webhooks, or other event-based notifications (will be added after MVP)
5. **Flux/K8s status monitoring** - Real-time deployment status from Flux or Kubernetes (initially will track last deployed version naively)
6. **Deployment queuing** - In-memory queue for sequential deployment processing (nice-to-have depending on complexity)
7. **Per-app API key scoping** - All API keys have global access initially
8. **OIDC authentication** - Starting with simple API keys only
9. **Advanced manifest building** - CLI feature to generate k8s manifests from simpler YAML (forge will be basic)
