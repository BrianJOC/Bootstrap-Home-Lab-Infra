#!/usr/bin/env bash
# Writes test/config.yaml and test/.env pointing at the QEMU test VM.
# Usage: ./test/write-test-config.sh [domain] [ip]
set -euo pipefail

DOMAIN="${1:-lab.test}"
IP="${2:-10.0.2.15}"
PROFILE="${3:-single}"
KEY_PATH="$(realpath "$(dirname "$0")/keys/test_id_ed25519")"
OUTPUT="$(dirname "$0")/config.yaml"
if [[ "$PROFILE" == "ssd" ]]; then
  OUTPUT="$(dirname "$0")/config-ssd.yaml"
fi

mkdir -p "$(dirname "$0")"

cat > "$OUTPUT" <<EOF
target_host: "localhost:2222"
ssh_user: "ansible"
ssh_key_path: "${KEY_PATH}"
internal_domain: "${DOMAIN}"
cloudflare_zone_id: "test-zone-id-placeholder"
bootstrap_server_ip: "${IP}"
lab_vlan_subnet: "10.99.0.0/24"
lab_gateway: "10.99.0.1"
lab_vlan_id: 99
admin_email: "test@${DOMAIN}"
vault_init_output_path: "/tmp/bootstrap-home-infra-test-vault-init.json"
vault_shares: 3
vault_threshold: 2
EOF

if [[ "$PROFILE" == "ssd" ]]; then
  printf '%s\n' 'data_disk: "/dev/vdb"' 'data_disk_mode: "full"' >> "$OUTPUT"
fi

cat > "$(dirname "$0")/.env" <<'ENVEOF'
CF_DNS_API_TOKEN="test-cf-token-placeholder"
BOOTSTRAP_ADMIN_PASSWORD="QemuFixture-8n4R2x7L9p3V6s1K0z5T"
CONTROLLER_RECOVERY_PASSWORD="test-controller-recovery-password"
ENVEOF
chmod 0600 "$(dirname "$0")/.env"

echo "Wrote $OUTPUT and test/.env"
echo "  target_host: localhost:2222"
echo "  ssh_key_path: ${KEY_PATH}"
