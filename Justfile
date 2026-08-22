set shell := ["bash", "-euo", "pipefail", "-c"]

default:
    @just --list

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

# Verify Go formatting without changing files.
fmt-check:
    @test -z "$(gofmt -l cmd internal)"
    terraform fmt -check -recursive terraform

# Initialize and validate every Terraform root and reusable module.
tf-validate:
    #!/usr/bin/env bash
    set -euo pipefail
    for directory in terraform/environments/bootstrap terraform/modules/*; do
        [ -d "$directory" ] || continue
        terraform -chdir="$directory" init -backend=false -input=false
        terraform -chdir="$directory" validate
    done

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
    if [ "{{arch}}" = "aarch64" ]; then
        qemu-system-aarch64 -machine virt -cpu host -bios /opt/homebrew/share/qemu/edk2-aarch64-code.fd \
          -accel {{accelerator}} -m 2048 -drive if=virtio,file="$SNAP",format=qcow2 \
          -netdev user,id=net0,hostfwd=tcp::2222-:22 -device virtio-net-pci,netdev=net0 \
          -display none -daemonize -pidfile "$PID_FILE"
    else
        qemu-system-x86_64 -machine q35 -accel {{accelerator}} -m 2048 \
          -drive if=virtio,file="$SNAP",format=qcow2 \
          -netdev user,id=net0,hostfwd=tcp::2222-:22 -device virtio-net-pci,netdev=net0 \
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
          -accel {{accelerator}} -m 2048 -drive if=virtio,file="$SNAP",format=qcow2 \
          -drive if=virtio,file="$SSD",format=qcow2 \
          -netdev user,id=net0,hostfwd=tcp::2222-:22 -device virtio-net-pci,netdev=net0 \
          -display none -daemonize -pidfile "$PID_FILE"
    else
        qemu-system-x86_64 -machine q35 -accel {{accelerator}} -m 2048 \
          -drive if=virtio,file="$SNAP",format=qcow2 \
          -drive if=virtio,file="$SSD",format=qcow2 \
          -netdev user,id=net0,hostfwd=tcp::2222-:22 -device virtio-net-pci,netdev=net0 \
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
write-test-config domain="lab.test" ip="127.0.0.1":
    bash test/write-test-config.sh "{{domain}}" "{{ip}}"

# Generate strict dual-disk test configuration.
write-test-config-ssd domain="lab.test" ip="127.0.0.1":
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
    ACME_SERVER_URL=https://127.0.0.1:14000/dir CLOUDFLARE_API_BASE_URL=http://127.0.0.1:8080 \
      ./bin/bootstrap --headless --accept-new-host-key --stage "{{stage}}" \
      --config test/config.yaml --env test/.env

# Run one stage with the secondary-disk test profile.
test-stage-ssd stage: build
    ACME_SERVER_URL=https://127.0.0.1:14000/dir CLOUDFLARE_API_BASE_URL=http://127.0.0.1:8080 \
      ./bin/bootstrap --headless --accept-new-host-key --stage "{{stage}}" \
      --config test/config-ssd.yaml --env test/.env

# Run the full single-disk bootstrap.
test-bootstrap: build
    ACME_SERVER_URL=https://127.0.0.1:14000/dir CLOUDFLARE_API_BASE_URL=http://127.0.0.1:8080 \
      ./bin/bootstrap --headless --accept-new-host-key --config test/config.yaml --env test/.env

# Run the full secondary-disk bootstrap.
test-bootstrap-ssd: build
    ACME_SERVER_URL=https://127.0.0.1:14000/dir CLOUDFLARE_API_BASE_URL=http://127.0.0.1:8080 \
      ./bin/bootstrap --headless --accept-new-host-key --config test/config-ssd.yaml --env test/.env

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

# Inspect active certificate identity, expiry, and renewal timer.
certificate-status: build
    ./bin/bootstrap certificate status --config config.yaml --env .env

# Trigger staged renewal with validation, atomic activation, and rollback.
certificate-renew: build
    ./bin/bootstrap certificate renew --config config.yaml --env .env
