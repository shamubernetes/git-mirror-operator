# git-mirror-operator

`git-mirror-operator` receives signed GitHub push webhooks and creates short-lived Kubernetes Jobs that mirror a configured source Git repository to a target repository such as Codeberg.

This is an Operator SDK Go project using the Kubebuilder/controller-runtime layout.

## Architecture

- The manager runs as a controller-runtime operator.
- It exposes `POST /webhooks/github`, `GET /healthz`, and `GET /readyz`.
- Push webhooks are matched to `GitMirror` resources by `repository.full_name`.
- The matching resource's webhook secret is loaded before verifying `X-Hub-Signature-256`.
- The webhook records sync intent in `GitMirror.status`; the reconciler owns sync Job creation.
- A per-repository `coordination.k8s.io/v1` Lease prevents concurrent sync Job creation across reconciler instances.
- A Kubernetes Job runs the sync runner image with source and target credentials loaded from Secrets.
- Owned Jobs are watched by controller-runtime and update `GitMirror.status`.
- `spec.fallback.schedule` can trigger scheduled catch-up syncs with the same active-job coalescing rules.

## API

API group: `mirror.shamubernetes.com/v1alpha1`

Kind: `GitMirror`

Important spec fields:

- `provider`: currently `github`
- `github.owner`, `github.repo`
- `github.webhookSecretRef.name`, `github.webhookSecretRef.key`
- `source.url`, `source.auth` or legacy `source.sshSecretRef`
- `target.url`, `target.auth` or legacy `target.sshSecretRef`
- `mirror.mode`: `exact` or `additive`
- `mirror.includeTags`: additive mode only; exact mode always mirrors tags
- `fallback.schedule`: optional cron expression
- `job.image`, `job.backoffLimit`, `job.activeDeadlineSeconds`, `job.ttlSecondsAfterFinished`, `job.resources`

CRDs and RBAC are generated from Kubebuilder markers:

```bash
make generate manifests
```

## Webhook Setup

Create a GitHub webhook pointing at:

```text
https://<your-host>/webhooks/github
```

Use content type `application/json`, enable push events, and configure the same secret in the `github.webhookSecretRef` Kubernetes Secret.
Placeholder webhook and auth Secret manifests are included in `config/samples/mirror_v1alpha1_secrets.yaml`.

Ping events are accepted without repository lookup. Unsupported GitHub event types are rejected. Push events require:

- `X-GitHub-Event: push`
- `X-GitHub-Delivery`
- `X-Hub-Signature-256`

## Sync Job Prerequisites

The default install creates the `git-mirror-sync` ServiceAccount used by sync Jobs without extra RBAC permissions. It also creates a `git-mirror-known-hosts` ConfigMap with verified `known_hosts` entries for GitHub.com, GitLab.com, Bitbucket Cloud, and Codeberg.org. Extend or override that ConfigMap in your GitOps overlay for any additional Git hosts you mirror.

## Sync Runner

Sync Jobs run as non-root. SSH key Secrets are mounted read-only and readable by that UID; before invoking git, the runner copies each key into a private `/tmp/git-mirror-credentials` directory, chmods the copy to `0400`, and points `GIT_SSH_COMMAND` at the copied key. HTTPS credentials are supplied to git through a private `GIT_ASKPASS` helper so tokens do not need to appear in repository URLs or command logs.

## Git Authentication

Each endpoint can choose its own auth method. Existing manifests that use `sshSecretRef` still work, but new manifests should prefer `auth`.

SSH deploy key:

```yaml
source:
  url: git@github.com:example/source-repo.git
  auth:
    type: ssh
    ssh:
      privateKeyRef:
        name: source-repo-source-ssh
        key: ssh-privatekey
```

Generic HTTPS username/token auth:

```yaml
target:
  url: https://codeberg.org/example/source-repo.git
  auth:
    type: basic
    basic:
      usernameSecretRef:
        name: source-repo-target-basic
        key: username
      passwordSecretRef:
        name: source-repo-target-basic
        key: password
```

Use `auth.type=basic` for hosters that expose Git over HTTPS with a username plus token or password credential: GitHub personal access tokens, GitLab personal/project/group access tokens or deploy tokens, Bitbucket Cloud API tokens, and Codeberg/Forgejo access tokens.

