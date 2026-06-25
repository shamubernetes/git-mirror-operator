package syncenv

const (
	AuthTypeSSH       = "ssh"
	AuthTypeBasic     = "basic"
	AuthTypeGitHubApp = "githubApp"
)

const (
	SourcePrefix = "SOURCE"
	TargetPrefix = "TARGET"
)

const (
	SourceURL      = "SOURCE_URL"
	TargetURL      = "TARGET_URL"
	MirrorMode     = "MIRROR_MODE"
	IncludeTags    = "INCLUDE_TAGS"
	KnownHostsPath = "KNOWN_HOSTS_PATH"
	Home           = "HOME"
)

const (
	SuffixAuthType                = "AUTH_TYPE"
	SuffixSSHKeyPath              = "SSH_KEY_PATH"
	SuffixAuthUsername            = "AUTH_USERNAME"
	SuffixAuthPassword            = "AUTH_PASSWORD"
	SuffixGitHubAppID             = "GITHUB_APP_ID"
	SuffixGitHubAppInstallationID = "GITHUB_APP_INSTALLATION_ID"
	SuffixGitHubAppPrivateKeyPath = "GITHUB_APP_PRIVATE_KEY_PATH"
	SuffixGitHubAppAPIURL         = "GITHUB_APP_API_URL"
)

func Endpoint(prefix, suffix string) string {
	return prefix + "_" + suffix
}
