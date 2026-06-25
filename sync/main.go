package main

import (
	"bytes"
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
)

const preparedCredentialDir = "/tmp/git-mirror-credentials"
const defaultGitHubAPIURL = "https://api.github.com"

const (
	authTypeSSH       = "ssh"
	authTypeBasic     = "basic"
	authTypeGitHubApp = "githubApp"
)

type Config struct {
	SourceURL      string
	TargetURL      string
	MirrorMode     string
	IncludeTags    bool
	SourceAuth     EndpointAuth
	TargetAuth     EndpointAuth
	KnownHostsPath string
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
		SourceURL:      os.Getenv("SOURCE_URL"),
		TargetURL:      os.Getenv("TARGET_URL"),
		MirrorMode:     envDefault("MIRROR_MODE", "exact"),
		IncludeTags:    envBoolDefault("INCLUDE_TAGS", true),
		SourceAuth:     endpointAuthFromEnv("SOURCE"),
		TargetAuth:     endpointAuthFromEnv("TARGET"),
		KnownHostsPath: os.Getenv("KNOWN_HOSTS_PATH"),
	}
}

func Run(cfg Config) error {
	preparedCfg, err := prepareCredentials(cfg, preparedCredentialDir)
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
		log.Printf("running %s", safeCommand(args))
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		authEnv, err := gitAuthEnv(cfg, authForCommand(cfg, args))
		if err != nil {
			return err
		}
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
	commands := [][]string{
		{"git", "clone", "--mirror", cfg.SourceURL, "/tmp/repo.git"},
	}
	switch mode {
	case "exact":
		commands = append(commands, []string{"git", "-C", "/tmp/repo.git", "push", "--mirror", cfg.TargetURL})
	case "additive":
		push := []string{"git", "-C", "/tmp/repo.git", "push", cfg.TargetURL, "refs/heads/*:refs/heads/*"}
		if cfg.IncludeTags {
			push = append(push, "refs/tags/*:refs/tags/*")
		}
		commands = append(commands, push)
	default:
		return nil, fmt.Errorf("unsupported MIRROR_MODE %q", mode)
	}
	return commands, nil
}

func endpointAuthFromEnv(prefix string) EndpointAuth {
	authType := os.Getenv(prefix + "_AUTH_TYPE")
	if authType == "" && os.Getenv(prefix+"_SSH_KEY_PATH") != "" {
		authType = authTypeSSH
	}
	return EndpointAuth{
		Type:                    authType,
		SSHKeyPath:              os.Getenv(prefix + "_SSH_KEY_PATH"),
		Username:                os.Getenv(prefix + "_AUTH_USERNAME"),
		Password:                os.Getenv(prefix + "_AUTH_PASSWORD"),
		GitHubAppID:             os.Getenv(prefix + "_GITHUB_APP_ID"),
		GitHubAppInstallationID: os.Getenv(prefix + "_GITHUB_APP_INSTALLATION_ID"),
		GitHubAppPrivateKeyPath: os.Getenv(prefix + "_GITHUB_APP_PRIVATE_KEY_PATH"),
		GitHubAppAPIURL:         envDefault(prefix+"_GITHUB_APP_API_URL", defaultGitHubAPIURL),
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
	case authTypeSSH:
		if auth.SSHKeyPath == "" {
			return auth, fmt.Errorf("%s SSH auth requires an SSH key path", name)
		}
		keyPath, err := copyPrivateFile(auth.SSHKeyPath, filepath.Join(dir, "ssh_key"))
		if err != nil {
			return auth, fmt.Errorf("prepare %s SSH key: %w", name, err)
		}
		auth.SSHKeyPath = keyPath
	case authTypeGitHubApp:
		token, err := createGitHubAppInstallationToken(auth, http.DefaultClient, time.Now)
		if err != nil {
			return auth, fmt.Errorf("create %s GitHub App installation token: %w", name, err)
		}
		auth.Username = "x-access-token"
		auth.Password = token
	case authTypeBasic:
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
	case authTypeSSH:
		if auth.SSHKeyPath == "" {
			return fmt.Errorf("%s SSH auth requires an SSH key path", name)
		}
	case authTypeBasic, authTypeGitHubApp:
		if !isHTTPURL(rawURL) {
			return fmt.Errorf("%s %s auth requires an HTTP(S) git URL", name, auth.Type)
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

func isHTTPURL(rawURL string) bool {
	parsed, err := neturl.Parse(rawURL)
	if err != nil {
		return false
	}
	return parsed.Scheme == "http" || parsed.Scheme == "https"
}

func gitAuthEnv(cfg Config, auth EndpointAuth) ([]string, error) {
	switch auth.Type {
	case authTypeSSH:
		return []string{gitSSHCommand(cfg, auth.SSHKeyPath)}, nil
	case authTypeBasic, authTypeGitHubApp:
		askpassPath, err := ensureAskPassScript(preparedCredentialDir)
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
	req, err := http.NewRequest(http.MethodPost, requestURL, bytes.NewReader(nil))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
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
