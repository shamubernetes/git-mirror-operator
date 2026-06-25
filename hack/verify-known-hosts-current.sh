#!/usr/bin/env bash
set -euo pipefail

config_file="${1:-config/default/known_hosts_configmap.yaml}"

require_command() {
	if ! command -v "$1" >/dev/null 2>&1; then
		echo "required command not found: $1" >&2
		exit 1
	fi
}

for command_name in awk curl diff python3 sort ssh-keygen ssh-keyscan; do
	require_command "$command_name"
done

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

actual_file="$tmpdir/actual"
expected_file="$tmpdir/expected"
codeberg_keys_file="$tmpdir/codeberg.keys"
codeberg_docs_fingerprints_file="$tmpdir/codeberg.docs.fingerprints"
codeberg_scanned_fingerprints_file="$tmpdir/codeberg.scanned.fingerprints"

normalize_known_hosts() {
	awk 'NF && $1 !~ /^#/ { print $1, $2, $3 }' | LC_ALL=C sort -u
}

extract_config_known_hosts() {
	awk '
		/^  known_hosts: \|/ { in_block = 1; next }
		in_block && /^    / { sub(/^    /, ""); print; next }
		in_block { exit }
	' "$config_file"
}

fetch_github_known_hosts() {
	curl -fsSL --retry 3 https://api.github.com/meta | python3 -c '
import json
import sys

for key in json.load(sys.stdin)["ssh_keys"]:
    print("github.com " + key)
'
}

fetch_gitlab_known_hosts() {
	curl -fsSL --retry 3 https://docs.gitlab.com/user/gitlab_com/ | python3 -c '
import html
import re
import sys

page = html.unescape(sys.stdin.read())
matches = re.findall(r"gitlab\.com (?:ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp256) [A-Za-z0-9+/=]+", page)
seen = []
for match in matches:
    if match not in seen:
        seen.append(match)

if len(seen) != 3:
    print(f"expected 3 GitLab.com known_hosts entries, found {len(seen)}", file=sys.stderr)
    sys.exit(1)

print("\n".join(seen))
'
}

fetch_bitbucket_known_hosts() {
	curl -fsSL --retry 3 https://bitbucket.org/site/ssh
}

fetch_codeberg_known_hosts() {
	ssh-keyscan -T 15 -t rsa,ecdsa,ed25519 codeberg.org 2>/dev/null | LC_ALL=C sort -u > "$codeberg_keys_file"

	curl -fsSL --retry 3 https://docs.codeberg.org/security/ssh-fingerprint/ \
		| python3 -c '
import re
import sys

fingerprints = sorted(set(re.findall(r"SHA256:[A-Za-z0-9+/=]+", sys.stdin.read())))
if len(fingerprints) != 3:
    print(f"expected 3 Codeberg.org SSH fingerprints, found {len(fingerprints)}", file=sys.stderr)
    sys.exit(1)

print("\n".join(fingerprints))
' > "$codeberg_docs_fingerprints_file"

	ssh-keygen -lf "$codeberg_keys_file" -E sha256 \
		| awk '{ print $2 }' \
		| LC_ALL=C sort -u > "$codeberg_scanned_fingerprints_file"

	if ! diff -u "$codeberg_docs_fingerprints_file" "$codeberg_scanned_fingerprints_file"; then
		echo "Codeberg.org ssh-keyscan fingerprints do not match Codeberg's published fingerprints." >&2
		exit 1
	fi

	cat "$codeberg_keys_file"
}

extract_config_known_hosts | normalize_known_hosts > "$actual_file"

{
	fetch_github_known_hosts
	fetch_gitlab_known_hosts
	fetch_bitbucket_known_hosts
	fetch_codeberg_known_hosts
} | normalize_known_hosts > "$expected_file"

ssh-keygen -lf "$actual_file" -E sha256 >/dev/null
ssh-keygen -lf "$expected_file" -E sha256 >/dev/null

if ! diff -u "$expected_file" "$actual_file"; then
	echo "Default known_hosts entries are stale. Update $config_file with the current upstream host keys before releasing." >&2
	exit 1
fi

echo "Default known_hosts entries match current upstream host keys."
