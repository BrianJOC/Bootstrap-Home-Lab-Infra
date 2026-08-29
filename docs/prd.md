# PRD: bootstrap-home-infra

> Status: Decisions complete; no known P0/P1 source blocker; external acceptance remains
> Last updated: 2026-08-27

Implementation note: the full single-host bootstrap, exclusive resource
ownership, GitLab state handoff, rerun/resume, reboot reconciliation,
manifest-backed backup/restore, destructive full GitLab recovery with explicit
unfreeze, controller recovery, and adoption draft MR are proven on the ARM64
disposable environment. Success criterion 21 is now integration verified.
For criterion 6, the reviewed pipeline now models both MAAS
registration/commissioning and the provider's separate OS deployment lifecycle;
physical PXE proof and post-deployment SSH fingerprint confirmation remain
external acceptance. Automatic k3s join deliberately does not bypass that
accepted trust gate. Real TrueNAS, USB, UniFi, paging-provider, and physical
node exercises require separate explicit authority.

---

## Core Principle: Bootstrap Exists to Eat Itself

The bootstrap tool's only job is to reach the point where GitOps can take over.
Once GitLab is operational, **everything** — Vault policies, DNS records, Authentik
OIDC clients, MAAS machine definitions, k3s workloads — is expressed as Terraform
code and managed through GitLab CI/CD. No service configuration lives outside of
IaC in the final state. The bootstrap is a temporary escape hatch through the
chicken-and-egg problem, not a permanent operator.

The handoff is explicit: bootstrap completes → local Terraform state migrates to
GitLab HTTP backend → GitOps takes over → bootstrap tool is no longer needed for
day-to-day operation.

---

## End State Vision

A single Ubuntu server bootstrapped via a TUI-guided install script (Go +
Bubbletea + Lipgloss) brings up a fully integrated homelab platform. From that
point forward, the user interacts exclusively via CLI and GitOps: defining a new
service in a YAML/HCL file, merging it, and having it automatically provisioned
with DNS, TLS, and optional Authentik SSO — on the appropriate server or
k3s scheduler. New physical servers are adopted via a single CLI command that
validates BMC/IPMI and opens a GitLab MR. Vault is the authoritative credential
store; core host services may retain minimum root-owned runtime copies needed
for reliable recovery. The tool is re-runnable, targetable at any machine, and
idempotent.

---

## Success Criteria

1. Running the bootstrap tool on a fresh Ubuntu server produces a fully operational
   platform with no manual steps beyond answering TUI prompts at the start
2. Merging a service definition results in automatic deployment, DNS record
   creation, and TLS cert provisioning within a reasonable time (target: <10 min)
3. A web UI service with `authentik: true` is reachable at its hostname and
   requires Authentik login before access
4. A protocol-native service (e.g. Minecraft) is reachable at its hostname with
   no Authentik proxy, access enforced by group membership
5. Running `just adopt-server <ip>` validates BMC/IPMI and opens a GitLab MR
   with a pre-populated server definition
6. Merging that MR provisions the server via MAAS and makes it available as a
   deployment target
7. Users assigned to `adults` or `kids` groups in Authentik have access enforced
   per service definition
8. All platform credentials are recorded authoritatively in Vault; no passwords
   exist in git, logs, or non-secret structural config. Minimum runtime copies
   may exist in root-owned files with mode `0600`
9. The bootstrap tool targets either x86 or ARM Ubuntu without code changes
10. Certs are publicly trusted (Cloudflare DNS-01), automatically renewed, and
    distributed through Vault
11. Running the bootstrap tool against the same target twice produces no
    unintended changes (idempotency)
12. The tool can target a remote machine by IP or hostname, not just localhost
13. Bootstrap outputs `vault-init.json` containing Vault unseal keys and root
    token, with a clear warning to store securely
14. After bootstrap completes, all service configuration is expressed as
    Terraform code in GitLab; local Terraform state is migrated to the GitLab
    HTTP backend; no service configuration exists outside of IaC
15. Bootstrap leaves existing home networks unchanged, installs MAAS with DHCP
    disabled by default, and provides a verified UniFi procedure for rolling
    Technitium DNS out to the whole home one network at a time
16. The wizard presents an encrypted TrueNAS/Restic backup profile with visible
    schedules and retention defaults, allows every interval to be changed or
    backups to be disabled, and supports a verified removable USB copy
