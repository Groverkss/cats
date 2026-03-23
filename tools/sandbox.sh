#!/usr/bin/env bash
# sandbox.sh — Bubblewrap launcher for cats agent sessions.
#
# Usage:
#   ./tools/sandbox.sh [--workdir DIR] [command...]
#   ./tools/sandbox.sh                          # interactive bash
#   ./tools/sandbox.sh claude -p "do stuff"     # headless agent
#   ./tools/sandbox.sh --workdir .worktrees/foo claude -p "..."
#
# Environment variables:
#   CATS_WORKSPACE   Override workspace root (default: script's parent dir)
#   CATS_ALLOW_NET   Set to 0 to block network (default: 1)
#   CATS_GPU         Set to 1 to enable GPU passthrough (default: 0)
#   CATS_EXTRA_RO    Colon-separated extra read-only bind mounts
#   CATS_EXTRA_RW    Colon-separated extra read-write bind mounts
#   BR_ACTOR         Agent identity for beads (e.g. coder-1)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKSPACE="${CATS_WORKSPACE:-$(dirname "$SCRIPT_DIR")}"
ALLOW_NET="${CATS_ALLOW_NET:-1}"
GPU="${CATS_GPU:-0}"
HOME_DIR="$HOME"
WORKDIR="$WORKSPACE"

