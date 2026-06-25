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

forbid_env() {
	name="$1"
	eval "is_set=\${$name+x}"
	if [ "$is_set" = "x" ]; then
		echo "$name: expected unset, got set" >&2
		exit 1
	fi
}

require_file_contains() {
	path="$1"
	want="$2"
	if ! grep -q "$want" "$path"; then
		echo "expected $path to contain '$want'" >&2
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

require_env MIRROR_MODE "exact"
require_env KNOWN_HOSTS_PATH "/var/run/git-mirror/known-hosts/known_hosts"
require_env HOME "/tmp"

require_path "$KNOWN_HOSTS_PATH"
require_dir "$HOME"

case "${SOURCE_AUTH_TYPE:-}:${TARGET_AUTH_TYPE:-}" in
	ssh:ssh)
		require_env SOURCE_URL "git@github.com:example/source-repo.git"
		require_env TARGET_URL "git@codeberg.org:example/source-repo.git"
		require_env SOURCE_SSH_KEY_PATH "/var/run/git-mirror/source/ssh_key"
		require_env TARGET_SSH_KEY_PATH "/var/run/git-mirror/target/ssh_key"
		require_path "$SOURCE_SSH_KEY_PATH"
		require_path "$TARGET_SSH_KEY_PATH"
		require_readable "$SOURCE_SSH_KEY_PATH"
		require_readable "$TARGET_SSH_KEY_PATH"
		;;
	githubApp:basic)
		require_env SOURCE_URL "https://github.com/example/source-repo-modern.git"
		require_env TARGET_URL "https://codeberg.org/example/source-repo-modern.git"
		require_env SOURCE_GITHUB_APP_ID "12345"
		require_env SOURCE_GITHUB_APP_INSTALLATION_ID "67890"
		require_env SOURCE_GITHUB_APP_API_URL "https://github.example.com/api/v3"
		require_env SOURCE_GITHUB_APP_PRIVATE_KEY_PATH "/var/run/git-mirror/source-github-app/private_key"
		require_env TARGET_AUTH_USERNAME "mirror-user"
		require_env TARGET_AUTH_PASSWORD "mirror-token"
		forbid_env SOURCE_SSH_KEY_PATH
		forbid_env TARGET_SSH_KEY_PATH
		require_path "$SOURCE_GITHUB_APP_PRIVATE_KEY_PATH"
		require_readable "$SOURCE_GITHUB_APP_PRIVATE_KEY_PATH"
		require_file_contains "$SOURCE_GITHUB_APP_PRIVATE_KEY_PATH" "dummy-github-app-private-key"
		;;
	*)
		echo "unexpected auth contract: SOURCE_AUTH_TYPE='${SOURCE_AUTH_TYPE:-}' TARGET_AUTH_TYPE='${TARGET_AUTH_TYPE:-}'" >&2
		exit 1
		;;
esac

echo "sync contract ok"
