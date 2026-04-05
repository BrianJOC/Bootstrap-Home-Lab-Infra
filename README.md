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

No services are publicly exposed. TLS is issued via Cloudflare DNS-01 — no inbound port required.

## Prerequisites

- Ubuntu target server with SSH access and passwordless sudo
- Cloudflare account managing your domain (for DNS-01 TLS)
- Go 1.22+ (to build the bootstrap binary)
- Ansible + Molecule (for role testing)
- Terraform (for post-bootstrap GitOps wiring)

## Quick start

### First run (interactive wizard)

```bash
just build
./bin/bootstrap
```

The TUI wizard collects all required configuration and writes `config.yaml` and `.env` before launching the stage runner.

### Re-run with existing config

```bash
just run
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
| `bootstrap_server_ip` | IP of this machine (used in Ansible inventory) |
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

```bash
just build              # build binary to bin/bootstrap
just test-go            # run Go unit tests
just lint-all           # go vet + terraform validate + ansible-lint
just tf-validate        # validate all Terraform modules
just syntax-check-all   # ansible-lint all roles
```

### Testing against a QEMU VM

```bash
just generate-test-keys          # once per environment
just build-base-image            # build Ubuntu qcow2 image via Packer
just vm-up                       # start VM (SSH on localhost:2222)
just write-test-config           # write test/config.yaml
just mocks-up-vm                 # start Pebble ACME + Cloudflare mock on VM
just test-stage dns              # run a single stage against the VM
just test-bootstrap              # run full end-to-end
just vm-down                     # stop VM
```

With a secondary disk attached (SSD hat tests):

```bash
just vm-up-ssd                   # start VM with /dev/vdb attached
just test-stage-ssd storage      # run storage stage with SSD config
just test-bootstrap-ssd          # full end-to-end with SSD
```

### Molecule (per-role idempotency testing)

```bash
just test-role dns               # full molecule test for a role
just test-role-idempotent vault  # converge twice, verify zero changed tasks
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

After Stage 6 (GitLab) is running, migrate Terraform state to the GitLab HTTP backend:

```bash
just migrate-tf-state
```

See `terraform/environments/post-bootstrap/README.md` for details.

## Security notes

- `vault-init.json` contains Vault unseal keys and root token — store it securely, it is gitignored
- The Cloudflare API token is the only externally-sensitive credential required during bootstrap
- All internal services communicate over the private network; nothing is exposed publicly
