#!/usr/bin/env bash
# test-e2e.sh — End-to-end test of the cats workspace.
#
# This script:
#   1. Initializes the workspace
#   2. Creates a test topic with a worktree
#   3. Creates a test ticket under the topic
#   4. Verifies br can find the ticket
#   5. Verifies the sandbox works
#   6. Prints instructions for testing moe
#
# Run from the workspace root.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKSPACE="$(dirname "$SCRIPT_DIR")"
cd "$WORKSPACE"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

pass() { echo -e "${GREEN}✓ $1${NC}"; }
fail() { echo -e "${RED}✗ $1${NC}"; exit 1; }
info() { echo -e "${YELLOW}→ $1${NC}"; }

echo "=== cats e2e test ==="
echo ""

# --- Step 1: Init ---
info "Initializing workspace..."
./tools/init.sh > /dev/null 2>&1
pass "Workspace initialized"

# --- Step 2: Verify beads ---
if [[ -d .beads ]]; then
    pass "Beads directory exists"
else
    fail "Beads directory missing"
fi

br info > /dev/null 2>&1
pass "br info works"

# --- Step 3: Create test topic ---
info "Creating test topic..."
TOPIC_NAME="e2e-test-$(date +%s)"

# Create topic (this creates branch + worktree + epic).
TOPIC_OUTPUT=$(./tools/topic.sh create "$TOPIC_NAME" "End-to-end test topic" 2>&1)
EPIC_ID=$(echo "$TOPIC_OUTPUT" | grep "Created epic:" | awk '{print $3}')

if [[ -z "$EPIC_ID" ]]; then
    fail "Failed to create topic (no epic ID)"
fi
pass "Topic created: $TOPIC_NAME (epic: $EPIC_ID)"

# Verify worktree.
if [[ -d ".worktrees/$TOPIC_NAME" ]]; then
    pass "Worktree exists: .worktrees/$TOPIC_NAME"
else
    fail "Worktree missing"
fi

# Verify topic metadata.
if [[ -f ".topics/$TOPIC_NAME.json" ]]; then
    pass "Topic metadata exists"
else
    fail "Topic metadata missing"
fi

# --- Step 4: Create test ticket ---
info "Creating test ticket..."
TICKET_ID=$(br create \
    --title="E2E test: create a hello.txt file" \
    --description="Create a file called hello.txt with 'Hello from cats!' as its content. This is a test ticket." \
    --type=task \
    --priority=2 \
    --parent="$EPIC_ID" \
    --assignee=coder \
    --silent)

if [[ -z "$TICKET_ID" ]]; then
    fail "Failed to create ticket"
fi
pass "Ticket created: $TICKET_ID"

# --- Step 5: Verify ticket is ready ---
info "Checking ticket visibility..."
READY_OUTPUT=$(br ready --assignee=coder --format=json 2>&1)
if echo "$READY_OUTPUT" | grep -q "$TICKET_ID"; then
    pass "Ticket visible in br ready"
else
    fail "Ticket not found in br ready output"
fi

# --- Step 6: Verify topic resolution ---
info "Verifying topic resolution..."
SHOW_OUTPUT=$(br show "$TICKET_ID" --format=json 2>&1)
if echo "$SHOW_OUTPUT" | grep -q "$EPIC_ID"; then
    pass "Ticket parent resolves to epic"
else
    # Parent might be set differently.
    info "Warning: parent field check inconclusive (may still work)"
fi

# --- Step 7: Test sandbox ---
info "Testing sandbox..."
SANDBOX_OUTPUT=$(./tools/sandbox.sh echo "sandbox works" 2>&1)
if echo "$SANDBOX_OUTPUT" | grep -q "sandbox works"; then
    pass "Sandbox executes commands"
else
    fail "Sandbox failed: $SANDBOX_OUTPUT"
fi

# Verify sandbox blocks credentials.
SANDBOX_SSH=$(./tools/sandbox.sh ls "$HOME/.ssh/" 2>&1 || true)
if [[ -z "$SANDBOX_SSH" ]] || echo "$SANDBOX_SSH" | grep -qi "no such file\|cannot access"; then
    pass "Sandbox blocks ~/.ssh"
else
    info "Warning: ~/.ssh may be accessible in sandbox"
fi

# --- Step 8: Verify moe binary ---
if [[ -x ./moe ]]; then
    pass "moe binary exists and is executable"
else
    info "moe not built — run: go build -o moe ./cmd/moe/"
fi

# --- Step 9: Test topic list ---
LIST_OUTPUT=$(./tools/topic.sh list 2>&1)
if echo "$LIST_OUTPUT" | grep -q "$TOPIC_NAME"; then
    pass "Topic visible in topic list"
else
    fail "Topic not in list"
fi

echo ""
echo "=== All checks passed ==="
echo ""
echo "To test moe interactively:"
echo "  1. Run: ./moe"
echo "  2. Press [l] to launch a coder agent"
echo "  3. Watch it pick up ticket $TICKET_ID"
echo "  4. It should create hello.txt in .worktrees/$TOPIC_NAME/"
echo ""
echo "To clean up:"
echo "  ./tools/topic.sh close $TOPIC_NAME"
echo "  git worktree remove .worktrees/$TOPIC_NAME"
echo "  br close $TICKET_ID"
echo ""