GitHub App installation token:

```yaml
source:
  url: https://github.com/example/source-repo.git
  auth:
    type: githubApp
    githubApp:
      appIDSecretRef:
        name: source-repo-github-app
        key: app-id
      installationIDSecretRef:
        name: source-repo-github-app
        key: installation-id
      privateKeySecretRef:
        name: source-repo-github-app
        key: private-key.pem
```

`githubApp.appIDSecretRef` may contain either the GitHub App client ID or app ID. The sync runner creates a short-lived installation access token inside the Job, uses `x-access-token` as the HTTPS username, and uses the installation token as the HTTPS password. For GitHub Enterprise Server, set `githubApp.apiURL` to the server REST API base URL.

Exact mode mirrors branches and tags, and prunes target branch and tag refs that no longer exist at the source. It intentionally ignores provider-internal refs such as GitHub `refs/pull/*`, GitLab `refs/merge-requests/*`, Gerrit `refs/changes/*`, and any other non-branch/tag refs from the source clone. `mirror.includeTags` does not apply in exact mode.

This makes GitHub to Codeberg/Forgejo mirroring safe because provider-generated refs are not pushed to hidden or protected ref namespaces on the target.

```bash
git clone --bare "$SOURCE_URL" /tmp/repo.git
git -C /tmp/repo.git push --prune "$TARGET_URL" '+refs/heads/*:refs/heads/*'
git -C /tmp/repo.git push --prune "$TARGET_URL" '+refs/tags/*:refs/tags/*'
```

Additive mode pushes branches and, by default, tags without pruning. Like exact mode, it only pushes normal branch and tag refs and ignores provider-internal refs.

With tags:

```bash
git clone --bare "$SOURCE_URL" /tmp/repo.git
git -C /tmp/repo.git push "$TARGET_URL" 'refs/heads/*:refs/heads/*'
git -C /tmp/repo.git push "$TARGET_URL" 'refs/tags/*:refs/tags/*'
```

Without tags:

```bash
git clone --bare "$SOURCE_URL" /tmp/repo.git
git -C /tmp/repo.git push "$TARGET_URL" 'refs/heads/*:refs/heads/*'
```

## Status Fields

- `status.lastRequestedRevision`: latest GitHub push revision accepted by the webhook.
- `status.lastMirroredRevision`: latest requested revision whose sync Job completed successfully.
- `status.lastCompletedJobName`: latest finished Job reflected into status.
- `status.pendingResync`: true when a push arrived while another sync Job was already active.

## Images

The `Release Images` GitHub Actions workflow publishes multi-architecture `linux/amd64` and `linux/arm64` images to GHCR from semantic version tags after lint, unit/envtest, and e2e checks pass:

- `ghcr.io/shamubernetes/git-mirror-operator`
- `ghcr.io/shamubernetes/git-mirror-sync`

Published tags include the release tag, semver aliases like `0.1` for `v0.1.0`, `sha-<commit>`, and `latest`.

The default kustomize install points the manager at `ghcr.io/shamubernetes/git-mirror-operator:latest` and configures Jobs to use `ghcr.io/shamubernetes/git-mirror-sync:latest`. Pin those image tags or digests in your GitOps overlay for production.

## Development

Run the SDK test path, including generated manifests and envtest:

```bash
make test
```

Build binaries:

```bash
make build
make build-sync
```

Build images:

```bash
make docker-build IMG=ghcr.io/shamubernetes/git-mirror-operator:dev
make docker-build-sync SYNC_IMG=ghcr.io/shamubernetes/git-mirror-sync:dev
```

Run the manager against the current kubeconfig:

```bash
SYNC_IMAGE=ghcr.io/shamubernetes/git-mirror-sync:dev make run
```

Send a locally signed webhook:

```bash
GITHUB_WEBHOOK_SECRET=replace-me ./scripts/send-signed-webhook.sh http://localhost:8082/webhooks/github
```

The generated SDK e2e suite under `test/e2e` requires `kind`; it is intentionally excluded from `make test`.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development checks and pull request expectations.

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