# Parse --workdir flag.
if [[ "${1:-}" == "--workdir" ]]; then
    shift
    WORKDIR="${1:?--workdir requires a path}"
    if [[ ! "$WORKDIR" = /* ]]; then
        WORKDIR="$WORKSPACE/$WORKDIR"
    fi
    shift
fi

# Resolve node binary path (nvm).
NVM_NODE_BIN=""
if [[ -d "$HOME_DIR/.nvm/versions/node" ]]; then
    NVM_NODE_LATEST="$(ls "$HOME_DIR/.nvm/versions/node/" | sort -V | tail -1)"
    if [[ -n "$NVM_NODE_LATEST" ]]; then
        NVM_NODE_BIN="$HOME_DIR/.nvm/versions/node/$NVM_NODE_LATEST/bin"
    fi
fi

# --- Build bwrap arguments ---
BWRAP_ARGS=(
    --die-with-parent

    # Read-only system.
    --ro-bind /usr /usr
    --ro-bind /lib /lib
    --ro-bind /bin /bin
    --ro-bind /sbin /sbin
    --ro-bind /etc /etc

    # Proc and dev.
    --proc /proc
    --dev /dev
    --dev-bind /dev/pts /dev/pts
    --dev-bind /dev/ptmx /dev/ptmx

    # Home directory: minimal tmpfs base (must come before sub-mounts).
    --tmpfs "$HOME_DIR"

    # The workspace: full read-write (on top of tmpfs home).
    --bind "$WORKSPACE" "$WORKSPACE"

    # Workdir bind (if outside workspace, e.g. /tmp worktrees).
    # Added conditionally below.

    # Claude Code config and cache (persisted).
    --bind "$HOME_DIR/.claude" "$HOME_DIR/.claude"
    --bind "$HOME_DIR/.cache" "$HOME_DIR/.cache"

    # Local binaries (claude CLI, pip tools).
    --ro-bind "$HOME_DIR/.local" "$HOME_DIR/.local"

    # Block credentials.
    --tmpfs "$HOME_DIR/.ssh"
    --tmpfs "$HOME_DIR/.gnupg"
    --tmpfs "$HOME_DIR/.aws"

    # Working directory.
    --chdir "$WORKDIR"

    # Clean environment.
    --clearenv
    --setenv HOME "$HOME_DIR"
    --setenv USER "$(id -un)"
    --setenv TERM "${TERM:-xterm-256color}"
    --setenv LANG "${LANG:-en_US.UTF-8}"
    --setenv SHELL /bin/bash
    --setenv COLUMNS "${COLUMNS:-120}"
    --setenv LINES "${LINES:-40}"
    --setenv XDG_CACHE_HOME "$HOME_DIR/.cache"
    --setenv XDG_CONFIG_HOME "$HOME_DIR/.config"

    # PATH.
    --setenv PATH "$WORKSPACE/.venv/bin:$HOME_DIR/.local/bin:${NVM_NODE_BIN:+$NVM_NODE_BIN:}/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

    # Temp directory.
    --setenv TMPDIR "/tmp"
    --setenv TEMP "/tmp"
    --setenv TMP "/tmp"

    # Cats-specific env.
    --setenv CATS_WORKSPACE "$WORKSPACE"
    --setenv CATS_SANDBOX 1
)

# --- Conditional mounts ---
[[ -d /lib64 ]] && BWRAP_ARGS+=(--ro-bind /lib64 /lib64)
[[ -d /run/systemd/resolve ]] && BWRAP_ARGS+=(--ro-bind /run/systemd/resolve /run/systemd/resolve)
[[ -f "$HOME_DIR/.claude.json" ]] && BWRAP_ARGS+=(--bind "$HOME_DIR/.claude.json" "$HOME_DIR/.claude.json")
[[ -d "$HOME_DIR/.nvm" ]] && BWRAP_ARGS+=(--ro-bind "$HOME_DIR/.nvm" "$HOME_DIR/.nvm")
[[ -d "$HOME_DIR/.npm" ]] && BWRAP_ARGS+=(--bind "$HOME_DIR/.npm" "$HOME_DIR/.npm")
[[ -f "$HOME_DIR/.gitconfig" ]] && BWRAP_ARGS+=(--ro-bind "$HOME_DIR/.gitconfig" "$HOME_DIR/.gitconfig")
[[ -d "$WORKSPACE/.venv" ]] && BWRAP_ARGS+=(--setenv VIRTUAL_ENV "$WORKSPACE/.venv")

# Tmp: workspace-local if exists, else tmpfs.
if [[ -d "$WORKSPACE/.tmp" ]]; then
    BWRAP_ARGS+=(--bind "$WORKSPACE/.tmp" /tmp)
else
    BWRAP_ARGS+=(--tmpfs /tmp)
fi

# Bind workdir if it's outside the workspace (AFTER /tmp mount so it overlays).
if [[ "$WORKDIR" != "$WORKSPACE"* ]]; then
    BWRAP_ARGS+=(--bind "$WORKDIR" "$WORKDIR")
fi

# Pass through BR_ACTOR if set.
[[ -n "${BR_ACTOR:-}" ]] && BWRAP_ARGS+=(--setenv BR_ACTOR "$BR_ACTOR")

# --- GPU passthrough ---
if [[ "$GPU" == "1" ]]; then
    [[ -e /dev/kfd ]] && BWRAP_ARGS+=(--dev-bind /dev/kfd /dev/kfd)
    [[ -d /dev/dri ]] && BWRAP_ARGS+=(--dev-bind /dev/dri /dev/dri)
    if [[ -d /opt/rocm ]]; then
        BWRAP_ARGS+=(--ro-bind /opt/rocm /opt/rocm)
        BWRAP_ARGS+=(--setenv ROCM_PATH /opt/rocm)
    fi
fi

# --- Network ---
[[ "$ALLOW_NET" == "0" ]] && BWRAP_ARGS+=(--unshare-net)

# --- Extra mounts ---
if [[ -n "${CATS_EXTRA_RO:-}" ]]; then
    IFS=':' read -ra EXTRA_RO <<< "$CATS_EXTRA_RO"
    for mount in "${EXTRA_RO[@]}"; do
        BWRAP_ARGS+=(--ro-bind "$mount" "$mount")
    done
fi

if [[ -n "${CATS_EXTRA_RW:-}" ]]; then
    IFS=':' read -ra EXTRA_RW <<< "$CATS_EXTRA_RW"
    for mount in "${EXTRA_RW[@]}"; do
        BWRAP_ARGS+=(--bind "$mount" "$mount")
    done
fi

# --- Command ---
if [[ $# -eq 0 ]]; then
    CMD=(bash)
else
    CMD=("$@")
fi

# --- Launch ---
exec bwrap "${BWRAP_ARGS[@]}" -- "${CMD[@]}"
