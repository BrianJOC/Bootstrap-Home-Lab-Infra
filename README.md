# bootstrap-home-infra

A multi-stage bootstrap system for a self-hosted homelab. Targets a single Ubuntu server (x86 or ARM) and brings up a fully integrated infrastructure platform driven by Ansible, Terraform, and GitOps.

Implementation and testing may proceed under the standing defaults in
[`docs/implementation-working-agreement.md`](docs/implementation-working-agreement.md).
Repository agents also read [`AGENTS.md`](AGENTS.md) so settled decisions and
safe local-test authority do not need to be re-established each session.

## Architecture & Components

### TLDR

This tool turns a bare Ubuntu server into a self-hosted homelab platform: internal DNS, SSO, secrets management, a GitOps pipeline, and bare-metal provisioning — all wired together and managed as code. Every service gets a browser-trusted HTTPS certificate. Nothing is publicly exposed. Once the bootstrap sequence completes, ongoing operations shift entirely to GitOps through GitLab.

---

### The network model: private services, publicly trusted TLS

The central design goal is a **fully private network that doesn't feel private**. Every internal service gets a real hostname (`vault.home.yourdomain.com`) that resolves to a private IP on your network, backed by a certificate that browsers trust out of the box. No certificate warnings. No "add security exception." No custom CA to push to every device.

This works because TLS trust and network reachability are separate concerns.

**How your devices find internal services:**
Technitium DNS runs on your server and acts as the authoritative resolver for your private domain. Bootstrap initially limits the change to the server and homelab network; it does not rewrite router settings or interrupt the rest of the home. After validation, the included UniFi guide walks through advertising Technitium as the DNS resolver to each additional home network, one network at a time. When a device asks "where is `vault.home.yourdomain.com`?", Technitium answers with the private IP. Devices off your network get nothing — there are no public DNS records for those hostnames.

**How the TLS certificate gets issued without exposing anything:**
Let's Encrypt supports a challenge type called DNS-01: instead of connecting to your server to verify ownership, it reads a temporary TXT record you add to your domain's public DNS. The flow:

1. lego (the ACME client) calls the Cloudflare API: *"Create TXT record `_acme-challenge._.home.yourdomain.com = <token>`"*
2. Let's Encrypt queries public DNS, finds the TXT record, and issues the certificate
3. lego calls the Cloudflare API again: *"Delete that TXT record"*
4. The cert is stored on your server and distributed via Vault

Your server never receives an inbound connection from the internet. The only outbound call is the Cloudflare API call to create and delete the temporary TXT record. Once that's done, the TXT record disappears.

**What's actually in Cloudflare's public DNS:**
Your domain (`yourdomain.com`) is registered and managed through Cloudflare — this is required because DNS-01 needs to write records to your domain's authoritative DNS. But your internal service hostnames (`vault.home.yourdomain.com`, `gitlab.home.yourdomain.com`, etc.) have **no public A records**. From the internet they return NXDOMAIN. Even knowing the hostname, an external party cannot reach your services.

> **Important:** You need a real, publicly registered domain managed in Cloudflare. A fake TLD like `.lab` or `.internal` won't work — Let's Encrypt needs to be able to validate the TXT record in actual public DNS.

**The issued certificate is a wildcard** (`*.home.yourdomain.com`), which means one certificate covers all internal services. Services don't each need their own cert — they all use the same one, fetched from Vault.

---

### Why each component exists

#### Bootstrap tool — Go + Bubbletea + Lipgloss

The bootstrap tool is a single binary. It runs on your laptop, connects to the target server over SSH, and drives the entire install sequence. A terminal UI (Bubbletea + Lipgloss) guides you through configuration on first run and shows live stage progress and log output.

Go was chosen so the tool compiles to a self-contained binary — no Python environment, no Node, no runtime dependencies to manage. It runs on x86 or ARM without code changes.

Every stage is idempotent: before doing anything, it checks whether the service is already in a healthy state. Re-running after a partial failure picks up where it left off.

#### Ansible

Ansible handles the imperative parts of service installation — things that are inherently procedural: downloading binaries, writing config files, starting Docker containers, calling service APIs to set initial state. It's driven by the bootstrap tool over SSH and is not intended to be used directly.

#### Technitium DNS — Stage 1

