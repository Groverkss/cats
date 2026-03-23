#!/usr/bin/env bash
# init.sh — Initialize a cats workspace.
#
# Usage:
#   ./tools/init.sh [project-dir]
#
# Sets up .beads/, .topics/, .worktrees/, logs/, and .tmp/ directories.
# If project-dir is given, symlinks or copies it as the workspace project.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKSPACE="$(dirname "$SCRIPT_DIR")"

echo "=== Initializing cats workspace ==="
echo "Location: $WORKSPACE"
echo ""

# Create directories.
mkdir -p "$WORKSPACE/.topics"
mkdir -p "$WORKSPACE/.worktrees"
mkdir -p "$WORKSPACE/logs"
mkdir -p "$WORKSPACE/.tmp"

# Initialize beads if not already done.
if [[ ! -d "$WORKSPACE/.beads" ]]; then
    echo "Initializing beads..."
    (cd "$WORKSPACE" && br init)
else
    echo "Beads already initialized."
fi

# Verify tools.
echo ""
echo "Checking tools..."
for tool in br bwrap claude; do
    if command -v "$tool" &>/dev/null || [[ -x "/usr/local/go/bin/$tool" ]]; then
        echo "  ✓ $tool"
    else
        echo "  ✗ $tool (not found)"
    fi
done

# Check moe binary.
if [[ -x "$WORKSPACE/moe" ]]; then
    echo "  ✓ moe (built)"
else
    echo "  ✗ moe (not built — run: go build -o moe ./cmd/moe/)"
fi

echo ""
echo "=== Workspace ready ==="
echo ""
echo "Next steps:"
echo "  1. Launch peggy (planner):  ./cmd/peggy/peggy.sh"
echo "  2. Launch moe (TUI):        ./moe"
echo "  3. Create a topic:           ./tools/topic.sh create <name> '<description>'"
echo ""
