#!/usr/bin/env bash
# peggy — Launch the planner agent (interactive claude session).
#
# Usage:
#   ./cmd/peggy/peggy.sh
#
# Peggy is the planner. She thinks, designs, creates tickets, and
# coordinates work. She does not write code.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKSPACE="$(cd "$SCRIPT_DIR/../.." && pwd)"
PROMPT_FILE="$WORKSPACE/prompts/planner.md"

if [[ ! -f "$PROMPT_FILE" ]]; then
    echo "Error: Planner prompt not found at $PROMPT_FILE"
    exit 1
fi

PROMPT="$(cat "$PROMPT_FILE")"

exec claude --append-system-prompt "$PROMPT"
