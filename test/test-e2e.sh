#!/usr/bin/env bash
# test-e2e.sh — End-to-end test of the full cats workflow.
#
# Usage: ./test/test-e2e.sh [path-to-cats-binary]
#
# Requires: br, git, bwrap

set -euo pipefail

CATS="$(cd "$(dirname "${1:-./cats}")" && pwd)/$(basename "${1:-./cats}")"
TESTDIR="$(cd "$(dirname "$0")/.." && pwd)/.tmp/test-e2e-$$"
WORKSPACE="$TESTDIR/workspace"
REPO="$TESTDIR/repo"

cleanup() {
    rm -rf "$TESTDIR"
}
trap cleanup EXIT

pass=0
fail=0

assert_ok() {
    local desc="$1"; shift
    if "$@" > /dev/null 2>&1; then
        echo "  PASS: $desc"
        pass=$((pass + 1))
    else
        echo "  FAIL: $desc"
        echo "    Command: $*"
        fail=$((fail + 1))
    fi
}

assert_fail() {
    local desc="$1"; shift
    if "$@" > /dev/null 2>&1; then
        echo "  FAIL: $desc (expected failure)"
        fail=$((fail + 1))
    else
        echo "  PASS: $desc"
        pass=$((pass + 1))
    fi
}

assert_contains() {
    local desc="$1"
    local expected="$2"; shift 2
    local output
    output=$("$@" 2>&1) || true
    if echo "$output" | grep -qF -- "$expected"; then
        echo "  PASS: $desc"
        pass=$((pass + 1))
    else
        echo "  FAIL: $desc"
        echo "    Expected to contain: $expected"
        echo "    Got: $output"
        fail=$((fail + 1))
    fi
}

assert_not_contains() {
    local desc="$1"
    local unexpected="$2"; shift 2
    local output
    output=$("$@" 2>&1) || true
    if echo "$output" | grep -qF -- "$unexpected"; then
        echo "  FAIL: $desc"
        echo "    Should not contain: $unexpected"
        echo "    Got: $output"
        fail=$((fail + 1))
    else
        echo "  PASS: $desc"
        pass=$((pass + 1))
    fi
}

echo "=== Phase 1: Setup ==="

mkdir -p "$REPO/internal/api" "$WORKSPACE"

# Create a git repo with some content.
git -C "$REPO" init -b main
echo "# Test Project" > "$REPO/README.md"
echo "package api" > "$REPO/internal/api/router.go"
git -C "$REPO" add . && git -C "$REPO" commit -m "init"

# Initialize workspace.
cd "$WORKSPACE"
"$CATS" kitten

assert_ok "workspace has cats.toml" test -f cats.toml
assert_ok "workspace has cats binary" test -x cats
assert_ok "workspace has prompts" test -f prompts/coder.md

echo ""
echo "=== Phase 2: Topic creation ==="

"$CATS" peggy topic create api-auth --repo "$REPO" --description "Add authentication to API" > /dev/null

assert_ok "topic metadata exists" test -f .topics/api-auth.json
assert_ok "worktree created" test -d .worktrees/api-auth
assert_ok "branch exists in worktree" git -C .worktrees/api-auth rev-parse --abbrev-ref HEAD

echo ""
echo "=== Phase 3: Ticket creation with dependencies ==="

T_IMPL=$("$CATS" peggy ticket create \
    --title="Implement JWT middleware" \
    --topic=api-auth \
    --type=task \
    --assignee=coder \
    --priority=1 \
    --description="Add JWT validation to internal/api/router.go")
echo "  Implementation ticket: $T_IMPL"

T_TEST=$("$CATS" peggy ticket create \
    --title="Add auth tests" \
    --topic=api-auth \
    --type=task \
    --assignee=coder \
    --priority=2 \
    --depends-on="$T_IMPL" \
    --description="Test the JWT middleware")
echo "  Test ticket: $T_TEST (depends on $T_IMPL)"

T_REVIEW=$("$CATS" peggy ticket create \
    --title="Review: api-auth" \
    --topic=api-auth \
    --type=review \
    --assignee=reviewer \
    --priority=1 \
    --depends-on="$T_IMPL,$T_TEST")
echo "  Review ticket: $T_REVIEW (depends on $T_IMPL, $T_TEST)"

echo ""
echo "=== Phase 4: Verify dependency-based readiness ==="

assert_contains "impl ticket is ready" "$T_IMPL" \
    "$CATS" peggy ticket ready --role=coder

assert_not_contains "test ticket is NOT ready" "$T_TEST" \
    "$CATS" peggy ticket ready --role=coder

assert_not_contains "review ticket is NOT ready" "$T_REVIEW" \
    "$CATS" peggy ticket ready --role=reviewer

assert_contains "test ticket is blocked" "$T_TEST" \
    "$CATS" peggy ticket blocked

assert_contains "review ticket is blocked" "$T_REVIEW" \
    "$CATS" peggy ticket blocked

echo ""
echo "=== Phase 5: Complete implementation, verify unblocking ==="

"$CATS" peggy ticket update "$T_IMPL" --status=in_progress > /dev/null
"$CATS" peggy ticket close "$T_IMPL" --reason="Implemented" > /dev/null

assert_contains "test ticket is now ready" "$T_TEST" \
    "$CATS" peggy ticket ready --role=coder

assert_not_contains "review still not ready" "$T_REVIEW" \
    "$CATS" peggy ticket ready --role=reviewer

echo ""
echo "=== Phase 6: Complete tests, verify review unblocks ==="

"$CATS" peggy ticket close "$T_TEST" --reason="Tests pass" > /dev/null

assert_contains "review ticket is now ready" "$T_REVIEW" \
    "$CATS" peggy ticket ready --role=reviewer

echo ""
echo "=== Phase 7: Sandbox verification ==="

if command -v bwrap &> /dev/null; then
    assert_contains "sandbox sees workspace" "$WORKSPACE" \
        "$CATS" box printenv CATS_WORKSPACE

    assert_contains "sandbox topic worktree accessible" "api" \
        "$CATS" box --topic api-auth ls internal/
else
    echo "  SKIP: bwrap not installed"
fi

echo ""
echo "=== Phase 8: Cleanup ==="

"$CATS" peggy ticket close "$T_REVIEW" --reason="Approved" > /dev/null
"$CATS" peggy topic close api-auth > /dev/null

assert_contains "topic shows closed" "closed" \
    "$CATS" peggy topic list

echo ""
echo "=== Results ==="
echo "  Passed: $pass"
echo "  Failed: $fail"

if [[ $fail -gt 0 ]]; then
    exit 1
fi
echo "  All tests passed."
