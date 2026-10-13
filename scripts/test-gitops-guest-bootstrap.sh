#!/usr/bin/env bash
# Runs only inside the repository's disposable QEMU guest, which is also its
# own isolated bootstrap controller. No administrator-laptop secrets are used.
set -Eeuo pipefail
test "$(hostname)" = bootstrap-test
test "$(id -un)" = ansible
test -f test/keys/test_id_ed25519
test "$(findmnt -n -o SOURCE /)" = /dev/vda1

# Keep concurrent standup retries from rewriting the shared synthetic config or
# racing dependency setup and the bootstrap process.
exec 9>/tmp/bootstrap-test-gitops-bootstrap.lock
if ! flock -n 9; then
  echo "another GitOps guest bootstrap is already running" >&2
  exit 1
fi

vm_root_growth="$(sudo growpart /dev/vda 1)" || [[ "$vm_root_growth" == NOCHANGE:* ]]
sudo resize2fs /dev/vda1
umask 0077
mkdir -p test
cat > test/gitops-config.yaml <<EOF
target_host: "127.0.0.1:22"
ssh_user: ansible
ssh_key_path: "$PWD/test/keys/test_id_ed25519"
internal_domain: lab.test
cloudflare_zone_id: test-zone-id-placeholder
bootstrap_server_ip: 10.0.2.15
lab_vlan_subnet: 10.99.0.0/24
lab_gateway: 10.99.0.1
lab_vlan_id: 99
admin_email: test@lab.test
vault_init_output_path: /home/ansible/gitops-test-vault-init.json
vault_shares: 3
vault_threshold: 2
backup_enabled: false
controller_recovery_enabled: false
application_upgrades_enabled: false
monitoring_enabled: false
k3s_enabled: true
k3s_cluster_name: bootstrap
EOF
cat > test/.env <<'EOF'
CF_DNS_API_TOKEN=test-cf-token-placeholder
BOOTSTRAP_ADMIN_PASSWORD=QemuFixture-8n4R2x7L9p3V6s1K0z5T
CONTROLLER_RECOVERY_PASSWORD=test-controller-recovery-password
EOF

# Keep the expensive pinned Python and Ansible collection setup across stage
# retries. Rebuild it whenever any dependency input or Hermit pin changes, or
# when a required part of the environment is missing.
setup_fingerprint="$(sha256sum requirements-dev.in requirements-dev.lock \
  ansible/requirements.yml bin/hermit.hcl bin/.*.pkg Justfile | sha256sum | awk '{print $1}')"
setup_marker=.venv/.gitops-guest-setup-fingerprint
setup_is_current=false
if [[ -f "$setup_marker" ]] && [[ "$(cat "$setup_marker")" == "$setup_fingerprint" ]] && \
   [[ -x bin/python3 ]] && [[ -x bin/uv ]] && \
   [[ -x .venv/bin/ansible-playbook ]] && [[ -x .venv/bin/ansible-lint ]] && \
   [[ -x .venv/bin/molecule ]] && \
   [[ -f ansible/collections/ansible_collections/ansible/posix/META/runtime.yml ]] && \
   [[ -f ansible/collections/ansible_collections/community/docker/META/runtime.yml ]] && \
   [[ -f ansible/collections/ansible_collections/community/postgresql/META/runtime.yml ]] && \
   [[ -f ansible/collections/ansible_collections/community/general/META/runtime.yml ]]; then
  setup_is_current=true
fi
if [[ "$setup_is_current" != true ]]; then
  bin/just dev-setup
  printf '%s\n' "$setup_fingerprint" > "$setup_marker.tmp"
  mv "$setup_marker.tmp" "$setup_marker"
fi
bin/just build
export PATH="$PWD/.venv/bin:$PWD/bin:$PATH"
export ANSIBLE_CONFIG="$PWD/ansible/ansible.cfg"
export ANSIBLE_LOCAL_TEMP=/tmp/bootstrap-home-infra-ansible-local
export ACME_SERVER_URL=https://pebble:14000/dir
export ACME_CA_CERT_URL=http://10.0.2.2:8080/pebble.minica.pem
export ACME_ISSUING_CA_CERT_URL=https://pebble:15000/roots/0
export ACME_SERVER_HOST_IP=10.0.2.2
export CLOUDFLARE_API_BASE_URL=http://10.0.2.2:8080
exec bin/bootstrap --headless --accept-new-host-key --approve-terraform \
  --config test/gitops-config.yaml --env test/.env "$@"
