# Contributing

Thanks for helping improve `git-mirror-operator`.

## Development Checks

Run these before opening a pull request:

```bash
make lint
make test
make build build-sync
make check-generated-drift
```

For changes that touch deployment manifests, webhook behavior, Job scheduling, or container images, also run:

```bash
make test-e2e
```

The e2e suite creates and deletes an ephemeral Kind cluster. It does not deploy to a real cluster.

## Generated Files

After API or marker changes, regenerate code and manifests:

```bash
make generate manifests
```

Commit generated CRDs, RBAC, and deepcopy files with the source change that required them.

## Images

Runtime images are published to GHCR by the `Release Images` workflow from semantic version tags after lint, unit/envtest, and e2e checks pass. Local image builds are available with:

```bash
make docker-build IMG=ghcr.io/shamubernetes/git-mirror-operator:dev
make docker-build-sync SYNC_IMG=ghcr.io/shamubernetes/git-mirror-sync:dev
```

## Pull Requests

Keep PRs focused. Include the behavior change, relevant tests, and any manifest or documentation updates needed by GitOps users.
