package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

type Config struct {
	Workspace string
	Workdir   string // defaults to Workspace if empty
	GPU       bool
	Network   bool
	ExtraRO   []string
	ExtraRW   []string
	Env       map[string]string // extra env vars (e.g. BR_ACTOR)
}

// Exec replaces the current process with a sandboxed bwrap invocation.
// cmd is the command to run inside the sandbox (e.g. "claude", "-p", ...).
// If cmd is empty, runs bash.
func Exec(cfg Config, cmd ...string) error {
	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		return fmt.Errorf("bwrap not found in PATH: %w", err)
	}

	args := buildArgs(cfg)
	args = append(args, "--")
	if len(cmd) == 0 {
		args = append(args, "bash")
	} else {
		args = append(args, cmd...)
	}

	// syscall.Exec replaces the process.
	return syscall.Exec(bwrapPath, append([]string{"bwrap"}, args...), nil)
}

// Command returns an *exec.Cmd for running a command in the sandbox.
// Use this when you need to capture output (e.g. moe running agents).
func Command(cfg Config, cmd ...string) *exec.Cmd {
	args := buildArgs(cfg)
	args = append(args, "--")
	if len(cmd) == 0 {
		args = append(args, "bash")
	} else {
		args = append(args, cmd...)
	}

	return exec.Command("bwrap", args...)
}

