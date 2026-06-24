package main

import (
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
