set shell := ["bash", "-euo", "pipefail", "-c"]

default:
    @just --list

# Stable entry points for the no-backup experimental release checklist.
# status exits 1 until every required gate has actual passing evidence.
release-status:
    bin/python scripts/release-check.py status

release-source:
    bin/python scripts/release-check.py source

# Mutates only the fixed disposable guest; preserves an existing snapshot.
release-standup:
    bin/python scripts/release-check.py standup

release-verify:
    bin/python scripts/release-check.py verify

test-release-check:
    bin/python scripts/test-release-check.py
    bin/python scripts/test-dev-authorized.py
    bin/python scripts/test-gitops-guest-merge-unit.py
    bin/python scripts/test-gitops-guest-authentik-unit.py

# Use Hermit's pinned Python/uv to create the Python test environment.
dev-setup:
    bin/uv venv --clear --python bin/python3 .venv
    bin/uv pip sync --python .venv/bin/python requirements-dev.lock
    ANSIBLE_LOCAL_TEMP="${TMPDIR:-/tmp}/bootstrap-home-infra-ansible-local" \
      .venv/bin/ansible-galaxy collection install \
      -r ansible/requirements.yml -p ansible/collections --force

# Refresh the hashed Python dependency lock with Hermit's pinned Python and uv.
dev-lock:
    bin/uv pip compile --python bin/python3 --generate-hashes \
      requirements-dev.in --output-file requirements-dev.lock

# Check prerequisites for source implementation and static validation.
doctor:
    bash scripts/dev-doctor.sh source

# Check prerequisites for disposable VM, container, and Molecule tests.
doctor-integration:
    bash scripts/dev-doctor.sh integration

# Build the controller binary.
build:
    mkdir -p bin
    GOCACHE="${TMPDIR:-/tmp}/bootstrap-home-infra-go-cache" go build -o bin/bootstrap ./cmd/bootstrap

# Start the interactive controller with the selected config.
run: build
    ./bin/bootstrap --config config.yaml --env .env

# Reject unresolved merge-conflict markers.
conflict-check:
    @if rg -n '^(<{7}|={7}|>{7})( |$)' --glob '!reports/**' .; then echo "merge-conflict markers found" >&2; exit 1; fi

# Run Go unit tests.
test-go:
    GOCACHE="${TMPDIR:-/tmp}/bootstrap-home-infra-go-cache" go test ./cmd/... ./internal/...

# Render the actual seeded project and validate it with pinned Helm/Terraform.
test-gitops-generated:
    GOCACHE="${TMPDIR:-/tmp}/bootstrap-home-infra-go-cache" GITOPS_TOOL_TEST=1 go test -v ./internal/gitops -run 'TestGeneratedProjectToolsAndCharts|TestTerraformHTTPStateLifecycle'

# Exercise the shipped access proxy with synthetic allowed/denied OIDC users.
test-gitops-access:
    GOCACHE="${TMPDIR:-/tmp}/bootstrap-home-infra-go-cache" GITOPS_DOCKER_TEST=1 go test -v ./internal/gitops -run TestGatewayGroupEnforcement -timeout 5m

# Requires the existing disposable ARM64 QEMU guest (just vm-up/vm-resume).
test-gitops-handoff:
    #!/usr/bin/env bash
    set -Eeuo pipefail
    work_dir="$(mktemp -d /tmp/bootstrap-gitops-handoff.XXXXXX)"
    trap 'rm -rf -- "$work_dir"' EXIT
    GOCACHE=/tmp/bootstrap-home-infra-go-cache GOOS=linux GOARCH=arm64 CGO_ENABLED=0 bin/go build -o "$work_dir/gitops" ./cmd/gitops
    ANSIBLE_CONFIG=ansible/ansible.cfg ANSIBLE_LOCAL_TEMP=/tmp/bootstrap-home-infra-ansible-local \
      .venv/bin/ansible-playbook ansible/playbooks/test-gitops-handoff.yml \
      -i 'localhost,' -e ansible_host=127.0.0.1 -e ansible_port=2222 \
      -e ansible_user=ansible -e ansible_ssh_private_key_file="$PWD/test/keys/test_id_ed25519" \
      -e gitops_binary_path="$work_dir/gitops"

# Verify Go formatting without changing files.
fmt-check:
    @test -z "$(gofmt -l cmd internal)"
    terraform fmt -check -recursive terraform

