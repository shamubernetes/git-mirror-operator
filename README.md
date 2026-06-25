# git-mirror-operator

`git-mirror-operator` receives signed GitHub push webhooks and creates short-lived Kubernetes Jobs that mirror a configured source Git repository to a target repository such as Codeberg.

This is an Operator SDK Go project using the Kubebuilder/controller-runtime layout.

## Architecture

- The manager runs as a controller-runtime operator.
- It exposes `POST /webhooks/github`, `GET /healthz`, and `GET /readyz`.
- Push webhooks are matched to `GitMirror` resources by `repository.full_name`.
- The matching resource's webhook secret is loaded before verifying `X-Hub-Signature-256`.
- A Kubernetes Job runs the sync runner image with source and target SSH keys mounted from Secrets.
- Owned Jobs are watched by controller-runtime and update `GitMirror.status`.
- `spec.fallback.schedule` can trigger scheduled catch-up syncs with the same active-job coalescing rules.

## API

API group: `mirror.maude.dev/v1alpha1`

Kind: `GitMirror`

Important spec fields:

- `provider`: currently `github`
- `github.owner`, `github.repo`
- `github.webhookSecretRef.name`, `github.webhookSecretRef.key`
- `source.url`, `source.sshSecretRef.name`, `source.sshSecretRef.key`
- `target.url`, `target.sshSecretRef.name`, `target.sshSecretRef.key`
- `mirror.mode`: `exact` or `additive`
- `mirror.includeTags`
- `mirror.prune`
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

Ping events are accepted without repository lookup. Push events require:

- `X-GitHub-Event: push`
- `X-GitHub-Delivery`
- `X-Hub-Signature-256`

## Sync Job Prerequisites

The default install creates the `git-mirror-sync` ServiceAccount used by sync Jobs without extra RBAC permissions. It also creates a `git-mirror-known-hosts` ConfigMap template with the `known_hosts` key expected by the sync runner. Populate that ConfigMap with verified SSH host keys for the Git hosts you mirror before running sync Jobs.

## Sync Runner

Exact mode mirrors all refs and uses:

```bash
git clone --mirror "$SOURCE_URL" /tmp/repo.git
git -C /tmp/repo.git push --mirror "$TARGET_URL"
```

Additive mode preserves target refs that no longer exist at the source.

With tags:

```bash
git clone --mirror "$SOURCE_URL" /tmp/repo.git
git -C /tmp/repo.git push "$TARGET_URL" 'refs/heads/*:refs/heads/*' 'refs/tags/*:refs/tags/*'
```

Without tags:

```bash
git clone --mirror "$SOURCE_URL" /tmp/repo.git
git -C /tmp/repo.git push "$TARGET_URL" 'refs/heads/*:refs/heads/*'
```

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
