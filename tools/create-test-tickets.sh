#!/usr/bin/env bash
# create-test-tickets.sh — Create dummy tickets for testing moe.
#
# Creates a topic with several simple coding tickets that agents can
# actually complete (create files, write content, etc.)
#
# Usage:
#   ./tools/create-test-tickets.sh [topic-name]

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKSPACE="$(dirname "$SCRIPT_DIR")"
cd "$WORKSPACE"

TOPIC_NAME="${1:-test-$(date +%s)}"

echo "=== Creating test topic: $TOPIC_NAME ==="
echo ""

# Create the topic.
TOPIC_OUTPUT=$(./tools/topic.sh create "$TOPIC_NAME" "Test topic with dummy tickets for moe" 2>&1)
EPIC_ID=$(echo "$TOPIC_OUTPUT" | grep "Created epic:" | awk '{print $3}')

if [[ -z "$EPIC_ID" ]]; then
    echo "Error: Failed to create topic"
    echo "$TOPIC_OUTPUT"
    exit 1
fi

echo "Topic: $TOPIC_NAME"
echo "Epic:  $EPIC_ID"
echo ""

# Create tickets.
echo "Creating tickets..."

T1=$(br create \
    --title="Create project README" \
    --description="Create a file called README.md in the worktree root with the following content:

# Test Project

This is a test project created by cats.

## Structure
- src/ — source code
- docs/ — documentation
- tests/ — test files

Commit the file with message: '$TOPIC_NAME: add README'" \
    --type=task --priority=2 --parent="$EPIC_ID" --assignee=coder --silent)
echo "  ✓ $T1 — Create project README"

T2=$(br create \
    --title="Create source directory with hello.py" \
    --description="Create the directory src/ and a file src/hello.py with the following content:

def greet(name: str) -> str:
    return f\"Hello, {name}!\"

def main():
    print(greet(\"cats\"))

if __name__ == \"__main__\":
    main()

Commit with message: '$TOPIC_NAME: add hello.py'" \
    --type=task --priority=2 --parent="$EPIC_ID" --assignee=coder --silent)
echo "  ✓ $T2 — Create hello.py"

T3=$(br create \
    --title="Create test file for hello.py" \
    --description="Create the directory tests/ and a file tests/test_hello.py with the following content:

from src.hello import greet

def test_greet():
    assert greet(\"world\") == \"Hello, world!\"

def test_greet_cats():
    assert greet(\"cats\") == \"Hello, cats!\"

Commit with message: '$TOPIC_NAME: add test_hello.py'" \
    --type=task --priority=2 --parent="$EPIC_ID" --assignee=coder --silent)
echo "  ✓ $T3 — Create test_hello.py"

T4=$(br create \
    --title="Create a config file" \
    --description="Create a file called config.toml with the following content:

[project]
name = \"test-project\"
version = \"0.1.0\"

[build]
output_dir = \"dist\"
minify = true

[logging]
level = \"info\"
format = \"json\"

Commit with message: '$TOPIC_NAME: add config.toml'" \
    --type=task --priority=3 --parent="$EPIC_ID" --assignee=coder --silent)
echo "  ✓ $T4 — Create config.toml"

T5=$(br create \
    --title="Create docs/architecture.md" \
    --description="Create the directory docs/ and a file docs/architecture.md with the following content:

# Architecture

## Overview
The system is composed of three layers:
1. **Input** — reads configuration and user requests
2. **Processing** — transforms input according to rules
3. **Output** — writes results to the configured destination

## Data Flow
\`\`\`
Input -> Parser -> Transformer -> Writer -> Output
\`\`\`

## Key Decisions
- Config is TOML-based for readability
- Processing is synchronous for simplicity
- Output format is JSON by default

Commit with message: '$TOPIC_NAME: add architecture.md'" \
    --type=task --priority=3 --parent="$EPIC_ID" --assignee=coder --silent)
echo "  ✓ $T5 — Create architecture.md"

br sync --flush-only 2>/dev/null

echo ""
echo "=== Done ==="
echo "Created 5 tickets under topic $TOPIC_NAME (epic $EPIC_ID)"
echo ""
echo "Ready tickets:"
br ready --assignee=coder 2>&1 | head -10
echo ""
echo "Launch moe and spawn coders to process them:"
echo "  ./moe"