17. A no-repository trial profile can complete bootstrap with persistent
    unprotected-status warnings and later enable backups in place; after reboot,
    core services recover before manual Vault unseal while Vault-dependent
    automation remains safely paused
18. A host-managed timer automatically renews certificates using a zone-scoped
    Cloudflare token, atomically deploys validated material with consumer
    rollback, and defers Vault synchronization when Vault is sealed
19. Application upgrades have configurable review deadlines and maintenance
    windows; overdue changes become policy-approved but still require backup,
    compatibility, migration, health, and rollback gates. Ubuntu security
    patches install automatically without automatic reboot
20. A target-local Uptime Kuma and Beszel profile provides functional checks,
    high-level aggressively aggregated metrics, seven-day incident/log
    retention, and configurable or disableable free paging within a bounded
    resource budget; TrueNAS performs no telemetry compute
21. GitLab loss freezes infrastructure mutation; nightly full-instance recovery
    data and verified post-apply Git/state checkpoints restore the exact GitLab
    version, critical applied commit, and Terraform lineage/serial before an
    explicit automation unfreeze
22. A blank replacement administrator laptop can reconstruct protected local
    state from the password manager and a verified recovery bundle, audit the
    target without mutation, then explicitly adopt it; normal recovery preserves
    old credentials while a lost-or-compromised mode rotates them

---

## Architecture Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| TUI framework | Go + Bubbletea + Lipgloss | Single binary, x86+ARM, no runtime deps, high quality ceiling |
| Container scheduler | k3s | Familiar, lightweight, IaC-managed via Terraform k8s provider |
| Bare metal IaC | MAAS (single-region, minimal) | Terraform provider, treats bare metal as resource, scales up |
| Vault unseal | Manual Shamir shares | Keys written to `vault-init.json`; appropriate homelab tradeoff |
| Config split | `config.yaml` (structural) + `.env` (secrets) | Structural config benefits from structure; secrets never in YAML files |
| TLS | Cloudflare DNS-01 | No public exposure required; publicly trusted certs |
| Certificate renewal | Host systemd timer; protected zone-scoped Cloudflare token | Independent of GitLab and Vault availability; validates deployment and rolls back failed consumers |
| Secrets | HashiCorp Vault | Source of truth for all credentials and certs |
| Host secret delivery | Protected local runtime copies in v1; Vault Agent optional later | Keeps initial single-host recovery understandable and does not put every service behind Vault unseal |
| k3s secret delivery | Kubernetes auth + Vault Agent Injector | Uses ephemeral pod-local rendering and native workload lifecycle |
| Workload identity | Defer SPIFFE/SPIRE | Revisit for multi-node workload identity or service mTLS; avoid a second v1 trust plane |
| Resource ownership | Ansible owns host/runtime; Terraform owns durable API objects; Go orchestrates | One owner from first creation avoids imports and drift |
| Terraform approval | Interactive plan approval; explicit `--approve-terraform` for headless mutation | Makes infrastructure changes reviewable without making CI ambiguous |
| SSH host trust | Interactive trust-on-first-use with durable pinning; explicit changed-key recovery | Secure identity continuity without requiring manual `known_hosts` maintenance |
| Network cutover | Homelab-first Technitium; opt-in MAAS DHCP; manual UniFi whole-home rollout | Limits the bootstrap failure domain while preserving the desired whole-home DNS end state |
| Backup | Restic to a dedicated TrueNAS dataset; optional offline USB repository | Encrypted deduplicated snapshots, configurable retention, integrity checking, and recovery without GitLab |
| Trial backup profile | Explicit `backup_enabled: false`; optional one-time controller export | Lets evaluators proceed without inventing a same-disk “backup”; durable warnings remain |
| Reboot recovery | Core services use protected runtime copies; Vault starts sealed; reconcile after manual unseal | Preserves DNS/platform availability without automating custody of Shamir shares |
| Upgrade policy | Review deadline then policy approval in a configurable maintenance window | Keeps normal human review while preventing indefinite version drift during administrator absence |
| Observability | Target-local Uptime Kuma + Beszel; optional paging | Minimal functional alerts and high-level metrics without a full telemetry stack or NAS compute |
| GitLab disaster recovery | Nightly full backup plus post-apply critical Git/state checkpoint | Breaks the GitLab/state circular dependency while keeping local Terraform break-glass-only |
| Controller recovery | Portable encrypted bundle, read-only audit, then explicit adoption | Reconstructs a single-admin laptop without making it another source of truth or silently revoking valid access |
| SSO | Authentik | OIDC for web services; group-based access control |

