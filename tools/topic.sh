#!/usr/bin/env bash
# topic.sh — Manage topics (branch + worktree + epic binding).
#
# Usage:
#   ./tools/topic.sh create <name> <description>
#   ./tools/topic.sh list
#   ./tools/topic.sh status <name>
#   ./tools/topic.sh close <name>

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKSPACE="$(dirname "$SCRIPT_DIR")"
TOPICS_DIR="$WORKSPACE/.topics"
WORKTREES_DIR="${CATS_WORKTREES_DIR:-/tmp/cats-worktrees}"

mkdir -p "$TOPICS_DIR" "$WORKTREES_DIR"

usage() {
    echo "Usage: topic.sh {create|list|status|close} [args...]"
    exit 1
}

cmd_create() {
    local name="${1:?Usage: topic.sh create <name> <description>}"
    local description="${2:?Usage: topic.sh create <name> <description>}"
    local branch="topic/$name"
    local topic_file="$TOPICS_DIR/$name.json"
    local worktree="$WORKTREES_DIR/$name"

    if [[ -f "$topic_file" ]]; then
        echo "Error: Topic '$name' already exists"
        exit 1
    fi

    # Create the br epic.
    epic_id=$(br create \
        --title="Topic: $name" \
        --description="$description" \
        --type=epic \
        --priority=1 \
        --silent)

    echo "Created epic: $epic_id"

    # Create branch and worktree.
    git branch "$branch" 2>/dev/null || true
    git worktree add "$worktree" "$branch"

    echo "Created worktree: $worktree"

    # Write topic metadata.
    cat > "$topic_file" <<EOF
{
  "name": "$name",
  "branch": "$branch",
  "worktree": "$worktree",
  "epic_id": "$epic_id",
  "status": "open",
  "created": "$(date -Iseconds)"
}
EOF

    echo "Topic '$name' created:"
    echo "  Branch:   $branch"
    echo "  Worktree: $worktree"
    echo "  Epic:     $epic_id"
    echo ""
    echo "Next: Add tasks with --parent=$epic_id"
    echo "  br create --title='...' --type=task --parent=$epic_id --assignee=coder"
}

cmd_list() {
    echo "=== Active Topics ==="
    local found=false
    for f in "$TOPICS_DIR"/*.json; do
        [[ -f "$f" ]] || continue
        found=true
        local name branch epic_id status
        name=$(python3 -c "import json; print(json.load(open('$f'))['name'])")
        branch=$(python3 -c "import json; print(json.load(open('$f'))['branch'])")
        epic_id=$(python3 -c "import json; print(json.load(open('$f'))['epic_id'])")
        status=$(python3 -c "import json; print(json.load(open('$f'))['status'])")
        echo "  $name ($status)"
        echo "    branch: $branch | epic: $epic_id"
    done
    if [[ "$found" == "false" ]]; then
        echo "  (none)"
    fi
}

cmd_status() {
    local name="${1:?Usage: topic.sh status <name>}"
    local topic_file="$TOPICS_DIR/$name.json"

    if [[ ! -f "$topic_file" ]]; then
        echo "Error: Topic '$name' not found"
        exit 1
    fi

    local epic_id
    epic_id=$(python3 -c "import json; print(json.load(open('$topic_file'))['epic_id'])")

    echo "=== Topic: $name ==="
    python3 -m json.tool "$topic_file"
    echo ""
    echo "=== Epic Issues ==="
    br show "$epic_id" 2>/dev/null || echo "  (no issues)"
    echo ""
    echo "=== Branch Commits ==="
    git log --oneline "main..topic/$name" 2>/dev/null | head -10 || echo "  (no commits yet)"
}

cmd_close() {
    local name="${1:?Usage: topic.sh close <name>}"
    local topic_file="$TOPICS_DIR/$name.json"

    if [[ ! -f "$topic_file" ]]; then
        echo "Error: Topic '$name' not found"
        exit 1
    fi

    local epic_id worktree
    epic_id=$(python3 -c "import json; print(json.load(open('$topic_file'))['epic_id'])")
    worktree=$(python3 -c "import json; print(json.load(open('$topic_file'))['worktree'])")

    # Update topic metadata.
    python3 -c "
import json
with open('$topic_file', 'r+') as f:
    d = json.load(f)
    d['status'] = 'closed'
    f.seek(0); json.dump(d, f, indent=2); f.truncate()
"

    # Close the epic.
    br close "$epic_id" --reason="Topic closed" 2>/dev/null || true

    echo "Topic '$name' closed. Epic $epic_id closed."
    echo ""
    echo "Next steps:"
    echo "  1. Squash merge: git merge --squash topic/$name"
    echo "  2. Remove worktree: git worktree remove $worktree"
}

# --- Dispatch ---
case "${1:-}" in
    create) shift; cmd_create "$@" ;;
    list)   shift; cmd_list "$@" ;;
    status) shift; cmd_status "$@" ;;
    close)  shift; cmd_close "$@" ;;
    *)      usage ;;
esac
