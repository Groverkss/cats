#!/usr/bin/env bash
# test-peggy.sh — Test peggy ticket and topic operations.
#
# Usage: ./test/test-peggy.sh [path-to-cats-binary]
#
# Requires: br, git
# Creates a temporary workspace and test repo, runs through ticket and topic
# lifecycle, verifies output at each step.

set -euo pipefail

CATS="${1:-./cats}"
TMPDIR="$(mktemp -d)"
WORKSPACE="$TMPDIR/workspace"
REPO="$TMPDIR/repo"

cleanup() {
    rm -rf "$TMPDIR"
}
trap cleanup EXIT

pass=0
fail=0

assert_ok() {
    local desc="$1"; shift
    if "$@" > /dev/null 2>&1; then
        echo "  PASS: $desc"
        ((pass++))
    else
        echo "  FAIL: $desc"
        echo "    Command: $*"
        ((fail++))
    fi
}

assert_fail() {
    local desc="$1"; shift
    if "$@" > /dev/null 2>&1; then
        echo "  FAIL: $desc (expected failure)"
        ((fail++))
    else
        echo "  PASS: $desc"
        ((pass++))
    fi
}

assert_contains() {
    local desc="$1"
    local expected="$2"; shift 2
    local output
    output=$("$@" 2>&1) || true
    if echo "$output" | grep -q "$expected"; then
        echo "  PASS: $desc"
        ((pass++))
    else
        echo "  FAIL: $desc"
        echo "    Expected to contain: $expected"
        echo "    Got: $output"
        ((fail++))
    fi
}

echo "=== Setting up test environment ==="

# Create test repo.
mkdir -p "$REPO"
git -C "$REPO" init -b main
echo "test" > "$REPO/README.md"
git -C "$REPO" add . && git -C "$REPO" commit -m "init" --allow-empty

# Create workspace.
mkdir -p "$WORKSPACE"
cd "$WORKSPACE"
"$CATS" kitten

echo ""
echo "=== Testing cats peggy topic ==="

assert_ok "topic create" \
    "$CATS" peggy topic create test-topic --repo "$REPO" --description "Test topic"

assert_contains "topic list shows topic" "test-topic" \
    "$CATS" peggy topic list

assert_contains "topic status shows details" "test-topic" \
    "$CATS" peggy topic status test-topic

echo ""
echo "=== Testing cats peggy ticket ==="

TICKET_ID=$("$CATS" peggy ticket create \
    --title="Test ticket 1" \
    --topic=test-topic \
    --type=task \
    --assignee=coder \
    --priority=1 \
    --description="This is a test ticket")

assert_ok "ticket create returned ID" test -n "$TICKET_ID"
echo "  Created ticket: $TICKET_ID"

assert_contains "ticket list shows ticket" "$TICKET_ID" \
    "$CATS" peggy ticket list

assert_contains "ticket show returns details" "Test ticket 1" \
    "$CATS" peggy ticket show "$TICKET_ID"

assert_contains "ticket ready shows ticket" "$TICKET_ID" \
    "$CATS" peggy ticket ready --role=coder

echo ""
echo "=== Testing ticket status updates ==="

assert_ok "ticket update to in_progress" \
    "$CATS" peggy ticket update "$TICKET_ID" --status=in_progress

assert_contains "ticket list shows in_progress" "in_progress" \
    "$CATS" peggy ticket list --status=in_progress

assert_ok "ticket close" \
    "$CATS" peggy ticket close "$TICKET_ID" --reason="Test done"

echo ""
echo "=== Testing dependencies ==="

T1=$("$CATS" peggy ticket create --title="Dep: first" --topic=test-topic --assignee=coder)
T2=$("$CATS" peggy ticket create --title="Dep: second" --topic=test-topic --assignee=coder --depends-on="$T1")
echo "  Created $T1 (first) and $T2 (second, depends on first)"

assert_contains "blocked shows dependent ticket" "$T2" \
    "$CATS" peggy ticket blocked

assert_contains "dep list shows dependency" "$T1" \
    "$CATS" peggy ticket dep list "$T2"

assert_ok "dep remove" \
    "$CATS" peggy ticket dep remove "$T2" "$T1"

assert_ok "dep add" \
    "$CATS" peggy ticket dep add "$T2" "$T1"

echo ""
echo "=== Testing topic close ==="

assert_ok "topic close" \
    "$CATS" peggy topic close test-topic

assert_contains "topic list shows closed" "closed" \
    "$CATS" peggy topic list

echo ""
echo "=== Testing help output ==="

assert_contains "cats --help shows commands" "kitten" \
    "$CATS" --help

assert_contains "peggy --help shows subcommands" "ticket" \
    "$CATS" peggy --help

assert_contains "ticket create --help shows flags" "--title" \
    "$CATS" peggy ticket create --help

echo ""
echo "=== Testing error cases ==="

assert_fail "ticket create without --title fails" \
    "$CATS" peggy ticket create --topic=test-topic

assert_fail "ticket create without --topic fails" \
    "$CATS" peggy ticket create --title="missing topic"

assert_fail "topic create without --repo fails" \
    "$CATS" peggy topic create bad-topic

assert_fail "topic status for nonexistent fails" \
    "$CATS" peggy topic status nonexistent

echo ""
echo "=== Results ==="
echo "  Passed: $pass"
echo "  Failed: $fail"

if [[ $fail -gt 0 ]]; then
    exit 1
fi
echo "  All tests passed."