---

## Service Requirements

| Service | Purpose | Healthy means | Key integrations |
|---------|---------|---------------|-----------------|
| **Technitium DNS** | Internal and eventual whole-home name resolution | Private and external queries resolve from the lab; selected UniFi networks can be migrated after bootstrap | Terraform auto-creates records; UniFi guide handles client rollout |
| **HashiCorp Vault** | Secrets + cert distribution | Unsealed, reachable, Authentik SSO login works | All services pull secrets; certs stored here |
| **Authentik** | SSO, group-based access control | OIDC login works for Vault, GitLab, Technitium, web UIs | Groups: `adults`, `kids` (extensible) |
| **GitLab + Runner** | GitOps platform, Terraform state backend | MR merged → pipeline runs → service deployed | Vault secrets injection; Terraform HTTP backend |
| **PostgreSQL** | Shared DB for services | All dependent services connected and healthy | GitLab, Authentik, MAAS |
| **MAAS** | Bare metal provisioning; optional dedicated lab VLAN DHCP | API and images are healthy; when explicitly enabled, a test node PXE boots from the isolated lab VLAN | UniFi routes VLAN; MAAS DHCP defaults off and never shares authority |
| **k3s clusters** | Named k8s infrastructure targets; Kubernetes handles internal scheduling | All defined clusters healthy, reachable as Terraform targets | MAAS provisions nodes; Ansible installs k3s; Terraform Helm provider deploys workloads |
| **TLS / Cloudflare** | Publicly trusted certs via DNS-01 | Certs auto-issued and renewed | Vault stores and distributes certs |

---

## Bootstrap Stage Order

Hard dependencies — do not reorder:

1. **Config collection** — TUI or CLI gathers domain, Cloudflare token, passwords
2. **DNS** — Technitium (internal name resolution for all subsequent services)
3. **TLS** — Cloudflare DNS-01 cert issuance; certs stored in Vault
4. **Vault** — Unsealed; `vault-init.json` written; OIDC auth mount prepared
5. **PostgreSQL** — Shared instance; isolated databases and roles created per service
6. **Authentik** — Starts directly on its final PostgreSQL database; SSO
   operational; groups and OIDC clients registered
7. **GitLab + Runner** — GitOps online; Terraform state backend configured; Vault integration active
8. **MAAS** — Bare metal provisioning ready; lab VLAN DHCP remains disabled
   unless the operator approves the isolated cutover
9. **k3s clusters** — Each cluster defined as IaC; provisioned via MAAS → Ansible → registered as named Terraform target

---

## Service Definition Schema

**Containerized service (k8s target):**
```yaml
name: ollama-webui
hostname: ollama.home.{{ domain }}     # → auto DNS record
target:
  cluster: gpu-cluster                 # named k8s cluster (defined in infrastructure/)
  helm_chart: open-webui
  values: ./values/ollama.yaml
authentik: true
allowed_groups: [adults]
health_check: http                     # materialized as a Uptime Kuma monitor
```

**Protocol-native service (k8s target, no SSO proxy):**
```yaml
name: minecraft
hostname: mc.home.{{ domain }}
target:
  cluster: general-cluster
  helm_chart: minecraft
  values: ./values/minecraft.yaml
authentik: false
allowed_groups: [adults, kids]
health_check: tcp:25565
```

**Bare metal service (direct MAAS target):**
```yaml
name: proxmox
hostname: pve.home.{{ domain }}
target:
  maas_machine: big-iron              # named MAAS machine
  ansible_role: proxmox
authentik: false
allowed_groups: [adults]
health_check: http
```

---

## Infrastructure Definition Schema

Clusters are named, first-class IaC objects. **How a cluster's nodes are
provisioned is decoupled from the cluster definition** — nodes can come from
bare metal (via MAAS), Proxmox VMs, Proxmox LXC containers, or any other
provisioning path. The cluster definition references nodes by name; the
provisioning of those nodes is defined separately.

```yaml
# infrastructure/clusters/gpu-cluster.yaml
name: gpu-cluster
nodes:
  - name: gpu-box          # how this node was provisioned is defined elsewhere
    roles: [control-plane, worker]
```

