# bootstrap-home-infra

A multi-stage bootstrap system for a self-hosted homelab. Targets a single Ubuntu server (x86 or ARM) and brings up a fully integrated infrastructure platform driven by Ansible, Terraform, and GitOps.

## What it sets up

| Stage | Service | Purpose |
|-------|---------|---------|
| 0 | Config collection | Validate configuration and write config files |
| 0b | Storage setup | Optional secondary disk (SSD hat) — format, mount, redirect Docker data |
| 1 | Technitium DNS | Internal name resolution for all services |
| 2 | TLS (lego + Cloudflare) | DNS-01 cert issuance; certs stored in Vault |
| 3 | HashiCorp Vault | Secrets source of truth; Shamir unseal |
| 4 | Authentik | SSO/OIDC for all services |
| 5 | PostgreSQL | Shared DB instance (GitLab, Authentik, MAAS) |
| 6 | GitLab + Runner | GitOps platform; Terraform state backend |
| 7 | MAAS | Bare-metal server provisioning |
| 8 | k3s | Kubernetes installation via Ansible |

No services are publicly exposed. TLS is issued via Cloudflare DNS-01 — no inbound port required.

## Prerequisites

- Ubuntu target server with SSH access and passwordless sudo
- Cloudflare account managing your domain (for DNS-01 TLS)
- Go 1.26.1+ (to build the bootstrap binary)
- Ansible + Molecule (for role testing)
- Terraform (for post-bootstrap GitOps wiring)

## Quick start

### First run (interactive wizard)

```bash
go build -o bin/bootstrap ./cmd/bootstrap
./bin/bootstrap
```

The TUI wizard collects all required configuration and writes `config.yaml` and `.env` before launching the stage runner.

### Re-run with existing config

```bash
./bin/bootstrap
```

The TUI shows a preamble with your current config and asks whether to proceed or re-enter the wizard.

### Force wizard mode

```bash
./bin/bootstrap --wizard
```

### Headless mode (CI / re-runs)

```bash
./bin/bootstrap --headless
./bin/bootstrap --headless --stage dns   # single stage
```

## Configuration

Copy `config.yaml.example` to `config.yaml` and fill in your values. Secrets go in `.env`.

**`config.yaml`** (structural config — safe to commit if values are not sensitive):

| Field | Description |
|-------|-------------|
| `target_host` | IP or hostname of the bootstrap target |
| `ssh_user` | SSH user (must have passwordless sudo) |
| `ssh_key_path` | Path to SSH private key |
| `internal_domain` | Private domain (e.g. `home.lab`) |
| `cloudflare_zone_id` | Cloudflare Zone ID for your domain |
| `bootstrap_server_ip` | IP of the target bootstrap server (service binding and database access) |
| `lab_vlan_subnet` | CIDR for the lab network (e.g. `10.0.0.0/24`) |
| `admin_email` | Email for ACME registration and Authentik admin |
| `vault_init_output_path` | Where to write Vault unseal keys (default: `./vault-init.json`) |
| `data_disk` | *(optional)* Secondary disk device (e.g. `/dev/sda`) — see below |

**`.env`** (secrets — never commit):

```
CF_DNS_API_TOKEN=your-cloudflare-dns-api-token
BOOTSTRAP_ADMIN_PASSWORD=your-admin-password
```

### Secondary disk (Raspberry Pi SSD hat)

Set `data_disk` in `config.yaml` to redirect Docker volumes and all service data off the SD card:

```yaml
data_disk: "/dev/sda"
data_disk_mode: "full"       # full | mount_only
data_disk_force_format: false
```

## Development

Run these commands from the repository root:

```bash
go build -o bin/bootstrap ./cmd/bootstrap
go test ./...
go vet ./...
just --list            # tasks available in the current Justfile
```

### Test infrastructure

```bash
just generate-test-keys          # once per environment
just build-base-image            # build Ubuntu qcow2 image via Packer
just mocks-up                    # start local Pebble + Cloudflare mocks
bash test/write-test-config.sh   # write test/config.yaml and test/.env
```

Start and configure the QEMU VM separately; the current Justfile has no VM
lifecycle recipes. Once the target and mock endpoints are reachable, pass the
test file paths explicitly:

```bash
ACME_SERVER_URL=https://localhost:14000/dir \
CLOUDFLARE_API_BASE_URL=http://localhost:8080 \
./bin/bootstrap --headless --config test/config.yaml --env test/.env --stage dns
```

The mock URLs must be reachable from the machine using them; adjust hostnames
and port forwarding for your VM setup. The `test-stage` and `test-bootstrap`
recipes use the default `./config.yaml` and `./.env`, not the files under `test/`.
For a configured secondary disk, the stage name is `storage-setup`.

### Molecule (per-role testing)

Use the role directory name:

```bash
just test-role technitium_dns
just test-role vault_install
```

## Directory structure

```
cmd/bootstrap/      Go binary entry point
internal/
  config/           Config loading, validation, wizard result mapping
  tui/              Bubbletea TUI (wizard + stage progress)
  stages/           Stage implementations (one file per stage)
  stage/            Stage runner interface
  ansible/          Ansible runner wrapper
  ssh/              SSH client
ansible/roles/      Ansible roles (one per service)
terraform/
  modules/          Reusable Terraform modules
  environments/     bootstrap (local state) + post-bootstrap (GitLab backend)
test/
  images/           Packer config for QEMU base image
  mocks/            Pebble ACME mock + Cloudflare API mock
docs/superpowers/   Design specs and implementation plans
```

## Post-bootstrap

After Stage 6 (GitLab) is running, configure the GitLab HTTP backend and export
its credentials before migrating Terraform state:

```bash
terraform -chdir=terraform/environments/bootstrap init -migrate-state
```

See `terraform/environments/post-bootstrap/README.md` for details.

## Security notes

- `vault-init.json` contains Vault unseal keys and root token — store it securely, it is gitignored
- The Cloudflare API token is the only externally-sensitive credential required during bootstrap
- All internal services communicate over the private network; nothing is exposed publicly

## Implementation notes

See [current implementation notes](docs/notes.md) for the October 14, 2026
bootstrap fixes, configuration behavior, and remaining verification work.
