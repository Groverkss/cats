#!/usr/bin/env bash
# test-moe.sh — Test moe agent pool and sandbox.
#
# Usage: ./test/test-moe.sh [path-to-cats-binary]
#
# Requires: br, git, bwrap

set -euo pipefail

CATS="$(cd "$(dirname "${1:-./cats}")" && pwd)/$(basename "${1:-./cats}")"
TESTDIR="$(cd "$(dirname "$0")/.." && pwd)/.tmp/test-moe-$$"
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

echo "=== Setting up test environment ==="

mkdir -p "$REPO" "$WORKSPACE"

# Create test repo.
git -C "$REPO" init -b main
echo "test" > "$REPO/README.md"
git -C "$REPO" add . && git -C "$REPO" commit -m "init"

# Create workspace.
cd "$WORKSPACE"
"$CATS" kitten

echo ""
echo "=== Testing workspace setup for moe ==="

assert_ok "cats.toml exists" test -f "$WORKSPACE/cats.toml"
assert_ok "logs dir exists" test -d "$WORKSPACE/logs"
assert_ok "prompts dir exists" test -d "$WORKSPACE/prompts"
assert_ok "coder prompt exists" test -f "$WORKSPACE/prompts/coder.md"
assert_ok "reviewer prompt exists" test -f "$WORKSPACE/prompts/reviewer.md"
assert_ok "cats binary copied" test -x "$WORKSPACE/cats"

echo ""
echo "=== Testing sandbox (cats box) ==="

if command -v bwrap &> /dev/null; then
    assert_contains "box runs command in sandbox" "CATS_SANDBOX=1" \
        "$CATS" box env

    assert_contains "box sees workspace" "$WORKSPACE" \
        "$CATS" box printenv CATS_WORKSPACE

    assert_contains "box has cats on PATH" "cats" \
        "$CATS" box which cats
else
    echo "  SKIP: bwrap not installed, skipping sandbox tests"
fi

echo ""
echo "=== Testing moe help ==="

assert_contains "cats moe shows help with tui" "tui" \
    "$CATS" moe

assert_contains "cats moe --help shows tui" "tui" \
    "$CATS" moe --help

echo ""
echo "=== Testing moe can find tickets via peggy ==="

"$CATS" peggy topic create moe-test --repo "$REPO" --description "Test for moe" > /dev/null

TICKET=$("$CATS" peggy ticket create \
    --title="Moe test ticket" \
    --topic=moe-test \
    --assignee=coder)

assert_contains "ticket is ready for coder" "$TICKET" \
    "$CATS" peggy ticket ready --role=coder

echo ""
echo "=== Results ==="
echo "  Passed: $pass"
echo "  Failed: $fail"

if [[ $fail -gt 0 ]]; then
    exit 1
fi
echo "  All tests passed."