```yaml
# infrastructure/clusters/general-cluster.yaml
name: general-cluster
nodes:
  - name: vm-01
    roles: [control-plane, worker]
  - name: vm-02
    roles: [worker]
```

Node provisioning is defined separately and can be any of:

```yaml
# infrastructure/nodes/gpu-box.yaml
name: gpu-box
provisioner: maas          # bare metal via MAAS
maas_machine: gpu-box-01

# infrastructure/nodes/vm-01.yaml
name: vm-01
provisioner: proxmox-vm    # VM on a Proxmox host
proxmox_host: pve-01
vm_template: ubuntu-24.04

# infrastructure/nodes/lxc-01.yaml
name: lxc-01
provisioner: proxmox-lxc   # LXC container on a Proxmox host
proxmox_host: pve-01
template: ubuntu-24.04
```

Multiple clusters are supported — you decide how many exist and what they're for.
The service definition picks the cluster; Kubernetes handles internal scheduling.
The provisioning path for nodes is flexible and not prescribed.

---

## Deployment Targets

| Mode | When to use | IaC chain |
|------|-------------|-----------|
| k8s cluster (Helm) | Containerized services | Nodes provisioned (any method) → Ansible (k3s) → Terraform Helm provider |
| Bare metal (Ansible) | Proxmox host, direct installs, base OS config | MAAS provisions → Ansible role on named machine |
| Proxmox VM | VM workloads or k8s nodes | Terraform Proxmox provider → VM defined as node |
| Proxmox LXC | Lightweight container workloads or k8s nodes | Terraform Proxmox provider → LXC defined as node |

The provisioning method is an implementation detail of the node — not of the
cluster or the service deployed to it.

---

## Bootstrap Tool Design

- **Language**: Go
- **TUI**: Bubbletea + Lipgloss — multi-pane: progress per stage, live log pane,
  error panel with log file path reference
- **CLI**: headless mode for re-runs and CI — `bootstrap --target <ip> --stage <name>`
- **Config split**:
  - `config.yaml` — non-sensitive structural config: domain, IPs, VLAN settings,
    cluster names, SSH key path, Vault share counts. Populated interactively on
    first run or pre-supplied. Safe to commit a template (`config.yaml.example`).
  - `.env` — bootstrap secrets only: Cloudflare API token, admin passwords,
    Restic passwords, paging destination credentials, and initial tokens. Never
    committed. Populated interactively on first run or pre-supplied for
    headless/CI use. Day-2 services read minimum protected mode `0600` runtime
    files; values are never written to logs or `config.yaml`.
- **Idempotency**: every stage checks preconditions before running; safe to re-run
- **Output**: `vault-init.json` with unseal keys + root token at an
  operator-selected path outside the repository (warning and backup guidance
  displayed)
- **Recovery defaults**: preserve `.env` and `~/.bootstrap` for safe re-runs;
  removal is an explicit future cleanup action, never an automatic post-run step

---

## Network

- **Lab VLAN**: dedicated, reserved IP range
- **DHCP**: UniFi remains authoritative by default; MAAS DHCP is an explicit,
  validated opt-in for only the dedicated lab VLAN
- **Internal DNS**: `*.home.<domain>` via Technitium; initially homelab-scoped,
  then advertised to selected home networks with the UniFi guide
- **Whole-home end state**: Technitium handles client DNS for each migrated
  UniFi network; rollout and rollback occur one network at a time
- **External DNS**: Cloudflare — used only for DNS-01 validation, no public records

---

## Day-2 Operations

| Task | How |
|------|-----|
| Deploy a new service | Write definition → MR → merge → auto-deploy |
| Adopt a new server | `just adopt-server <ip>` → MR auto-created → merge → MAAS provisions |
| Rotate a secret | Update in Vault → services pick up on restart or via Vault agent |
| Update a service | Edit definition → MR → merge → re-deploy |
| Renew a cert | Automatic via Cloudflare DNS-01 |
| Add a user | Create in Authentik → assign group → access enforced immediately |
| Re-run bootstrap | `bootstrap --target <ip> --stage <name>` — idempotent |

---

## Testing Strategy

### Test Environment

A QEMU-based test environment provides fast, repeatable, isolated testing against
a clean target for every run.

