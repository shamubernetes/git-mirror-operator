# git-mirror-operator

`git-mirror-operator` receives signed GitHub push webhooks and creates short-lived Kubernetes Jobs that mirror a configured source Git repository to a target repository such as Codeberg.

## Architecture

- The manager runs as a Kubernetes Deployment.
- It exposes `POST /webhooks/github`, `GET /healthz`, and `GET /readyz`.
- Push webhooks are matched to `GitMirror` resources by `repository.full_name`.
- The matching resource's webhook secret is loaded before verifying `X-Hub-Signature-256`.
- A Kubernetes Job runs the sync runner image with source and target SSH keys mounted from Secrets.
- Owned Jobs are watched by controller-runtime and update `GitMirror.status`.
- `spec.fallback.schedule` can trigger scheduled catch-up syncs with the same active-job coalescing rules.

## CRD

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

Status records delivery IDs, trigger times, last Job, success/failure timestamps, last error, last mirrored revision, pending resync state, and conditions.

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

## Secrets

Apply or adapt the placeholders in [config/samples/secrets.placeholder.yaml](/Users/kilian/git/shamubernetes/git-mirror-operator/config/samples/secrets.placeholder.yaml).

The source and target SSH Secrets should use the key named by `sshSecretRef.key`, usually `ssh-privatekey`.

The sync Job mounts:

- source key at `/var/run/git-mirror/source/ssh_key`
- target key at `/var/run/git-mirror/target/ssh_key`
- known hosts at `/var/run/git-mirror/known-hosts/known_hosts`

Populate [config/manager/known_hosts_configmap.yaml](/Users/kilian/git/shamubernetes/git-mirror-operator/config/manager/known_hosts_configmap.yaml) with current host keys:

```bash
ssh-keyscan github.com codeberg.org
```

## GitHub To Codeberg Example

```bash
kubectl apply -f config/manager/namespace.yaml
kubectl apply -f config/crd/mirror.maude.dev_gitmirrors.yaml
kubectl apply -f config/rbac/service_account.yaml
kubectl apply -f config/rbac/role.yaml
kubectl apply -f config/manager/known_hosts_configmap.yaml
kubectl apply -f config/manager/deployment.yaml
kubectl apply -f config/manager/service.yaml
kubectl apply -f config/samples/secrets.placeholder.yaml
kubectl apply -f config/samples/gitmirror.yaml
```

Edit the sample before applying it to use real repository URLs, webhook secret, SSH deploy keys, and images.

## Exact Vs Additive Mode

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

## Fallback Schedules

Set `spec.fallback.schedule` to a standard five-field cron expression:

```yaml
fallback:
  schedule: "0 */6 * * *"
```

If a sync Job is already active, scheduled and webhook triggers set `status.pendingResync=true`. When the active Job finishes, the controller starts one follow-up Job.

## Local Validation

Run:

```bash
make test
make vet
go test ./...
```

Run the manager against your current kubeconfig:

```bash
make run-local
```

Send a locally signed webhook:

```bash
GITHUB_WEBHOOK_SECRET=replace-me ./scripts/send-signed-webhook.sh http://localhost:8082/webhooks/github
```

Build images:

```bash
make docker-build IMG=ghcr.io/shamubernetes/git-mirror-operator:dev
make docker-build-sync SYNC_IMG=ghcr.io/shamubernetes/git-mirror-sync:dev
```
