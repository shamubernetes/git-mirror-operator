package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shamubernetes/git-mirror-operator/internal/syncenv"
)

const defaultGitHubAPIURL = "https://api.github.com"
const githubAppTokenRequestTimeout = 30 * time.Second
const preparedCredentialDirName = "git-mirror-credentials"
const defaultMirrorRepoPath = "/tmp/repo.git"
const headsRefSpec = "refs/heads/*:refs/heads/*"
const tagsRefSpec = "refs/tags/*:refs/tags/*"
const forceHeadsRefSpec = "+" + headsRefSpec
const forceTagsRefSpec = "+" + tagsRefSpec

var githubAppHTTPClient = &http.Client{Timeout: githubAppTokenRequestTimeout}

type Config struct {
	SourceURL      string
	TargetURL      string
	MirrorMode     string
	IncludeTags    bool
	SourceAuth     EndpointAuth
	TargetAuth     EndpointAuth
	KnownHostsPath string
	RepoPath       string
}

type EndpointAuth struct {
	Type                    string
	SSHKeyPath              string
	Username                string
	Password                string
	GitHubAppID             string
	GitHubAppInstallationID string
	GitHubAppPrivateKeyPath string
	GitHubAppAPIURL         string
}

func main() {
	cfg := ConfigFromEnv()
	if err := Run(cfg); err != nil {
		log.Fatalf("sync failed: %v", err)
	}
}

func ConfigFromEnv() Config {
	return Config{
		SourceURL:      os.Getenv(syncenv.SourceURL),
		TargetURL:      os.Getenv(syncenv.TargetURL),
		MirrorMode:     envDefault(syncenv.MirrorMode, "exact"),
		IncludeTags:    envBoolDefault(syncenv.IncludeTags, true),
		SourceAuth:     endpointAuthFromEnv(syncenv.SourcePrefix),
		TargetAuth:     endpointAuthFromEnv(syncenv.TargetPrefix),
		KnownHostsPath: os.Getenv(syncenv.KnownHostsPath),
	}
}

func Run(cfg Config) error {
	preparedCfg, err := prepareCredentials(cfg, preparedCredentialDir())
	if err != nil {
		return err
	}
	cfg = preparedCfg
	if err := validateEndpointAuth("source", cfg.SourceURL, cfg.SourceAuth); err != nil {
		return err
	}
	if err := validateEndpointAuth("target", cfg.TargetURL, cfg.TargetAuth); err != nil {
		return err
	}
	commands, err := BuildGitCommands(cfg)
	if err != nil {
		return err
	}
	for _, args := range commands {
		authEnv, err := gitAuthEnv(cfg, authForCommand(cfg, args))
		if err != nil {
			return err
		}
		skip, err := shouldSkipEmptyTagPush(args, authEnv)
		if err != nil {
			return err
		}
		if skip {
			log.Printf("skipping %s: source and target have no tags", safeCommand(args))
			continue
		}
		log.Printf("running %s", safeCommand(args))
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = append(os.Environ(), authEnv...)
		if err := cmd.Run(); err != nil {
			return err
		}
	}
	return nil
}

func BuildGitCommands(cfg Config) ([][]string, error) {
	if cfg.SourceURL == "" {
		return nil, errors.New("SOURCE_URL is required")
	}
	if cfg.TargetURL == "" {
		return nil, errors.New("TARGET_URL is required")
	}
	mode := cfg.MirrorMode
	if mode == "" {
		mode = "exact"
	}
	repoPath := mirrorRepoPath(cfg)
	commands := [][]string{
		{"git", "clone", "--bare", cfg.SourceURL, repoPath},
	}
	switch mode {
	case "exact":
		commands = append(commands,
			[]string{"git", "-C", repoPath, "push", "--prune", cfg.TargetURL, forceHeadsRefSpec},
			[]string{"git", "-C", repoPath, "push", "--prune", cfg.TargetURL, forceTagsRefSpec},
		)
	case "additive":
		commands = append(commands, []string{"git", "-C", repoPath, "push", cfg.TargetURL, headsRefSpec})
		if cfg.IncludeTags {
			commands = append(commands, []string{"git", "-C", repoPath, "push", cfg.TargetURL, tagsRefSpec})
		}
	default:
		return nil, fmt.Errorf("unsupported MIRROR_MODE %q", mode)
	}
	return commands, nil
}

func mirrorRepoPath(cfg Config) string {
	if cfg.RepoPath != "" {
		return cfg.RepoPath
	}
	return defaultMirrorRepoPath
}

func shouldSkipEmptyTagPush(args []string, env []string) (bool, error) {
	if !isTagWildcardPush(args) {
		return false, nil
	}
	repoPath, ok := commandRepoPath(args)
	if !ok {
		return false, nil
	}
	hasSourceTags, err := hasLocalRefs(repoPath, "refs/tags")
	if err != nil {
		return false, err
	}
	if hasSourceTags {
		return false, nil
	}
	targetURL, ok := pushTargetURL(args)
	if !ok {
		return false, nil
	}
	hasTargetTags, err := hasRemoteTags(targetURL, env)
	if err != nil {
		return false, err
	}
	return !hasTargetTags, nil
}

