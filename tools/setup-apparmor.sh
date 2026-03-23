#!/usr/bin/env bash
# setup-apparmor.sh — Create AppArmor profile to allow bwrap user namespaces.
# Run with sudo: sudo ./tools/setup-apparmor.sh

set -euo pipefail

if [[ $EUID -ne 0 ]]; then
    echo "Error: Run with sudo"
    echo "  sudo $0"
    exit 1
fi

BWRAP_PATH="$(which bwrap 2>/dev/null || echo /usr/bin/bwrap)"

echo "Creating AppArmor profile for bwrap at $BWRAP_PATH..."

cat > /etc/apparmor.d/bwrap << EOF
abi <abi/4.0>,
include <tunables/global>

profile bwrap $BWRAP_PATH flags=(unconfined) {
  userns,
  include if exists <local/bwrap>
}
EOF

echo "Loading profile..."
apparmor_parser -r /etc/apparmor.d/bwrap

echo "Verifying..."
if [[ -f /etc/apparmor.d/bwrap ]] && apparmor_parser -p /etc/apparmor.d/bwrap >/dev/null 2>&1; then
    echo "✓ bwrap AppArmor profile installed and valid"
else
    echo "✗ Profile failed to parse"
    exit 1
fi