# Initialize and validate every Terraform root and reusable module.
tf-validate:
    #!/usr/bin/env bash
    set -euo pipefail
    for directory in terraform/environments/bootstrap/* terraform/modules/*; do
        [ -d "$directory" ] || continue
        terraform -chdir="$directory" init -backend=false -input=false
        terraform -chdir="$directory" validate
    done
    scripts/validate-generated-iac.sh

# Syntax-check the complete Ansible stage chain.
syntax-check-all:
    PLAYBOOK=.venv/bin/ansible-playbook; [ -x "$PLAYBOOK" ] || PLAYBOOK="$(command -v ansible-playbook)"; \
    ANSIBLE_LOCAL_TEMP="${TMPDIR:-/tmp}/bootstrap-home-infra-ansible-local" \
      ANSIBLE_REMOTE_TEMP=/tmp/.ansible-bootstrap \
      "$PLAYBOOK" --syntax-check ansible/playbooks/site.yml \
      -i 'bootstrap_target,' \
      -e bootstrap_server_ip=127.0.0.1 \
      -e internal_domain=home.example.com

# Run all inexpensive source gates.
lint-all: conflict-check fmt-check test-go syntax-check-all
    GOCACHE="${TMPDIR:-/tmp}/bootstrap-home-infra-go-cache" go build ./cmd/... ./internal/...
    GOCACHE="${TMPDIR:-/tmp}/bootstrap-home-infra-go-cache" go vet ./cmd/... ./internal/...
    LINTER=.venv/bin/ansible-lint; [ -x "$LINTER" ] || LINTER="$(command -v ansible-lint)"; \
      ANSIBLE_LOCAL_TEMP="${TMPDIR:-/tmp}/bootstrap-home-infra-ansible-local" \
      ANSIBLE_REMOTE_TEMP=/tmp/.ansible-bootstrap \
      "$LINTER" ansible/playbooks ansible/roles
    just tf-validate

# Generate the test SSH keypair once.
generate-test-keys:
    @test ! -e test/keys/test_id_ed25519 || { echo "test key already exists"; exit 1; }
    ssh-keygen -t ed25519 -f test/keys/test_id_ed25519 -N "" -C "bootstrap-test-key"

# Build the base QEMU image.
build-base-image:
    packer init test/images/base.pkr.hcl
    packer build test/images/base.pkr.hcl

# Start the QEMU VM.
vm-up arch="aarch64" accelerator="hvf":
    #!/usr/bin/env bash
    BASE="test/images/output/base-{{arch}}.qcow2"
    SNAP="test/images/run-snapshot.qcow2"
    PID_FILE="${TMPDIR:-/tmp}/bootstrap-home-infra-test-vm.pid"
    test -f "$BASE" || { echo "missing $BASE; run just build-base-image" >&2; exit 1; }
    test ! -f "$PID_FILE" || { echo "VM PID file already exists: $PID_FILE" >&2; exit 1; }
    qemu-img create -f qcow2 -b "$(realpath "$BASE")" -F qcow2 "$SNAP"
    # GitLab packages, images, providers and a guest-local test controller do
    # not fit in the base cloud image's 10 GiB disk. This remains sparse.
    qemu-img resize "$SNAP" 60G
    if [ "{{arch}}" = "aarch64" ]; then
        qemu-system-aarch64 -machine virt -cpu host -bios /opt/homebrew/share/qemu/edk2-aarch64-code.fd \
          -accel {{accelerator}} -m 8192 -drive if=virtio,file="$SNAP",format=qcow2 \
          -netdev user,id=net0,hostfwd=tcp::2222-:22,hostfwd=tcp::15380-:5380,hostfwd=tcp::18200-:8200,hostfwd=tcp::19000-:9000,hostfwd=tcp::18443-:443,hostfwd=tcp::15240-:5240,hostfwd=tcp::16443-:6443 -device virtio-net-pci,netdev=net0 \
          -display none -daemonize -pidfile "$PID_FILE"
    else
        qemu-system-x86_64 -machine q35 -accel {{accelerator}} -m 8192 \
          -drive if=virtio,file="$SNAP",format=qcow2 \
          -netdev user,id=net0,hostfwd=tcp::2222-:22,hostfwd=tcp::15380-:5380,hostfwd=tcp::18200-:8200,hostfwd=tcp::19000-:9000,hostfwd=tcp::18443-:443,hostfwd=tcp::15240-:5240,hostfwd=tcp::16443-:6443 -device virtio-net-pci,netdev=net0 \
          -display none -daemonize -pidfile "$PID_FILE"
    fi
    for _ in $(seq 1 60); do
        if ssh-keyscan -p 2222 localhost 2>/dev/null | ssh-keygen -lf - >/dev/null 2>&1; then
            echo "VM SSH identity is available."
            exit 0
        fi
        sleep 1
    done
    echo "VM SSH did not become ready" >&2
    exit 1

# Resume an existing disposable snapshot after a deliberate interruption.
vm-resume arch="aarch64" accelerator="hvf":
    #!/usr/bin/env bash
    SNAP="test/images/run-snapshot.qcow2"
    PID_FILE="${TMPDIR:-/tmp}/bootstrap-home-infra-test-vm.pid"
    test -f "$SNAP" || { echo "missing $SNAP; run just vm-up first" >&2; exit 1; }
    test ! -f "$PID_FILE" || { echo "VM PID file already exists: $PID_FILE" >&2; exit 1; }
    if [ "{{arch}}" = "aarch64" ]; then
        qemu-system-aarch64 -machine virt -cpu host -bios /opt/homebrew/share/qemu/edk2-aarch64-code.fd \
          -accel {{accelerator}} -m 8192 -drive if=virtio,file="$SNAP",format=qcow2 \
          -netdev user,id=net0,hostfwd=tcp::2222-:22,hostfwd=tcp::15380-:5380,hostfwd=tcp::18200-:8200,hostfwd=tcp::19000-:9000,hostfwd=tcp::18443-:443,hostfwd=tcp::15240-:5240,hostfwd=tcp::16443-:6443 -device virtio-net-pci,netdev=net0 \
          -display none -daemonize -pidfile "$PID_FILE"
    else
        qemu-system-x86_64 -machine q35 -accel {{accelerator}} -m 8192 \
          -drive if=virtio,file="$SNAP",format=qcow2 \
          -netdev user,id=net0,hostfwd=tcp::2222-:22,hostfwd=tcp::15380-:5380,hostfwd=tcp::18200-:8200,hostfwd=tcp::19000-:9000,hostfwd=tcp::18443-:443,hostfwd=tcp::15240-:5240,hostfwd=tcp::16443-:6443 -device virtio-net-pci,netdev=net0 \
          -display none -daemonize -pidfile "$PID_FILE"
    fi
    for _ in $(seq 1 60); do
        if ssh-keyscan -p 2222 localhost 2>/dev/null | ssh-keygen -lf - >/dev/null 2>&1; then
            echo "VM SSH identity is available."
            exit 0
        fi
        sleep 1
    done
    echo "VM SSH did not become ready" >&2
    exit 1

# Start the QEMU VM with an ephemeral secondary disk at /dev/vdb.
vm-up-ssd arch="aarch64" accelerator="hvf":
    #!/usr/bin/env bash
    BASE="test/images/output/base-{{arch}}.qcow2"
    SNAP="test/images/run-snapshot.qcow2"
    SSD="test/images/run-ssd-disk.qcow2"
    PID_FILE="${TMPDIR:-/tmp}/bootstrap-home-infra-test-vm.pid"
    test -f "$BASE" || { echo "missing $BASE; run just build-base-image" >&2; exit 1; }
    test ! -f "$PID_FILE" || { echo "VM PID file already exists: $PID_FILE" >&2; exit 1; }
    qemu-img create -f qcow2 -b "$(realpath "$BASE")" -F qcow2 "$SNAP"
    qemu-img create -f qcow2 "$SSD" 2G
    if [ "{{arch}}" = "aarch64" ]; then
        qemu-system-aarch64 -machine virt -cpu host -bios /opt/homebrew/share/qemu/edk2-aarch64-code.fd \
          -accel {{accelerator}} -m 8192 -drive if=virtio,file="$SNAP",format=qcow2 \
          -drive if=virtio,file="$SSD",format=qcow2 \
          -netdev user,id=net0,hostfwd=tcp::2222-:22,hostfwd=tcp::15380-:5380,hostfwd=tcp::18200-:8200,hostfwd=tcp::19000-:9000,hostfwd=tcp::18443-:443,hostfwd=tcp::15240-:5240,hostfwd=tcp::16443-:6443 -device virtio-net-pci,netdev=net0 \
          -display none -daemonize -pidfile "$PID_FILE"
    else
        qemu-system-x86_64 -machine q35 -accel {{accelerator}} -m 8192 \
          -drive if=virtio,file="$SNAP",format=qcow2 \
          -drive if=virtio,file="$SSD",format=qcow2 \
          -netdev user,id=net0,hostfwd=tcp::2222-:22,hostfwd=tcp::15380-:5380,hostfwd=tcp::18200-:8200,hostfwd=tcp::19000-:9000,hostfwd=tcp::18443-:443,hostfwd=tcp::15240-:5240,hostfwd=tcp::16443-:6443 -device virtio-net-pci,netdev=net0 \
          -display none -daemonize -pidfile "$PID_FILE"
    fi
    for _ in $(seq 1 60); do
        if ssh-keyscan -p 2222 localhost 2>/dev/null | ssh-keygen -lf - >/dev/null 2>&1; then
            echo "VM SSH identity is available."
            exit 0
        fi
        sleep 1
    done
    echo "VM SSH did not become ready" >&2
    exit 1

# Stop the test VM and remove only its ephemeral disk images.
vm-down:
    #!/usr/bin/env bash
    PID_FILE="${TMPDIR:-/tmp}/bootstrap-home-infra-test-vm.pid"
    if [ -f "$PID_FILE" ]; then
        PID="$(<"$PID_FILE")"
        kill "$PID" 2>/dev/null || true
        rm -f "$PID_FILE"
    fi
    rm -f test/images/run-snapshot.qcow2 test/images/run-ssd-disk.qcow2

# Generate strict single-disk test configuration.
write-test-config domain="lab.test" ip="10.0.2.15":
    bash test/write-test-config.sh "{{domain}}" "{{ip}}"

# Generate strict dual-disk test configuration.
write-test-config-ssd domain="lab.test" ip="10.0.2.15":
    bash test/write-test-config.sh "{{domain}}" "{{ip}}" ssd

# Start local Pebble and Cloudflare API mocks.
mocks-up:
    docker compose -f test/mocks/docker-compose.yml up -d --build

# Compatibility name used by the integration guide.
mocks-up-vm: mocks-up

# Stop local mock services.
mocks-down:
    docker compose -f test/mocks/docker-compose.yml down

# Run a Molecule scenario for an exact role directory name.
test-role role:
    test -x .venv/bin/molecule || { echo "run just dev-setup first" >&2; exit 1; }
    cd ansible/roles/{{role}} && ../../../.venv/bin/molecule test

# Run the role's complete Molecule sequence, including idempotence.
test-role-idempotent role:
    test -x .venv/bin/molecule || { echo "run just dev-setup first" >&2; exit 1; }
    cd ansible/roles/{{role}} && ../../../.venv/bin/molecule test

# Run one bootstrap stage against the QEMU VM.
test-stage stage: build
    ACME_SERVER_URL=https://pebble:14000/dir ACME_CA_CERT_URL=http://10.0.2.2:8080/pebble.minica.pem ACME_ISSUING_CA_CERT_URL=https://pebble:15000/roots/0 ACME_SERVER_HOST_IP=10.0.2.2 CLOUDFLARE_API_BASE_URL=http://10.0.2.2:8080 \
      BOOTSTRAP_TECHNITIUM_API_URL=http://127.0.0.1:15380 BOOTSTRAP_VAULT_API_URL=https://127.0.0.1:18200 \
      BOOTSTRAP_AUTHENTIK_API_URL=http://127.0.0.1:19000 BOOTSTRAP_GITLAB_API_URL=https://127.0.0.1:18443/api/v4/ \
      BOOTSTRAP_GITLAB_BASE_URL=https://127.0.0.1:18443 BOOTSTRAP_MAAS_API_URL=http://127.0.0.1:15240/MAAS BOOTSTRAP_API_INSECURE=true \
      ./bin/bootstrap --headless --accept-new-host-key --approve-terraform --stage "{{stage}}" \
      --config test/config.yaml --env test/.env

# Run one stage with the secondary-disk test profile.
test-stage-ssd stage: build
    ACME_SERVER_URL=https://pebble:14000/dir ACME_CA_CERT_URL=http://10.0.2.2:8080/pebble.minica.pem ACME_ISSUING_CA_CERT_URL=https://pebble:15000/roots/0 ACME_SERVER_HOST_IP=10.0.2.2 CLOUDFLARE_API_BASE_URL=http://10.0.2.2:8080 \
      BOOTSTRAP_TECHNITIUM_API_URL=http://127.0.0.1:15380 BOOTSTRAP_VAULT_API_URL=https://127.0.0.1:18200 \
      BOOTSTRAP_AUTHENTIK_API_URL=http://127.0.0.1:19000 BOOTSTRAP_GITLAB_API_URL=https://127.0.0.1:18443/api/v4/ \
      BOOTSTRAP_GITLAB_BASE_URL=https://127.0.0.1:18443 BOOTSTRAP_MAAS_API_URL=http://127.0.0.1:15240/MAAS BOOTSTRAP_API_INSECURE=true \
      ./bin/bootstrap --headless --accept-new-host-key --approve-terraform --stage "{{stage}}" \
      --config test/config-ssd.yaml --env test/.env

# Run the full single-disk bootstrap.
test-bootstrap: build
    ACME_SERVER_URL=https://pebble:14000/dir ACME_CA_CERT_URL=http://10.0.2.2:8080/pebble.minica.pem ACME_ISSUING_CA_CERT_URL=https://pebble:15000/roots/0 ACME_SERVER_HOST_IP=10.0.2.2 CLOUDFLARE_API_BASE_URL=http://10.0.2.2:8080 \
      BOOTSTRAP_TECHNITIUM_API_URL=http://127.0.0.1:15380 BOOTSTRAP_VAULT_API_URL=https://127.0.0.1:18200 \
      BOOTSTRAP_AUTHENTIK_API_URL=http://127.0.0.1:19000 BOOTSTRAP_GITLAB_API_URL=https://127.0.0.1:18443/api/v4/ \
      BOOTSTRAP_GITLAB_BASE_URL=https://127.0.0.1:18443 BOOTSTRAP_MAAS_API_URL=http://127.0.0.1:15240/MAAS BOOTSTRAP_API_INSECURE=true \
      ./bin/bootstrap --headless --accept-new-host-key --approve-terraform --config test/config.yaml --env test/.env

# Run the full secondary-disk bootstrap.
test-bootstrap-ssd: build
    ACME_SERVER_URL=https://pebble:14000/dir ACME_CA_CERT_URL=http://10.0.2.2:8080/pebble.minica.pem ACME_ISSUING_CA_CERT_URL=https://pebble:15000/roots/0 ACME_SERVER_HOST_IP=10.0.2.2 CLOUDFLARE_API_BASE_URL=http://10.0.2.2:8080 \
      BOOTSTRAP_TECHNITIUM_API_URL=http://127.0.0.1:15380 BOOTSTRAP_VAULT_API_URL=https://127.0.0.1:18200 \
      BOOTSTRAP_AUTHENTIK_API_URL=http://127.0.0.1:19000 BOOTSTRAP_GITLAB_API_URL=https://127.0.0.1:18443/api/v4/ \
      BOOTSTRAP_GITLAB_BASE_URL=https://127.0.0.1:18443 BOOTSTRAP_MAAS_API_URL=http://127.0.0.1:15240/MAAS BOOTSTRAP_API_INSECURE=true \
      ./bin/bootstrap --headless --accept-new-host-key --approve-terraform --config test/config-ssd.yaml --env test/.env

# Resume Stage 6, including saved-plan approval and verified GitLab state handoff.
migrate-tf-state: build
    ./bin/bootstrap --headless --approve-terraform --stage gitlab --config config.yaml --env .env

# Collect a read-only adoption draft for later GitLab review.
adopt-server ip: build
    ./bin/bootstrap adopt-server --target "{{ip}}" --config config.yaml --env .env

# Inspect the target-side Restic policy and latest snapshot.
backup-status: build
    ./bin/bootstrap backup status --config config.yaml --env .env

# Trigger the target's application-consistent backup unit.
backup-now: build
    ./bin/bootstrap backup run --config config.yaml --env .env

# Restore into a new isolated drill directory; never targets live paths.
backup-restore snapshot target: build
    ./bin/bootstrap backup restore --snapshot "{{snapshot}}" --target "{{target}}" --config config.yaml --env .env

# Repair GitLab service state while keeping Terraform/GitOps frozen.
recover-gitlab-service incident: build
    ./bin/bootstrap recover gitlab --mode service --incident "{{incident}}" --config config.yaml --env .env

# Destructively restore live GitLab only after typing the same incident ID twice.
recover-gitlab-restore incident snapshot="latest": build
    ./bin/bootstrap recover gitlab --mode restore --incident "{{incident}}" --snapshot "{{snapshot}}" --approve-restore "{{incident}}" --config config.yaml --env .env

# Resume GitLab Runner and IaC only after the recovery verification gate passed.
recover-gitlab-unfreeze incident: build
    ./bin/bootstrap recover gitlab --unfreeze "{{incident}}" --config config.yaml --env .env

# Inspect active certificate identity, expiry, and renewal timer.
certificate-status: build
    ./bin/bootstrap certificate status --config config.yaml --env .env

# Trigger staged renewal with validation, atomic activation, and rollback.
certificate-renew: build
    ./bin/bootstrap certificate renew --config config.yaml --env .env

# Serve the repository review with Hermit's pinned Python.
report-serve port="8765":
    bin/python -m http.server {{port}} --bind 127.0.0.1 --directory reports