func isTagWildcardPush(args []string) bool {
	if len(args) == 0 {
		return false
	}
	hasPush := false
	hasTagRefSpec := false
	for _, arg := range args {
		if arg == "push" {
			hasPush = true
		}
		if arg == tagsRefSpec || arg == forceTagsRefSpec {
			hasTagRefSpec = true
		}
	}
	return hasPush && hasTagRefSpec
}

func commandRepoPath(args []string) (string, bool) {
	for i, arg := range args {
		if arg == "-C" && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func pushTargetURL(args []string) (string, bool) {
	for i, arg := range args {
		if arg != "push" {
			continue
		}
		for _, candidate := range args[i+1:] {
			if strings.HasPrefix(candidate, "-") {
				continue
			}
			return candidate, true
		}
	}
	return "", false
}

func hasLocalRefs(repoPath, prefix string) (bool, error) {
	cmd := exec.Command("git", "-C", repoPath, "for-each-ref", "--format=%(refname)", "--count=1", prefix)
	output, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(output)) != "", nil
}

func hasRemoteTags(targetURL string, env []string) (bool, error) {
	cmd := exec.Command("git", "ls-remote", "--tags", "--refs", targetURL)
	cmd.Env = append(os.Environ(), env...)
	output, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(output)) != "", nil
}

func endpointAuthFromEnv(prefix string) EndpointAuth {
	authType := os.Getenv(syncenv.Endpoint(prefix, syncenv.SuffixAuthType))
	if authType == "" && os.Getenv(syncenv.Endpoint(prefix, syncenv.SuffixSSHKeyPath)) != "" {
		authType = syncenv.AuthTypeSSH
	}
	return EndpointAuth{
		Type:                    authType,
		SSHKeyPath:              os.Getenv(syncenv.Endpoint(prefix, syncenv.SuffixSSHKeyPath)),
		Username:                os.Getenv(syncenv.Endpoint(prefix, syncenv.SuffixAuthUsername)),
		Password:                os.Getenv(syncenv.Endpoint(prefix, syncenv.SuffixAuthPassword)),
		GitHubAppID:             os.Getenv(syncenv.Endpoint(prefix, syncenv.SuffixGitHubAppID)),
		GitHubAppInstallationID: os.Getenv(syncenv.Endpoint(prefix, syncenv.SuffixGitHubAppInstallationID)),
		GitHubAppPrivateKeyPath: os.Getenv(syncenv.Endpoint(prefix, syncenv.SuffixGitHubAppPrivateKeyPath)),
		GitHubAppAPIURL:         envDefault(syncenv.Endpoint(prefix, syncenv.SuffixGitHubAppAPIURL), defaultGitHubAPIURL),
	}
}

func prepareCredentials(cfg Config, dir string) (Config, error) {
	var err error
	cfg.SourceAuth, err = prepareEndpointAuth("source", cfg.SourceAuth, filepath.Join(dir, "source"))
	if err != nil {
		return cfg, err
	}
	cfg.TargetAuth, err = prepareEndpointAuth("target", cfg.TargetAuth, filepath.Join(dir, "target"))
	if err != nil {
		return cfg, err
	}
	return cfg, nil
}

func prepareEndpointAuth(name string, auth EndpointAuth, dir string) (EndpointAuth, error) {
	switch auth.Type {
	case "":
		return auth, nil
	case syncenv.AuthTypeSSH:
		if auth.SSHKeyPath == "" {
			return auth, fmt.Errorf("%s SSH auth requires an SSH key path", name)
		}
		keyPath, err := copyPrivateFile(auth.SSHKeyPath, filepath.Join(dir, "ssh_key"))
		if err != nil {
			return auth, fmt.Errorf("prepare %s SSH key: %w", name, err)
		}
		auth.SSHKeyPath = keyPath
	case syncenv.AuthTypeGitHubApp:
		token, err := createGitHubAppInstallationToken(auth, githubAppHTTPClient, time.Now)
		if err != nil {
			return auth, fmt.Errorf("create %s GitHub App installation token: %w", name, err)
		}
		auth.Username = "x-access-token"
		auth.Password = token
	case syncenv.AuthTypeBasic:
		// Basic credentials are already supplied by environment variables.
	default:
		return auth, fmt.Errorf("unsupported %s auth type %q", name, auth.Type)
	}
	return auth, nil
}

func copyPrivateFile(src, dst string) (string, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return "", err
	}
	tmp := dst + ".tmp"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0400); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return dst, nil
}

