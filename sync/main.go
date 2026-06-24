package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type Config struct {
	SourceURL        string
	TargetURL        string
	MirrorMode       string
	IncludeTags      bool
	Prune            bool
	SourceSSHKeyPath string
	TargetSSHKeyPath string
	KnownHostsPath   string
}

func main() {
	cfg := ConfigFromEnv()
	if err := Run(cfg); err != nil {
		log.Fatalf("sync failed: %v", err)
	}
}

func ConfigFromEnv() Config {
	return Config{
		SourceURL:        os.Getenv("SOURCE_URL"),
		TargetURL:        os.Getenv("TARGET_URL"),
		MirrorMode:       envDefault("MIRROR_MODE", "exact"),
		IncludeTags:      envBoolDefault("INCLUDE_TAGS", true),
		Prune:            envBoolDefault("PRUNE", true),
		SourceSSHKeyPath: os.Getenv("SOURCE_SSH_KEY_PATH"),
		TargetSSHKeyPath: os.Getenv("TARGET_SSH_KEY_PATH"),
		KnownHostsPath:   os.Getenv("KNOWN_HOSTS_PATH"),
	}
}

func Run(cfg Config) error {
	commands, err := BuildGitCommands(cfg)
	if err != nil {
		return err
	}
	for _, args := range commands {
		log.Printf("running %s", safeCommand(args))
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = append(os.Environ(), gitSSHCommand(cfg, sshKeyForCommand(cfg, args)))
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

func sshKeyForCommand(cfg Config, args []string) string {
	for _, arg := range args {
		if arg == "clone" {
			return cfg.SourceSSHKeyPath
		}
	}
	return cfg.TargetSSHKeyPath
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
		}
	}
	return strings.Join(redacted, " ")
}
