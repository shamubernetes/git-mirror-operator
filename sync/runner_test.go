package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shamubernetes/git-mirror-operator/internal/syncenv"
)

func TestBuildGitCommandsForExactMode(t *testing.T) {
	cfg := Config{SourceURL: "git@github.com:example/source-repo.git", TargetURL: "git@codeberg.org:example/source-repo.git", MirrorMode: "exact"}

	commands, err := BuildGitCommands(cfg)
	if err != nil {
		t.Fatalf("expected commands: %v", err)
	}

	want := [][]string{
		{"git", "clone", "--bare", "git@github.com:example/source-repo.git", defaultMirrorRepoPath},
		{"git", "-C", defaultMirrorRepoPath, "push", "--prune", "git@codeberg.org:example/source-repo.git", forceHeadsRefSpec},
		{"git", "-C", defaultMirrorRepoPath, "push", "--prune", "git@codeberg.org:example/source-repo.git", forceTagsRefSpec},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands mismatch\nwant: %#v\n got: %#v", want, commands)
	}
	for _, command := range commands {
		joined := strings.Join(command, " ")
		if containsArg(command, "--mirror") {
			t.Fatalf("exact mode must not use --mirror: %#v", command)
		}
		if !containsArg(command, "push") {
			continue
		}
		if !strings.Contains(joined, "--prune") {
			t.Fatalf("exact push must prune: %#v", command)
		}
		if strings.Contains(joined, "refs/pull/") {
			t.Fatalf("exact push must not include provider refs: %#v", command)
		}
	}
}

func TestBuildGitCommandsForAdditiveModeWithoutTags(t *testing.T) {
	cfg := Config{SourceURL: "git@github.com:example/source-repo.git", TargetURL: "git@codeberg.org:example/source-repo.git", MirrorMode: "additive", IncludeTags: false}

	commands, err := BuildGitCommands(cfg)
	if err != nil {
		t.Fatalf("expected commands: %v", err)
	}

	wantPush := []string{"git", "-C", defaultMirrorRepoPath, "push", "git@codeberg.org:example/source-repo.git", headsRefSpec}
	if !reflect.DeepEqual(commands[1], wantPush) {
		t.Fatalf("push mismatch\nwant: %#v\n got: %#v", wantPush, commands[1])
	}
	for _, command := range commands[1:] {
		if containsArg(command, "--prune") {
			t.Fatalf("additive push must not prune: %#v", command)
		}
	}
}

func TestBuildGitCommandsForAdditiveModeWithTags(t *testing.T) {
	cfg := Config{SourceURL: "git@github.com:example/source-repo.git", TargetURL: "git@codeberg.org:example/source-repo.git", MirrorMode: "additive", IncludeTags: true}

	commands, err := BuildGitCommands(cfg)
	if err != nil {
		t.Fatalf("expected commands: %v", err)
	}

	want := [][]string{
		{"git", "clone", "--bare", "git@github.com:example/source-repo.git", defaultMirrorRepoPath},
		{"git", "-C", defaultMirrorRepoPath, "push", "git@codeberg.org:example/source-repo.git", headsRefSpec},
		{"git", "-C", defaultMirrorRepoPath, "push", "git@codeberg.org:example/source-repo.git", tagsRefSpec},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands mismatch\nwant: %#v\n got: %#v", want, commands)
	}
	for _, command := range commands[1:] {
		if containsArg(command, "--prune") {
			t.Fatalf("additive push must not prune: %#v", command)
		}
	}
}