func validateEndpointAuth(name, rawURL string, auth EndpointAuth) error {
	switch auth.Type {
	case syncenv.AuthTypeSSH:
		if auth.SSHKeyPath == "" {
			return fmt.Errorf("%s SSH auth requires an SSH key path", name)
		}
	case syncenv.AuthTypeBasic, syncenv.AuthTypeGitHubApp:
		if !isHTTPSURL(rawURL) {
			return fmt.Errorf("%s %s auth requires an HTTPS git URL", name, auth.Type)
		}
		if auth.Username == "" || auth.Password == "" {
			return fmt.Errorf("%s %s auth requires username and password credentials", name, auth.Type)
		}
	case "":
		return fmt.Errorf("%s auth is required", name)
	default:
		return fmt.Errorf("unsupported %s auth type %q", name, auth.Type)
	}
	return nil
}

func isHTTPSURL(rawURL string) bool {
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https"
}

func gitAuthEnv(cfg Config, auth EndpointAuth) ([]string, error) {
	switch auth.Type {
	case syncenv.AuthTypeSSH:
		return []string{gitSSHCommand(cfg, auth.SSHKeyPath)}, nil
	case syncenv.AuthTypeBasic, syncenv.AuthTypeGitHubApp:
		askpassPath, err := ensureAskPassScript(preparedCredentialDir())
		if err != nil {
			return nil, err
		}
		return []string{
			"GIT_ASKPASS=" + askpassPath,
			"GIT_TERMINAL_PROMPT=0",
			"GIT_MIRROR_USERNAME=" + auth.Username,
			"GIT_MIRROR_PASSWORD=" + auth.Password,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported auth type %q", auth.Type)
	}
}

func preparedCredentialDir() string {
	return filepath.Join(os.TempDir(), preparedCredentialDirName)
}

func ensureAskPassScript(dir string) (string, error) {
	path := filepath.Join(dir, "askpass.sh")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	script := `#!/bin/sh
case "$1" in
*Username*) printf '%s\n' "$GIT_MIRROR_USERNAME" ;;
*Password*) printf '%s\n' "$GIT_MIRROR_PASSWORD" ;;
*) printf '\n' ;;
esac
`
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, []byte(script), 0700); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, 0500); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

func gitSSHCommand(cfg Config, keyPath string) string {
	parts := []string{"ssh", "-o", "IdentitiesOnly=yes"}
	if keyPath != "" {
		parts = append(parts, "-i", keyPath)
	}
	if cfg.KnownHostsPath != "" {
		parts = append(parts, "-o", "UserKnownHostsFile="+cfg.KnownHostsPath, "-o", "StrictHostKeyChecking=yes")
	}
	return "GIT_SSH_COMMAND=" + strings.Join(parts, " ")
}

func authForCommand(cfg Config, args []string) EndpointAuth {
	for _, arg := range args {
		if arg == "clone" {
			return cfg.SourceAuth
		}
	}
	return cfg.TargetAuth
}

func createGitHubAppInstallationToken(auth EndpointAuth, client *http.Client, now func() time.Time) (string, error) {
	if auth.GitHubAppID == "" {
		return "", errors.New("GitHub App ID is required")
	}
	if auth.GitHubAppInstallationID == "" {
		return "", errors.New("GitHub App installation ID is required")
	}
	if auth.GitHubAppPrivateKeyPath == "" {
		return "", errors.New("GitHub App private key path is required")
	}
	apiURL := auth.GitHubAppAPIURL
	if apiURL == "" {
		apiURL = defaultGitHubAPIURL
	}
	privateKey, err := readRSAPrivateKey(auth.GitHubAppPrivateKeyPath)
	if err != nil {
		return "", err
	}
	jwt, err := signGitHubAppJWT(auth.GitHubAppID, privateKey, now())
	if err != nil {
		return "", err
	}
	requestURL := strings.TrimRight(apiURL, "/") + "/app/installations/" + neturl.PathEscape(auth.GitHubAppInstallationID) + "/access_tokens"
	ctx, cancel := context.WithTimeout(context.Background(), githubAppTokenRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(nil))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if client == nil {
		client = githubAppHTTPClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("GitHub App token request failed with status %s", resp.Status)
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Token == "" {
		return "", errors.New("GitHub App token response did not include token")
	}
	return body.Token, nil
}

func readRSAPrivateKey(path string) (*rsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("GitHub App private key must be PEM encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("GitHub App private key must be RSA")
	}
	return key, nil
}

func signGitHubAppJWT(appID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload, err := json.Marshal(map[string]any{
		"iat": now.Add(-1 * time.Minute).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": appID,
	})
	if err != nil {
		return "", err
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	signingInput := header + "." + encodedPayload
	sum := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBoolDefault(name string, fallback bool) bool {
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func safeCommand(args []string) string {
	if len(args) == 0 {
		return ""
	}
	redacted := append([]string(nil), args...)
	for i, arg := range redacted {
		if strings.Contains(strings.ToLower(arg), "key") {
			redacted[i] = "<redacted>"
			continue
		}
		redacted[i] = redactURLUserinfo(arg)
	}
	return strings.Join(redacted, " ")
}

func redactURLUserinfo(value string) string {
	parsed, err := neturl.Parse(value)
	if err != nil || parsed.User == nil {
		return value
	}
	parsed.User = neturl.User("<redacted>")
	return parsed.String()
}
