package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
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

	cfg, err := prepareSSHKeys(Config{
		SourceSSHKeyPath: sourceMount,
		TargetSSHKeyPath: targetMount,
	}, filepath.Join(dir, "prepared"))
	if err != nil {
		t.Fatalf("expected prepared keys: %v", err)
	}

	assertPreparedKey(t, cfg.SourceSSHKeyPath, "source-key")
	assertPreparedKey(t, cfg.TargetSSHKeyPath, "target-key")
	if cfg.SourceSSHKeyPath == sourceMount {
		t.Fatal("expected source key path to point at prepared copy")
	}
	if cfg.TargetSSHKeyPath == targetMount {
		t.Fatal("expected target key path to point at prepared copy")
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
