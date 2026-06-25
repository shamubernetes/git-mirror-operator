package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const preparedSSHDir = "/tmp/git-mirror-ssh"

type Config struct {
	SourceURL        string
	TargetURL        string
	MirrorMode       string
	IncludeTags      bool
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
		SourceSSHKeyPath: os.Getenv("SOURCE_SSH_KEY_PATH"),
		TargetSSHKeyPath: os.Getenv("TARGET_SSH_KEY_PATH"),
		KnownHostsPath:   os.Getenv("KNOWN_HOSTS_PATH"),
	}
}

func Run(cfg Config) error {
	preparedCfg, err := prepareSSHKeys(cfg, preparedSSHDir)
	if err != nil {
		return err
	}
	cfg = preparedCfg
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

func prepareSSHKeys(cfg Config, dir string) (Config, error) {
	var err error
	if cfg.SourceSSHKeyPath != "" {
		cfg.SourceSSHKeyPath, err = copySSHKey(cfg.SourceSSHKeyPath, filepath.Join(dir, "source_key"))
		if err != nil {
			return cfg, fmt.Errorf("prepare source SSH key: %w", err)
		}
	}
	if cfg.TargetSSHKeyPath != "" {
		cfg.TargetSSHKeyPath, err = copySSHKey(cfg.TargetSSHKeyPath, filepath.Join(dir, "target_key"))
		if err != nil {
			return cfg, fmt.Errorf("prepare target SSH key: %w", err)
		}
	}
	return cfg, nil
}

func copySSHKey(src, dst string) (string, error) {
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
