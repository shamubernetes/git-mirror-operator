#!/bin/sh
set -eu

require_env() {
	name="$1"
	want="$2"
	eval "got=\${$name:-}"
	if [ "$got" != "$want" ]; then
		echo "$name: expected '$want', got '$got'" >&2
		exit 1
	fi
}

require_path() {
	path="$1"
	if [ ! -f "$path" ]; then
		echo "expected mounted file at $path" >&2
		exit 1
	fi
}

require_readable() {
	path="$1"
	if [ ! -r "$path" ]; then
		echo "expected readable file at $path" >&2
		exit 1
	fi
}

require_dir() {
	path="$1"
	if [ ! -d "$path" ]; then
		echo "expected directory at $path" >&2
		exit 1
	fi
}

require_env SOURCE_URL "git@github.com:example/source-repo.git"
require_env TARGET_URL "git@codeberg.org:example/source-repo.git"
require_env MIRROR_MODE "exact"
require_env SOURCE_SSH_KEY_PATH "/var/run/git-mirror/source/ssh_key"
require_env TARGET_SSH_KEY_PATH "/var/run/git-mirror/target/ssh_key"
require_env KNOWN_HOSTS_PATH "/var/run/git-mirror/known-hosts/known_hosts"
require_env HOME "/tmp"

require_path "$SOURCE_SSH_KEY_PATH"
require_path "$TARGET_SSH_KEY_PATH"
require_path "$KNOWN_HOSTS_PATH"
require_readable "$SOURCE_SSH_KEY_PATH"
require_readable "$TARGET_SSH_KEY_PATH"
require_dir "$HOME"

echo "sync contract ok"
