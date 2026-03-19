# ── Project Tasks ──────────────────────────────────────────────────────────────

# List available tasks
default:
    @just --list

# ── Test Infrastructure ────────────────────────────────────────────────────────

# Generate test SSH keypair (run once before first test)
generate-test-keys:
    ssh-keygen -t ed25519 -f test/keys/test_id_ed25519 -N "" -C "bootstrap-test-key"
    @echo "Keys generated. Rebuild the base image to bake in the new public key."

# Build the base QEMU test image via Packer
build-base-image:
    packer init test/images/base.pkr.hcl
    packer build test/images/base.pkr.hcl

# Start test mock servers (Pebble + Cloudflare mock)
mocks-up:
    docker compose -f test/mocks/docker-compose.yml up -d --build

# Stop test mock servers
mocks-down:
    docker compose -f test/mocks/docker-compose.yml down

# Run Molecule tests for a specific Ansible role
test-role role:
    cd ansible/roles/{{role}} && molecule test

# Run a single bootstrap stage against a fresh QEMU VM
test-stage stage:
    ACME_SERVER_URL=https://localhost:14000/dir \
    CLOUDFLARE_API_BASE_URL=http://localhost:8080 \
    ./bin/bootstrap --headless --stage {{stage}}

# Run the full bootstrap end-to-end
test-bootstrap:
    ACME_SERVER_URL=https://localhost:14000/dir \
    CLOUDFLARE_API_BASE_URL=http://localhost:8080 \
    ./bin/bootstrap --headless