func TestExactSyncMirrorsBranchesAndTagsOnlyPrunesAndForceUpdates(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.git")
	target := filepath.Join(root, "target.git")
	work := filepath.Join(root, "work")

	runGit(t, "init", "--bare", source)
	runGit(t, "init", "--bare", target)
	runGit(t, "init", work)
	runGit(t, "-C", work, "config", "user.email", "test@example.invalid")
	runGit(t, "-C", work, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("initial\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, "-C", work, "add", "-A")
	runGit(t, "-C", work, "commit", "-m", "initial")
	runGit(t, "-C", work, "branch", "-M", "main")
	runGit(t, "-C", work, "checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(work, "feature.txt"), []byte("feature\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, "-C", work, "add", "feature.txt")
	runGit(t, "-C", work, "commit", "-m", "feature")
	runGit(t, "-C", work, "checkout", "main")
	runGit(t, "-C", work, "tag", "v1.0.0")
	runGit(t, "-C", work, "tag", "stale-tag")
	runGit(t, "-C", work, "remote", "add", "origin", source)
	runGit(t, "-C", work, "push", "origin", "main", "feature", "v1.0.0", "stale-tag")
	mainCommit := strings.TrimSpace(runGit(t, "-C", work, "rev-parse", "main"))
	runGit(t, "-C", source, "update-ref", "refs/pull/1/head", mainCommit)

	runBuiltCommands(t, Config{SourceURL: source, TargetURL: target, MirrorMode: "exact", RepoPath: filepath.Join(root, "mirror-1.git")})
	assertRefExists(t, target, "refs/heads/main")
	assertRefExists(t, target, "refs/heads/feature")
	assertRefExists(t, target, "refs/tags/v1.0.0")
	assertRefExists(t, target, "refs/tags/stale-tag")
	assertRefMissing(t, target, "refs/pull/1/head")

	runGit(t, "-C", work, "checkout", "--orphan", "rewritten-main")
	if err := os.RemoveAll(filepath.Join(work, "README.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(work, "feature.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("rewritten\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, "-C", work, "add", "-A")
	runGit(t, "-C", work, "commit", "-m", "rewritten")
	rewrittenCommit := strings.TrimSpace(runGit(t, "-C", work, "rev-parse", "HEAD"))
	runGit(t, "-C", work, "tag", "-f", "v1.0.0", rewrittenCommit)
	runGit(t, "-C", work, "push", "origin", ":refs/heads/feature")
	runGit(t, "-C", work, "push", "origin", ":refs/tags/stale-tag")
	runGit(t, "-C", work, "push", "origin", "+rewritten-main:refs/heads/main")
	runGit(t, "-C", work, "push", "origin", "+refs/tags/v1.0.0:refs/tags/v1.0.0")
	runBuiltCommands(t, Config{SourceURL: source, TargetURL: target, MirrorMode: "exact", RepoPath: filepath.Join(root, "mirror-2.git")})
	assertRefExists(t, target, "refs/heads/main")
	assertRefMissing(t, target, "refs/heads/feature")
	assertRefExists(t, target, "refs/tags/v1.0.0")
	assertRefMissing(t, target, "refs/tags/stale-tag")
	assertRefMissing(t, target, "refs/pull/1/head")
	assertRefEquals(t, target, "refs/heads/main", rewrittenCommit)
	assertRefEquals(t, target, "refs/tags/v1.0.0", rewrittenCommit)
}

func TestShouldSkipEmptyTagPushOnlyWhenSourceAndTargetHaveNoTags(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.git")
	emptyTarget := filepath.Join(root, "empty-target.git")
	targetWithTag := filepath.Join(root, "target-with-tag.git")
	work := filepath.Join(root, "work")
	repoPath := filepath.Join(root, "mirror.git")

	runGit(t, "init", "--bare", source)
	runGit(t, "init", "--bare", emptyTarget)
	runGit(t, "init", "--bare", targetWithTag)
	runGit(t, "init", work)
	runGit(t, "-C", work, "config", "user.email", "test@example.invalid")
	runGit(t, "-C", work, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("initial\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, "-C", work, "add", "README.md")
	runGit(t, "-C", work, "commit", "-m", "initial")
	runGit(t, "-C", work, "branch", "-M", "main")
	runGit(t, "-C", work, "remote", "add", "origin", source)
	runGit(t, "-C", work, "push", "origin", "main")
	runGit(t, "-C", work, "tag", "stale")
	runGit(t, "-C", work, "push", targetWithTag, "stale")
	runGit(t, "clone", "--bare", source, repoPath)

	emptyTargetCommand := []string{"git", "-C", repoPath, "push", "--prune", emptyTarget, forceTagsRefSpec}
	skip, err := shouldSkipEmptyTagPush(emptyTargetCommand, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !skip {
		t.Fatal("expected empty source tags plus empty target tags to skip tag push")
	}

	targetWithTagCommand := []string{"git", "-C", repoPath, "push", "--prune", targetWithTag, forceTagsRefSpec}
	skip, err = shouldSkipEmptyTagPush(targetWithTagCommand, nil)
	if err != nil {
		t.Fatal(err)
	}
	if skip {
		t.Fatal("expected stale target tags to keep prune tag push enabled")
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
		SourceAuth: EndpointAuth{Type: syncenv.AuthTypeSSH, SSHKeyPath: sourceMount},
		TargetAuth: EndpointAuth{Type: syncenv.AuthTypeSSH, SSHKeyPath: targetMount},
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

func TestValidateEndpointAuthRequiresHTTPSForTokenAuth(t *testing.T) {
	for _, authType := range []string{syncenv.AuthTypeBasic, syncenv.AuthTypeGitHubApp} {
		t.Run(authType, func(t *testing.T) {
			auth := EndpointAuth{Type: authType, Username: "user", Password: "secret"}
			if err := validateEndpointAuth("source", "http://github.com/example/repo.git", auth); err == nil {
				t.Fatal("expected HTTP token-auth URL to be rejected")
			}
			if err := validateEndpointAuth("source", "https://github.com/example/repo.git", auth); err != nil {
				t.Fatalf("expected HTTPS token-auth URL to be accepted: %v", err)
			}
		})
	}

	if err := validateEndpointAuth("source", "git@github.com:example/repo.git", EndpointAuth{Type: syncenv.AuthTypeSSH, SSHKeyPath: "/keys/id_rsa"}); err != nil {
		t.Fatalf("expected SSH auth behavior to be preserved: %v", err)
	}
}

func TestGitAuthEnvForBasicUsesAskPass(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	env, err := gitAuthEnv(Config{}, EndpointAuth{Type: syncenv.AuthTypeBasic, Username: "user", Password: "secret"})
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
	if !strings.HasPrefix(envByName["GIT_ASKPASS"], dir+string(os.PathSeparator)) {
		t.Fatalf("expected askpass path under test temp dir %q, got %q", dir, envByName["GIT_ASKPASS"])
	}
	if info, err := os.Stat(envByName["GIT_ASKPASS"]); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got != 0500 {
		t.Fatalf("expected askpass mode 0500, got %04o", got)
	}
}

func TestCreateGitHubAppInstallationToken(t *testing.T) {
	privateKeyPath := writeTestGitHubAppPrivateKey(t)

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

func TestCreateGitHubAppInstallationTokenSetsRequestDeadline(t *testing.T) {
	privateKeyPath := writeTestGitHubAppPrivateKey(t)

	sawRoundTrip := false
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sawRoundTrip = true
		deadline, ok := req.Context().Deadline()
		if !ok {
			t.Error("expected GitHub App token request context to have a deadline")
		} else if remaining := time.Until(deadline); remaining <= 0 || remaining > time.Minute {
			t.Errorf("expected bounded positive request deadline, got %s", remaining)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Status:     "201 Created",
			Body:       io.NopCloser(strings.NewReader(`{"token":"installation-token"}`)),
			Header:     make(http.Header),
			Request:    req,
		}, nil
	})}

	token, err := createGitHubAppInstallationToken(EndpointAuth{
		GitHubAppID:             "12345",
		GitHubAppInstallationID: "67890",
		GitHubAppPrivateKeyPath: privateKeyPath,
		GitHubAppAPIURL:         "https://api.example.test",
	}, client, func() time.Time {
		return time.Unix(1700000000, 0)
	})
	if err != nil {
		t.Fatalf("expected token: %v", err)
	}
	if token != "installation-token" {
		t.Fatalf("expected installation token, got %q", token)
	}
	if !sawRoundTrip {
		t.Fatal("expected HTTP client to receive request")
	}
}

func TestSafeCommandRedactsURLCredentials(t *testing.T) {
	got := safeCommand([]string{"git", "clone", "https://user:secret@example.com/repo.git"})
	if strings.Contains(got, "secret") || strings.Contains(got, "user:") {
		t.Fatalf("expected credentials redacted, got %q", got)
	}
}

func writeTestGitHubAppPrivateKey(t *testing.T) string {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyPath := filepath.Join(t.TempDir(), "github-app.pem")
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)})
	if err := os.WriteFile(privateKeyPath, privateKeyPEM, 0400); err != nil {
		t.Fatal(err)
	}
	return privateKeyPath
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
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

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func runBuiltCommands(t *testing.T, cfg Config) {
	t.Helper()
	commands, err := BuildGitCommands(cfg)
	if err != nil {
		t.Fatalf("expected commands: %v", err)
	}
	for _, command := range commands {
		runCommand(t, command...)
	}
}

func runGit(t *testing.T, args ...string) string {
	t.Helper()
	return runCommand(t, append([]string{"git"}, args...)...)
}

func runCommand(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", strings.Join(args, " "), err, string(output))
	}
	return string(output)
}

func assertRefExists(t *testing.T, repoPath, ref string) {
	t.Helper()
	cmd := exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet", ref)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("expected %s in %s: %v\n%s", ref, repoPath, err, string(output))
	}
}

func assertRefEquals(t *testing.T, repoPath, ref, want string) {
	t.Helper()
	got := strings.TrimSpace(runGit(t, "-C", repoPath, "rev-parse", ref))
	if got != want {
		t.Fatalf("expected %s in %s to equal %s, got %s", ref, repoPath, want, got)
	}
}

func assertRefMissing(t *testing.T, repoPath, ref string) {
	t.Helper()
	cmd := exec.Command("git", "-C", repoPath, "show-ref", "--verify", "--quiet", ref)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected %s to be absent from %s", ref, repoPath)
	} else if len(output) != 0 {
		t.Fatalf("unexpected output while checking missing %s: %s", ref, string(output))
	}
}