**Base image**: A minimal `qcow2` snapshot built with Packer (HCL):
- Ubuntu (x86 + ARM builds)
- SSH enabled with key-based auth only (password auth disabled)
- `ansible` user with passwordless sudo
- Test keypair embedded at build time: public key in `~/.ssh/authorized_keys`;
  private key stored at `test/keys/test_id_ed25519` in the repo (test-only key,
  not a secret — never used outside local QEMU test VMs)
- No other services — clean slate

**Two-phase image lifecycle:**

| Phase | How | When |
|-------|-----|------|
| Phase 1 (pre-GitLab) | `just build-base-image` — Packer builds on demand locally | During bootstrap development |
| Phase 2 (post-GitLab) | GitLab CI builds and caches image as a pipeline artifact; runners pull it | After GitLab is operational |

The transition from Phase 1 → Phase 2 follows the same GitOps handoff pattern as
the rest of the project: bootstrap builds locally, GitLab takes over once it exists.

### Test Doubles (External Services)

Tests must never call real external APIs. Two test doubles replace external
dependencies for Stage 2 and any test involving TLS cert issuance:

| Real service | Test double | Purpose |
|---|---|---|
| Let's Encrypt (ACME) | **Pebble** + `challtestsrv` | Local ACME CA; handles DNS-01 challenge verification without real DNS propagation |
| Cloudflare DNS API | **Cloudflare API mock** (simple HTTP stub) | Responds to list-zones, create-TXT-record, delete-TXT-record with success; no real DNS touched |

Both run as containers alongside the QEMU VM during Stage 2 tests.

The bootstrap tool must support these overrides from the start (retrofitting is painful):
- `ACME_SERVER_URL` env var (or `--acme-server` flag) — points at Pebble instead of Let's Encrypt
- `CLOUDFLARE_API_BASE_URL` env var — points at the mock instead of `api.cloudflare.com`

These are test-only env vars, set in the test harness, never in `.env` for real runs.

### Test Layers

| Layer | Tool | Scope | When to run |
|-------|------|-------|-------------|
| Ansible role | Molecule (libvirt/QEMU driver) | Single role against QEMU VM; idempotency check (run twice) | Every role change |
| Bootstrap stage | `just test-stage <name>` | Full stage run against fresh QEMU VM from base snapshot; external APIs mocked | Per stage, before merge |
| Full bootstrap | `just test-bootstrap` | End-to-end bootstrap sequence on a single QEMU VM; external APIs mocked | Pre-release / integration |
| Terraform plan | `terraform plan` | Module change validation, no live infra required | Every module change |

### Justfile targets (to be added)

```justfile
# Build the base QEMU test image via Packer
build-base-image:
    packer build test/images/base.pkr.hcl

# Run Molecule tests for a specific Ansible role
test-role role:
    cd ansible/roles/{{ role }} && molecule test

# Run a single bootstrap stage against a fresh QEMU VM
test-stage stage:
    go run ./cmd/bootstrap --target qemu --stage {{ stage }} --test

# Run the full bootstrap end-to-end against a QEMU VM
test-bootstrap:
    go run ./cmd/bootstrap --target qemu --test
```

---

## Constrained Observability (v1)

Uptime Kuma supplies functional HTTP/TCP/DNS/push checks, incident history, and
optional Discord, Telegram, ntfy, or generic-webhook paging. Beszel supplies
high-level host, container, systemd, disk, network, temperature, and SMART
metrics with aggressive aggregation. Both run on the target server within the
configured resource budget; TrueNAS remains storage-only.

Kuma and local logs retain seven days. Beszel retains one-minute data for one
hour, then progressively aggregates to 10-minute, 20-minute, two-hour, and
eight-hour records through 30 days. Paging thresholds and destinations are
configurable, and dashboard-only mode is supported. A total target host, power,
or outbound-network failure cannot page from the affected host. See
[`docs/observability.md`](observability.md).

---

## Explicit Out of Scope (v1)

- Proxmox VM lifecycle management
- Multi-site or WAN replication
- Public-facing services
- Multi-user GitLab (single admin for v1)
- Prometheus, Grafana, Loki, distributed tracing, and centralized log ingestion

---

## Scale

- 1 bootstrap/management server
- 1–2 bare metal nodes (provisioned via MAAS, joined to k3s cluster)
- Domain managed in Cloudflare
