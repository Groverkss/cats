#!/usr/bin/env bash
# cleanup-test-tickets.sh — Remove test topics, tickets, and worktrees.
#
# Usage:
#   ./tools/cleanup-test-tickets.sh <topic-name>   # clean up a specific topic
#   ./tools/cleanup-test-tickets.sh --all           # clean up ALL topics

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKSPACE="$(dirname "$SCRIPT_DIR")"
cd "$WORKSPACE"

TOPICS_DIR="$WORKSPACE/.topics"

cleanup_topic() {
    local name="$1"
    local topic_file="$TOPICS_DIR/$name.json"

    if [[ ! -f "$topic_file" ]]; then
        echo "Topic '$name' not found"
        return 1
    fi

    local epic_id worktree branch
    epic_id=$(python3 -c "import json; print(json.load(open('$topic_file'))['epic_id'])")
    worktree=$(python3 -c "import json; print(json.load(open('$topic_file'))['worktree'])")
    branch=$(python3 -c "import json; print(json.load(open('$topic_file'))['branch'])")

    echo "Cleaning up topic: $name"

    # Close all child tickets.
    echo "  Closing tickets under epic $epic_id..."
    br ready --parent="$epic_id" --format=json 2>/dev/null | \
        python3 -c "import json,sys; [print(t['id']) for t in json.load(sys.stdin)]" 2>/dev/null | \
        while read -r tid; do
            br close "$tid" --reason="cleanup" 2>/dev/null && echo "    closed $tid"
        done
    br list --status=in_progress --format=json 2>/dev/null | \
        python3 -c "import json,sys; [print(t['id']) for t in json.load(sys.stdin)]" 2>/dev/null | \
        while read -r tid; do
            br close "$tid" --reason="cleanup" 2>/dev/null
        done

    # Close the epic.
    br close "$epic_id" --reason="cleanup" 2>/dev/null && echo "  Closed epic $epic_id" || true

    # Remove worktree.
    if [[ -d "$worktree" ]]; then
        git worktree remove "$worktree" --force 2>/dev/null && echo "  Removed worktree $worktree" || true
    fi

    # Remove branch.
    git branch -D "$branch" 2>/dev/null && echo "  Deleted branch $branch" || true

    # Remove topic metadata.
    rm -f "$topic_file"
    echo "  Removed $topic_file"

    echo "  Done."
}

case "${1:-}" in
    --all)
        found=false
        for f in "$TOPICS_DIR"/*.json; do
            [[ -f "$f" ]] || continue
            found=true
            name=$(python3 -c "import json; print(json.load(open('$f'))['name'])")
            cleanup_topic "$name"
            echo ""
        done
        if [[ "$found" == "false" ]]; then
            echo "No topics to clean up."
        fi
        br sync --flush-only 2>/dev/null || true
        ;;
    ""|--help|-h)
        echo "Usage:"
        echo "  $0 <topic-name>   Clean up a specific topic"
        echo "  $0 --all          Clean up ALL topics"
        echo ""
        echo "Active topics:"
        ./tools/topic.sh list 2>/dev/null || echo "  (none)"
        ;;
    *)
        cleanup_topic "$1"
        br sync --flush-only 2>/dev/null || true
        ;;
esac
