## Summary

<!-- What changed and why? -->

## Verification

<!-- List commands run and any relevant results. -->

- [ ] `make lint`
- [ ] `make test`
- [ ] `make build build-sync`
- [ ] `make check-generated-drift`
- [ ] `make test-e2e` when deployment, webhook, controller, Job, or image behavior changed

## GitOps Impact

- [ ] CRDs/RBAC/manifests are updated when needed
- [ ] Image tags, defaults, or required Secrets/ConfigMaps are documented when changed
- [ ] No manual cluster apply is required for this change