Technitium is a full-featured DNS server deployed as a Docker container. It's the authoritative resolver for your private domain within your network.

It comes first in the bootstrap sequence because every subsequent service is reached by hostname. Vault, GitLab, Authentik — all of them need DNS resolution before they can be installed, configured, and wired to each other.

After bootstrap, Terraform automatically creates new DNS records when services are deployed.

#### TLS via lego — Stage 2

[lego](https://github.com/go-acme/lego) is an ACME client, similar in purpose to certbot but distributed as a single Go binary with no external dependencies. It issues the wildcard certificate described above.

lego was chosen over certbot because it installs cleanly via Ansible without managing a Python virtualenv, certbot plugins, or OS package conflicts. One binary, one install step.

The host runs a daily systemd eligibility check and automatically renews the certificate when it is within 30 days of expiry. A zone-scoped Cloudflare token is retained as a root-owned mode `0600` runtime secret so renewal does not depend on GitLab or an unsealed Vault. New material is validated, activated atomically, and rolled back if any consumer fails its reload health check. Vault synchronization may be deferred until unseal. The schedule and renewal window are configurable, and automation can be disabled with persistent expiry warnings. See [certificate renewal and deployment](docs/certificate-renewal.md).

#### HashiCorp Vault — Stage 3

Vault is the authoritative source of truth for platform credentials, API tokens, TLS certificates, and service-to-service authentication. For v1, core host services may also retain the minimum local runtime copies they need in root-owned files with mode `0600`. This keeps an ordinary reboot from making every service dependent on Vault being unsealed first, while still centralizing secret inventory and rotation in Vault.

Vault uses **Shamir secret sharing** for unseal: on first initialization, it outputs 5 key shares to `vault-init.json`. You need 3 of those shares to unseal Vault after any restart. This is a deliberate homelab tradeoff — automated unseal would add external dependencies. Store the recovery file outside the repository and back it up in a password manager or another secure location. Bootstrap preserves `.env` and `~/.bootstrap` for safe re-runs unless the operator later requests an explicit cleanup.

Vault Agent is an optional post-v1 delivery mechanism for selected host services. Future k3s workloads should prefer Kubernetes authentication with Vault Agent Injector and ephemeral pod-local rendering. SPIFFE/SPIRE is intentionally deferred until a multi-node workload-identity or service-mTLS requirement justifies another identity control plane.

#### PostgreSQL — Stage 4

GitLab, Authentik, and MAAS all need a relational database. Rather than running three separate database servers, a shared PostgreSQL instance supplies a dedicated database and role to each service. On homelab hardware, this reduces memory and backup overhead without sharing credentials between applications.

PostgreSQL is installed before Authentik so Authentik starts directly against its final database. There is no temporary bundled database or bootstrap migration.

#### Authentik — Stage 5

Authentik is a self-hosted identity provider. It gives you:
- **Single sign-on** — one login across all web-based services
- **OIDC/OAuth2** — Vault, GitLab, and other services delegate authentication to Authentik
- **Group-based access control** — define groups (`adults`, `kids`, etc.) and enforce access per service

Without Authentik, each service would have its own separate user database and login flow. With Authentik, you manage users in one place and access follows group membership everywhere.

#### GitLab + Runner — Stage 6

GitLab is the destination the bootstrap tool is working toward. Once it's running:

- Terraform state migrates from local files to the GitLab HTTP backend
- Service configuration is expressed as code in GitLab repositories
- GitLab CI/CD pipelines apply changes when merge requests are merged
- GitLab Runner executes pipelines and pulls secrets from Vault via JWT auth — no secrets stored in GitLab itself

This is the handoff point. After Stage 6, the bootstrap tool is no longer
needed for routine ongoing operations; it remains the recovery and deliberate
break-glass interface. Normal changes are driven by GitLab.

#### MAAS — Stage 7

MAAS (Metal as a Service) handles bare-metal server provisioning. When you add a new physical server to your lab, MAAS PXE-boots it, enumerates its hardware, and deploys the reviewed Ubuntu series. `just adopt-server <ip>` performs a pinned-SSH read-only audit, displays the exact node definition and OS-replacement warning, requires approval, and opens a draft GitLab MR. It refreshes its short-lived GitLab credential over the pinned bootstrap-host connection when needed and refuses duplicate definitions or open adoption drafts. After merge, Terraform registers/commissions the machine and uses the separate MAAS instance lifecycle to deploy it; k3s join waits for deliberate confirmation of the replacement OS SSH fingerprint.

MAAS can take over DHCP for one dedicated lab VLAN, ensuring new servers get consistent IP assignments. This is disabled by default and requires an explicit cutover after the tool verifies the VLAN, range, Technitium, and absence of another DHCP authority. UniFi continues to provide DHCP to ordinary home networks.

For a single-server setup, MAAS may feel like overkill. It earns its place when you start adding physical machines — it turns "SSH in and reinstall Ubuntu by hand" into "merge a pull request."

#### k3s and Terraform (post-bootstrap)

k3s is a lightweight Kubernetes distribution for containerized workloads. Clusters are defined as code; the Terraform Helm provider manages deployments. When you define a new service, Terraform creates the DNS record, registers the Authentik OIDC client, and deploys the Helm chart — all in a single pipeline run.

---

### The bootstrap-then-GitOps handoff

There's a chicken-and-egg problem at the center of any GitOps homelab: you can't store the GitLab configuration in GitLab before GitLab exists. The bootstrap tool is the one-time solution to that problem.

```
Bootstrap runs → GitLab comes up → Terraform state migrates to GitLab → GitOps takes over
```

After that handoff, the day-to-day workflow is:
- Define a service in code → open a merge request → merge → pipeline deploys it
- Add a server → `just adopt-server <ip>` → review the displayed definition and OS replacement → merge → the pipeline registers, commissions, and deploys it in MAAS → confirm the replacement SSH fingerprint before cluster join
- Rotate a secret → update in Vault → services pick it up on restart

The bootstrap tool is explicitly designed not to be needed in the normal
post-handoff loop. It remains useful for re-running a failed stage, recovering
the platform or controller, and bootstrapping a new server from scratch.

---

## What it sets up

| Stage | Service | Purpose |
|-------|---------|---------|
| 0 | Config collection | Validate configuration and write config files |
| 0b | Storage setup | Optional secondary disk (SSD hat) — format, mount, redirect Docker data |
| 1 | Technitium DNS | Internal name resolution for all services |
| 2 | TLS (lego + Cloudflare) | DNS-01 cert issuance; certs stored in Vault |
| 3 | HashiCorp Vault | Secrets source of truth; Shamir unseal |
| 4 | PostgreSQL | Shared server with isolated DB and role per dependent service |
| 5 | Authentik | SSO/OIDC for all services; starts on its final PostgreSQL DB |
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
| `ssh_host_fingerprint` | *(optional)* Expected SHA-256 host fingerprint for strict first use or headless replacement |
| `internal_domain` | Private DNS zone under a public domain you control (e.g. `home.example.com`) |
| `cloudflare_zone_id` | Cloudflare Zone ID for your domain |
| `bootstrap_server_ip` | IP of this machine (used in Ansible inventory) |
| `lab_vlan_subnet` | CIDR for the lab network (e.g. `10.0.0.0/24`) |
| `lab_gateway` | Gateway address on the lab network |
| `lab_vlan_id` | *(optional)* Dedicated lab VLAN tag; `0` means untagged |
| `lab_vlan_interface` | *(required for MAAS DHCP)* Target interface that receives the lab VLAN |
| `dhcp_range_low`, `dhcp_range_high` | *(required for MAAS DHCP)* Dynamic range contained by the lab subnet |
| `maas_dhcp_enabled` | *(optional, default `false`)* Start the explicit MAAS DHCP cutover |
| `backup_enabled` | *(optional)* Configure automated encrypted Restic backups; wizard recommends enabling |
| `backup_repository` | Restic repository path or URI, such as a mounted TrueNAS dataset |
| `backup_on_calendar` | Backup schedule; default is daily at 02:30 local time |
| `backup_keep_daily`, `backup_keep_weekly`, `backup_keep_monthly` | Retention values; defaults are `7`, `5`, and `12` |
| `backup_usb_*` | *(optional)* Removable offline repository and copy/reminder interval |
| `controller_recovery_enabled` | Create a portable encrypted replacement-laptop bundle; default `true` |
| `controller_recovery_refresh_after_change` | Refresh the bundle after material controller/configuration changes; default `true` |
| `controller_recovery_include_ssh_private_key` | Include the current SSH key inside a second encrypted envelope; default `true` |
| `admin_email` | Email for ACME registration and Authentik admin |
| `vault_init_output_path` | Absolute path outside the repository where Vault recovery material will be written |
| `data_disk` | *(optional)* Secondary disk device (e.g. `/dev/sda`) — see below |

**`.env`** (secrets — never commit):

```
CF_DNS_API_TOKEN=your-cloudflare-dns-api-token
BOOTSTRAP_ADMIN_PASSWORD=your-admin-password
# Optional when the corresponding backup repository is configured:
# RESTIC_PRIMARY_PASSWORD=your-primary-repository-password
# RESTIC_USB_PASSWORD=your-usb-repository-password
# CONTROLLER_RECOVERY_PASSWORD=separate-password-manager-secret
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
bin/just dev-setup      # Hermit Python/uv + hashed Python lock + pinned collections
bin/just doctor         # verify source-development prerequisites
just build              # build binary to bin/bootstrap
just test-go            # run Go unit tests
just lint-all           # go vet + terraform validate + ansible-lint
just tf-validate        # validate all Terraform modules
just syntax-check-all   # syntax-check the complete Ansible stage chain
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
just test-role technitium_dns               # full Molecule test for a role
just test-role-idempotent vault_install     # converge twice, verify zero changed tasks
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

Technitium remains homelab-scoped by default. To make it the DNS resolver for
additional UniFi networks after bootstrap, follow
[`docs/unifi-network-guide.md`](docs/unifi-network-guide.md). The staged design,
validation gates, and MAAS DHCP rollback contract are in
[`docs/network-cutover.md`](docs/network-cutover.md).

### Backups

The recommended backup profile writes encrypted, deduplicated Restic snapshots
to a dedicated TrueNAS dataset every night. Retention defaults to 7 daily, 5
weekly, and 12 monthly snapshots. An optional USB repository provides a normally
disconnected second copy.

The wizard presents these defaults before enabling them. Backups may be skipped
with `backup_enabled: false`, and every schedule, retention value, integrity
check, USB cadence, and restore reminder can be changed. See
[`docs/backup-recovery.md`](docs/backup-recovery.md) and the
[`TrueNAS setup guide`](docs/truenas-backup-guide.md).

For a first evaluation, **Skip for now — trial mode** is supported. It requires
no NAS, USB device, repository, or backup password and does not block bootstrap.
The platform clearly remains marked as unprotected, warns before destructive
maintenance, and can be enabled later by updating the accepted backup fields
and re-running the lifecycle stage.
Preserving Vault recovery material in the password manager is still required.

### Reboot and Vault unseal

After a host reboot, Technitium, PostgreSQL, Authentik, GitLab, MAAS, and eligible
k3s workloads start from their minimum protected local runtime credentials.
Vault starts sealed and the platform reports a degraded state until the operator
runs `bootstrap vault unseal`.

Vault-dependent CI, secret changes, fresh Vault Agent delivery, and a current
Vault Raft backup remain paused while sealed. Successful unseal triggers
reconciliation of Vault health, OIDC/JWT integration, runtime secret versions,
and any missed Vault backup. See
[`docs/reboot-recovery.md`](docs/reboot-recovery.md).

### Upgrades and maintenance windows

Ubuntu security patches install automatically, but the host never reboots
automatically. Renovate proposes pinned application upgrades through GitLab for
normal review. Unreviewed proposals become policy-approved after configurable
deadlines—3 days for critical security, 7 for other security, 21 for patch or
minor releases, and 30 for major releases—and run in the next maintenance
window. The default window is Sunday at 03:00 local time.

Time-based approval never bypasses compatibility, health, backup, migration, or
rollback gates. Trial mode permits automatic patch/minor changes only for
stateless, safely rollbackable workloads; data-bearing and major upgrades wait
for verified backup protection. See
[`docs/upgrade-policy.md`](docs/upgrade-policy.md).

### Constrained observability

Uptime Kuma and Beszel run entirely on the target host. The immediately active
health adapter performs functional checks and configurable
Discord/Telegram/ntfy/webhook paging without requiring either dashboard's
first-user setup. Kuma can then provide visual monitors and incident history;
Beszel provides lightweight CPU, memory, disk, network, and container history
after the operator creates its first user and adds the preconfigured local
agent.

Paging is recommended but optional; dashboard-only mode is supported. Kuma
history is retained for seven days, logs remain local for seven days, and
Prometheus, Grafana, Loki, and tracing are not installed. The combined stack
defaults to a 512 MiB memory, half-core CPU, and 5 GiB state budget. TrueNAS
remains storage-only and never runs telemetry compute. Because the monitor runs
on the monitored host, a total host, power, or internet failure cannot page.
See [`docs/observability.md`](docs/observability.md).

### GitLab and GitOps disaster recovery

The nightly Restic set includes a native GitLab backup plus its separately
required configuration, secrets, TLS/SSH identity, Runner metadata, exact
version/edition manifest, and verified package artifact. After every successful
infrastructure apply, the target also creates an encrypted critical checkpoint
containing Git bundles and decrypted Terraform state for the applied commit.

GitLab loss freezes Runner pickup and every Terraform/GitOps mutation. A
host-side recovery flow restores the exact GitLab version independently of CI,
reconciles any newer critical checkpoint by commit, state lineage, and serial,
then requires full verification and an explicit unfreeze. Nightly protection
targets ordinary GitLab content within 24 hours; critical GitOps code and state
target the last successful apply. See
[`docs/gitlab-disaster-recovery.md`](docs/gitlab-disaster-recovery.md).

### Replacing the administrator laptop

A blank replacement laptop can recover control using the password manager, a
verified TrueNAS or USB Restic repository, target access, and a verified
bootstrap release. The encrypted controller bundle contains portable
configuration, the pinned server fingerprint, backend and ownership metadata,
run manifests, and a verified bootstrap artifact. The current controller SSH
key is included by default inside a second encrypted envelope whose distinct
password remains only in the password manager. Vault unseal shares and
`vault-init.json` are never collected into the bundle automatically.

Recovery reconstructs local paths and protected files, verifies server identity,
and performs a read-only platform audit before the replacement laptop may
explicitly adopt the target. Normal recovery preserves the old controller's
credentials with a warning. An explicit lost-or-compromised mode enrolls a new
SSH key and rotates controller-specific tokens without removing working access
until replacements are verified. See
[`docs/controller-recovery.md`](docs/controller-recovery.md).

## Security notes

- `vault-init.json` contains Vault unseal keys and the initial root token — store it outside the repository and back it up securely
- Vault is authoritative, but v1 core services may keep minimum root-owned `0600` runtime copies of required secrets
- `.env` and `~/.bootstrap` are preserved by default for recovery and safe re-runs; a future explicit cleanup flow may remove obsolete staging credentials
- Manual Shamir unseal is the v1 default; automatic unseal is a future optional profile
- A sealed Vault after reboot degrades secret-dependent automation but does not take household DNS or the primary web platform offline
- First SSH use requires fingerprint confirmation and is pinned for Go and Ansible; changed keys require an explicit in-workflow replacement confirmation
- Bootstrap does not change UniFi automatically; whole-home DNS is rolled out one network at a time after Technitium is healthy
- A single Technitium instance is an accepted v1 availability risk; do not advertise a public resolver beside it as a client-side fallback
- Restic repository passwords are backed up in the operator's password manager and may retain a root-owned `0600` runtime copy for unattended backups
- The online TrueNAS repository is complemented by an optional disconnected USB repository; NAS snapshots alone are not the offline copy
- Trial mode is allowed without a backup repository, but same-host checkpoints are never represented as protection from disk or host loss
- Upgrade deadlines can replace human approval but never safety gates; trial mode blocks unattended data-bearing and major upgrades
- Monitoring and paging run only on the target; TrueNAS stores backups but never executes telemetry
- Paging can be disabled without disabling the private dashboards, and alert credentials use protected `0600` runtime files
- GitLab outages freeze IaC mutation; critical decrypted state exports exist only in protected tmpfs before encrypted Restic checkpointing
- Controller recovery requires explicit adoption; a recently active old controller produces a warning rather than silent takeover
- The backed-up controller SSH key has encryption separate from Restic, while Vault unseal material remains only in operator-controlled custody
- The Cloudflare API token is the only external credential required by the core bootstrap; optional backups add encrypted Restic repository credentials
- All internal services communicate over the private network; nothing is exposed publicly
