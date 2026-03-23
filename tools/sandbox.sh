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
    # Resolve relative to workspace.
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

    # lib64 if it exists (Fedora/RHEL).
    $(test -d /lib64 && echo "--ro-bind /lib64 /lib64" || true)

    # systemd-resolved socket (DNS).
    $(test -d /run/systemd/resolve && echo "--ro-bind /run/systemd/resolve /run/systemd/resolve" || true)

    # Proc and dev.
    --proc /proc
    --dev /dev
    --dev-bind /dev/pts /dev/pts
    --dev-bind /dev/ptmx /dev/ptmx

    # Tmp: workspace-local if exists, else tmpfs.
    $(test -d "$WORKSPACE/.tmp" && echo "--bind $WORKSPACE/.tmp /tmp" || echo "--tmpfs /tmp")

    # The workspace: full read-write.
    --bind "$WORKSPACE" "$WORKSPACE"

    # Home directory: minimal tmpfs base.
    --tmpfs "$HOME_DIR"

    # Claude Code config and cache (persisted).
    --bind "$HOME_DIR/.claude" "$HOME_DIR/.claude"
    $(test -f "$HOME_DIR/.claude.json" && echo "--bind $HOME_DIR/.claude.json $HOME_DIR/.claude.json" || true)
    --bind "$HOME_DIR/.cache" "$HOME_DIR/.cache"

    # Local binaries (claude CLI, pip tools).
    --ro-bind "$HOME_DIR/.local" "$HOME_DIR/.local"

    # Node.js / nvm if present.
    $(test -d "$HOME_DIR/.nvm" && echo "--ro-bind $HOME_DIR/.nvm $HOME_DIR/.nvm" || true)
    $(test -d "$HOME_DIR/.npm" && echo "--bind $HOME_DIR/.npm $HOME_DIR/.npm" || true)

    # Git config (read-only).
    $(test -f "$HOME_DIR/.gitconfig" && echo "--ro-bind $HOME_DIR/.gitconfig $HOME_DIR/.gitconfig" || true)

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
    --setenv COLUMNS "${COLUMNS:-$(tput cols 2>/dev/null || echo 120)}"
    --setenv LINES "${LINES:-$(tput lines 2>/dev/null || echo 40)}"
    --setenv XDG_CACHE_HOME "$HOME_DIR/.cache"
    --setenv XDG_CONFIG_HOME "$HOME_DIR/.config"

    # PATH: venv first, local bins, node, system.
    --setenv PATH "$WORKSPACE/.venv/bin:$HOME_DIR/.local/bin:${NVM_NODE_BIN:+$NVM_NODE_BIN:}/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"

    # Python venv.
    $(test -d "$WORKSPACE/.venv" && echo "--setenv VIRTUAL_ENV $WORKSPACE/.venv" || true)

    # Temp directory.
    --setenv TMPDIR "/tmp"
    --setenv TEMP "/tmp"
    --setenv TMP "/tmp"

    # Cats-specific env.
    --setenv CATS_WORKSPACE "$WORKSPACE"
    --setenv CATS_SANDBOX 1
)

# Pass through BR_ACTOR if set.
if [[ -n "${BR_ACTOR:-}" ]]; then
    BWRAP_ARGS+=(--setenv BR_ACTOR "$BR_ACTOR")
fi

# --- GPU passthrough ---
if [[ "$GPU" == "1" ]]; then
    if [[ -e /dev/kfd ]]; then
        BWRAP_ARGS+=(--dev-bind /dev/kfd /dev/kfd)
    fi
    if [[ -d /dev/dri ]]; then
        BWRAP_ARGS+=(--dev-bind /dev/dri /dev/dri)
    fi
    if [[ -d /opt/rocm ]]; then
        BWRAP_ARGS+=(--ro-bind /opt/rocm /opt/rocm)
        BWRAP_ARGS+=(--setenv ROCM_PATH /opt/rocm)
    fi
fi

# --- Network ---
if [[ "$ALLOW_NET" == "0" ]]; then
    BWRAP_ARGS+=(--unshare-net)
fi

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
