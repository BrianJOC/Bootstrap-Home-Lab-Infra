#!/usr/bin/env bash
# Writes test/config.yaml and test/.env pointing at the QEMU test VM.
# Usage: ./test/write-test-config.sh [domain] [ip]
set -euo pipefail

DOMAIN="${1:-lab.test}"
IP="${2:-127.0.0.1}"
KEY_PATH="$(realpath "$(dirname "$0")/keys/test_id_ed25519")"

mkdir -p "$(dirname "$0")"

cat > "$(dirname "$0")/config.yaml" <<EOF
target_host: "localhost:2222"
ssh_user: "ansible"
ssh_key_path: "${KEY_PATH}"
internal_domain: "${DOMAIN}"
cloudflare_zone_id: "test-zone-id-placeholder"
bootstrap_server_ip: "${IP}"
lab_vlan_subnet: "10.99.0.0/24"
lab_vlan_id: 99
admin_email: "test@${DOMAIN}"
vault_shares: 3
vault_threshold: 2
EOF

cat > "$(dirname "$0")/.env" <<'ENVEOF'
CF_DNS_API_TOKEN="test-cf-token-placeholder"
BOOTSTRAP_ADMIN_PASSWORD="test-admin-password"
ENVEOF
chmod 0600 "$(dirname "$0")/.env"

echo "Wrote test/config.yaml and test/.env"
echo "  target_host: localhost:2222"
echo "  ssh_key_path: ${KEY_PATH}"
