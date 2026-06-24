#!/usr/bin/env bash
set -euo pipefail

url="${1:-http://localhost:8082/webhooks/github}"
secret="${GITHUB_WEBHOOK_SECRET:?set GITHUB_WEBHOOK_SECRET}"
payload="${PAYLOAD:-{\"repository\":{\"full_name\":\"example/source-repo\"},\"after\":\"abc123\"}}"
delivery="${GITHUB_DELIVERY_ID:-local-$(date +%s)}"
signature="$(printf '%s' "$payload" | openssl dgst -sha256 -hmac "$secret" -hex | awk '{print "sha256="$2}')"

curl -i -X POST "$url" \
  -H "Content-Type: application/json" \
  -H "X-GitHub-Event: push" \
  -H "X-GitHub-Delivery: $delivery" \
  -H "X-Hub-Signature-256: $signature" \
  --data "$payload"