func buildArgs(cfg Config) []string {
	home := os.Getenv("HOME")
	workdir := cfg.Workdir
	if workdir == "" {
		workdir = cfg.Workspace
	}

	var args []string

	// Die with parent.
	args = append(args, "--die-with-parent")

	// Read-only system.
	args = append(args, "--ro-bind", "/usr", "/usr")
	args = append(args, "--ro-bind", "/lib", "/lib")
	args = append(args, "--ro-bind", "/bin", "/bin")
	args = append(args, "--ro-bind", "/sbin", "/sbin")
	args = append(args, "--ro-bind", "/etc", "/etc")

	// Proc and dev.
	args = append(args, "--proc", "/proc")
	args = append(args, "--dev", "/dev")
	args = append(args, "--dev-bind", "/dev/pts", "/dev/pts")
	args = append(args, "--dev-bind", "/dev/ptmx", "/dev/ptmx")

	// Home: tmpfs base, then selective mounts on top.
	args = append(args, "--tmpfs", home)

	// Workspace: full read-write.
	args = append(args, "--bind", cfg.Workspace, cfg.Workspace)

	// Claude Code config and cache.
	args = append(args, "--bind", filepath.Join(home, ".claude"), filepath.Join(home, ".claude"))
	args = append(args, "--bind", filepath.Join(home, ".cache"), filepath.Join(home, ".cache"))

	// Local binaries (claude CLI, pip tools).
	args = append(args, "--ro-bind", filepath.Join(home, ".local"), filepath.Join(home, ".local"))

	// Block credentials.
	args = append(args, "--tmpfs", filepath.Join(home, ".ssh"))
	args = append(args, "--tmpfs", filepath.Join(home, ".gnupg"))
	args = append(args, "--tmpfs", filepath.Join(home, ".aws"))

	// Working directory.
	args = append(args, "--chdir", workdir)

	// Clean environment.
	args = append(args, "--clearenv")
	args = append(args, "--setenv", "HOME", home)

	u, _ := user.Current()
	username := "user"
	if u != nil {
		username = u.Username
	}
	args = append(args, "--setenv", "USER", username)

	args = append(args, "--setenv", "TERM", envOr("TERM", "xterm-256color"))
	args = append(args, "--setenv", "LANG", envOr("LANG", "en_US.UTF-8"))
	args = append(args, "--setenv", "SHELL", "/bin/bash")
	args = append(args, "--setenv", "COLUMNS", envOr("COLUMNS", "120"))
	args = append(args, "--setenv", "LINES", envOr("LINES", "40"))
	args = append(args, "--setenv", "XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	args = append(args, "--setenv", "XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	// PATH.
	pathParts := []string{
		filepath.Join(cfg.Workspace, ".venv", "bin"),
		filepath.Join(home, ".local", "bin"),
	}
	if nvmBin := findNvmNodeBin(home); nvmBin != "" {
		pathParts = append(pathParts, nvmBin)
	}
	pathParts = append(pathParts, "/usr/local/go/bin", "/usr/local/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin")
	args = append(args, "--setenv", "PATH", strings.Join(pathParts, ":"))

	// Temp.
	args = append(args, "--setenv", "TMPDIR", "/tmp")
	args = append(args, "--setenv", "TEMP", "/tmp")
	args = append(args, "--setenv", "TMP", "/tmp")

	// Cats-specific.
	args = append(args, "--setenv", "CATS_WORKSPACE", cfg.Workspace)
	args = append(args, "--setenv", "CATS_SANDBOX", "1")

	// --- Conditional mounts ---
	if isDir("/lib64") {
		args = append(args, "--ro-bind", "/lib64", "/lib64")
	}
	if isDir("/run/systemd/resolve") {
		args = append(args, "--ro-bind", "/run/systemd/resolve", "/run/systemd/resolve")
	}
	if isFile(filepath.Join(home, ".claude.json")) {
		args = append(args, "--bind", filepath.Join(home, ".claude.json"), filepath.Join(home, ".claude.json"))
	}
	if isDir(filepath.Join(home, ".nvm")) {
		args = append(args, "--ro-bind", filepath.Join(home, ".nvm"), filepath.Join(home, ".nvm"))
	}
	if isDir(filepath.Join(home, ".npm")) {
		args = append(args, "--bind", filepath.Join(home, ".npm"), filepath.Join(home, ".npm"))
	}
	if isFile(filepath.Join(home, ".gitconfig")) {
		args = append(args, "--ro-bind", filepath.Join(home, ".gitconfig"), filepath.Join(home, ".gitconfig"))
	}
	if isDir(filepath.Join(cfg.Workspace, ".venv")) {
		args = append(args, "--setenv", "VIRTUAL_ENV", filepath.Join(cfg.Workspace, ".venv"))
	}

	// Tmp: workspace-local if exists, else tmpfs.
	if isDir(filepath.Join(cfg.Workspace, ".tmp")) {
		args = append(args, "--bind", filepath.Join(cfg.Workspace, ".tmp"), "/tmp")
	} else {
		args = append(args, "--tmpfs", "/tmp")
	}

	// Bind workdir if outside workspace (after /tmp mount).
	if !strings.HasPrefix(workdir, cfg.Workspace) {
		args = append(args, "--bind", workdir, workdir)
	}

	// Extra env vars.
	// Sort keys for deterministic output.
	keys := make([]string, 0, len(cfg.Env))
	for k := range cfg.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--setenv", k, cfg.Env[k])
	}

	// GPU passthrough.
	if cfg.GPU {
		if exists("/dev/kfd") {
			args = append(args, "--dev-bind", "/dev/kfd", "/dev/kfd")
		}
		if isDir("/dev/dri") {
			args = append(args, "--dev-bind", "/dev/dri", "/dev/dri")
		}
		if isDir("/opt/rocm") {
			args = append(args, "--ro-bind", "/opt/rocm", "/opt/rocm")
			args = append(args, "--setenv", "ROCM_PATH", "/opt/rocm")
		}
	}

	// Network.
	if !cfg.Network {
		args = append(args, "--unshare-net")
	}

	// Extra mounts.
	for _, m := range cfg.ExtraRO {
		args = append(args, "--ro-bind", m, m)
	}
	for _, m := range cfg.ExtraRW {
		args = append(args, "--bind", m, m)
	}

	return args
}

func findNvmNodeBin(home string) string {
	nvmDir := filepath.Join(home, ".nvm", "versions", "node")
	entries, err := os.ReadDir(nvmDir)
	if err != nil {
		return ""
	}
	if len(entries) == 0 {
		return ""
	}
	// Sort and take latest.
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return filepath.Join(nvmDir, names[len(names)-1], "bin")
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
