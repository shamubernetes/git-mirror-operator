#!/usr/bin/env bash
set -euo pipefail

chart_dir="${1:-charts/git-mirror-operator}"
release_name="git-mirror-operator"
namespace="git-mirror-operator-system"

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

default_render="$tmpdir/default.yaml"
digest_render="$tmpdir/digest.yaml"
no_webhook_render="$tmpdir/no-webhook.yaml"
config_known_hosts="$tmpdir/config-known-hosts"
chart_known_hosts="$tmpdir/chart-known-hosts"
app_version="$(helm show chart "$chart_dir" | awk '$1 == "appVersion:" { print $2 }' | tr -d '"')"

if [[ -z "$app_version" ]]; then
  echo "unable to determine chart appVersion" >&2
  exit 1
fi

helm lint "$chart_dir"
helm template "$release_name" "$chart_dir" --namespace "$namespace" > "$default_render"
helm template "$release_name" "$chart_dir" --namespace "$namespace" \
  --set manager.image.digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  --set sync.image.digest=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb \
  > "$digest_render"
helm template "$release_name" "$chart_dir" --namespace "$namespace" \
  --set webhook.enabled=false > "$no_webhook_render"

require_rendered() {
  local pattern="$1"
  local file="$2"
  if ! grep -Eq "$pattern" "$file"; then
    printf 'missing rendered pattern %s in %s\n' "$pattern" "$file" >&2
    exit 1
  fi
}

require_rendered '^kind: CustomResourceDefinition$' "$default_render"
require_rendered '^kind: Deployment$' "$default_render"
require_rendered '^kind: ServiceAccount$' "$default_render"
require_rendered '^[[:space:]]+name: git-mirror-operator-controller-manager$' "$default_render"
require_rendered '^[[:space:]]+name: git-mirror-sync$' "$default_render"
require_rendered '^kind: ConfigMap$' "$default_render"
require_rendered '^[[:space:]]+name: git-mirror-known-hosts$' "$default_render"
require_rendered "ghcr.io/shamubernetes/git-mirror-operator:${app_version}" "$default_render"
require_rendered "ghcr.io/shamubernetes/git-mirror-sync:${app_version}" "$default_render"
require_rendered 'ghcr.io/shamubernetes/git-mirror-operator@sha256:aaaaaaaa' "$digest_render"
require_rendered 'ghcr.io/shamubernetes/git-mirror-sync@sha256:bbbbbbbb' "$digest_render"

if grep -q 'github-webhook-service' "$no_webhook_render"; then
  echo 'webhook Service rendered while webhook.enabled=false' >&2
  exit 1
fi

grep -E '^[[:space:]]+(github\.com|gitlab\.com|bitbucket\.org|codeberg\.org) ' \
  config/default/known_hosts_configmap.yaml | sed 's/^[[:space:]]*//' | sort -u > "$config_known_hosts"
grep -E '^[[:space:]]+(github\.com|gitlab\.com|bitbucket\.org|codeberg\.org) ' \
  "$default_render" | sed 's/^[[:space:]]*//' | sort -u > "$chart_known_hosts"
diff -u "$config_known_hosts" "$chart_known_hosts"

printf 'Helm chart rendered and validated successfully.\n'
