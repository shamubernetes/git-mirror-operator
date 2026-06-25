package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuildGitCommandsForExactMode(t *testing.T) {
	cfg := Config{SourceURL: "git@github.com:example/source-repo.git", TargetURL: "git@codeberg.org:example/source-repo.git", MirrorMode: "exact"}

	commands, err := BuildGitCommands(cfg)
	if err != nil {
		t.Fatalf("expected commands: %v", err)
	}

	want := [][]string{
		{"git", "clone", "--mirror", "git@github.com:example/source-repo.git", "/tmp/repo.git"},
		{"git", "-C", "/tmp/repo.git", "push", "--mirror", "git@codeberg.org:example/source-repo.git"},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands mismatch\nwant: %#v\n got: %#v", want, commands)
	}
}

func TestBuildGitCommandsForAdditiveModeWithoutTags(t *testing.T) {
	cfg := Config{SourceURL: "git@github.com:example/source-repo.git", TargetURL: "git@codeberg.org:example/source-repo.git", MirrorMode: "additive", IncludeTags: false}

	commands, err := BuildGitCommands(cfg)
	if err != nil {
		t.Fatalf("expected commands: %v", err)
	}

	wantPush := []string{"git", "-C", "/tmp/repo.git", "push", "git@codeberg.org:example/source-repo.git", "refs/heads/*:refs/heads/*"}
	if !reflect.DeepEqual(commands[1], wantPush) {
		t.Fatalf("push mismatch\nwant: %#v\n got: %#v", wantPush, commands[1])
	}
}

func TestPrepareSSHKeysCopiesMountedSecretsToPrivateFiles(t *testing.T) {
	dir := t.TempDir()
	sourceMount := filepath.Join(dir, "source-mounted-key")
	targetMount := filepath.Join(dir, "target-mounted-key")
	if err := os.WriteFile(sourceMount, []byte("source-key"), 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetMount, []byte("target-key"), 0444); err != nil {
		t.Fatal(err)
	}

	cfg, err := prepareCredentials(Config{
		SourceAuth: EndpointAuth{Type: authTypeSSH, SSHKeyPath: sourceMount},
		TargetAuth: EndpointAuth{Type: authTypeSSH, SSHKeyPath: targetMount},
	}, filepath.Join(dir, "prepared"))
	if err != nil {
		t.Fatalf("expected prepared keys: %v", err)
	}

	assertPreparedKey(t, cfg.SourceAuth.SSHKeyPath, "source-key")
	assertPreparedKey(t, cfg.TargetAuth.SSHKeyPath, "target-key")
	if cfg.SourceAuth.SSHKeyPath == sourceMount {
		t.Fatal("expected source key path to point at prepared copy")
	}
	if cfg.TargetAuth.SSHKeyPath == targetMount {
		t.Fatal("expected target key path to point at prepared copy")
	}
}

func TestGitAuthEnvForBasicUsesAskPass(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	env, err := gitAuthEnv(Config{}, EndpointAuth{Type: authTypeBasic, Username: "user", Password: "secret"})
	if err != nil {
		t.Fatalf("expected auth env: %v", err)
	}

	envByName := map[string]string{}
	for _, item := range env {
		name, value, ok := strings.Cut(item, "=")
		if !ok {
			t.Fatalf("invalid env item %q", item)
		}
		envByName[name] = value
	}
	if envByName["GIT_TERMINAL_PROMPT"] != "0" {
		t.Fatalf("expected terminal prompts disabled, got %q", envByName["GIT_TERMINAL_PROMPT"])
	}
	if envByName["GIT_MIRROR_USERNAME"] != "user" || envByName["GIT_MIRROR_PASSWORD"] != "secret" {
		t.Fatalf("expected askpass credentials in env, got %#v", envByName)
	}
	if info, err := os.Stat(envByName["GIT_ASKPASS"]); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got != 0500 {
		t.Fatalf("expected askpass mode 0500, got %04o", got)
	}
}

func TestCreateGitHubAppInstallationToken(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyPath := filepath.Join(t.TempDir(), "github-app.pem")
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	if err := os.WriteFile(privateKeyPath, privateKeyPEM, 0400); err != nil {
		t.Fatal(err)
	}

	var gotPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"installation-token"}`))
	}))
	defer server.Close()

	token, err := createGitHubAppInstallationToken(EndpointAuth{
		GitHubAppID:             "12345",
		GitHubAppInstallationID: "67890",
		GitHubAppPrivateKeyPath: privateKeyPath,
		GitHubAppAPIURL:         server.URL,
	}, server.Client(), func() time.Time {
		return time.Unix(1700000000, 0)
	})
	if err != nil {
		t.Fatalf("expected token: %v", err)
	}
	if token != "installation-token" {
		t.Fatalf("expected installation token, got %q", token)
	}
	if gotPath != "/app/installations/67890/access_tokens" {
		t.Fatalf("unexpected token request path %q", gotPath)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Fatalf("expected bearer JWT, got %q", gotAuth)
	}
}

func TestSafeCommandRedactsURLCredentials(t *testing.T) {
	got := safeCommand([]string{"git", "clone", "https://user:secret@example.com/repo.git"})
	if strings.Contains(got, "secret") || strings.Contains(got, "user:") {
		t.Fatalf("expected credentials redacted, got %q", got)
	}
}

func assertPreparedKey(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("prepared key content mismatch: got %q", string(data))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0400 {
		t.Fatalf("expected prepared key mode 0400, got %04o", got)
	}
}
