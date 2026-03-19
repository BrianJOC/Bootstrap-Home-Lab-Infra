# PRD: bootstrap-home-infra

> Status: Complete — all open questions resolved. Ready for workflow-architect.
> Last updated: 2026-03-19

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
validates BMC/IPMI and opens a GitLab MR. The user never manages passwords
manually; everything flows through Vault. The tool is re-runnable, targetable
at any machine, and idempotent.

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
8. All credentials are stored in Vault; no passwords exist in git, logs, or
   plaintext config
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

---

## Architecture Decisions

| Decision | Choice | Rationale |
|----------|--------|-----------|
| TUI framework | Go + Bubbletea + Lipgloss | Single binary, x86+ARM, no runtime deps, high quality ceiling |
| Container scheduler | k3s | Familiar, lightweight, IaC-managed via Terraform k8s provider |
| Bare metal IaC | MAAS (single-region, minimal) | Terraform provider, treats bare metal as resource, scales up |
| Vault unseal | Manual Shamir shares | Keys written to `vault-init.json`; appropriate homelab tradeoff |
| Domain config | `config.yaml` / interactive TUI | No hardcoded values; tool usable by others |
| TLS | Cloudflare DNS-01 | No public exposure required; publicly trusted certs |
| Secrets | HashiCorp Vault | Source of truth for all credentials and certs |
| SSO | Authentik | OIDC for web services; group-based access control |

---

## Service Requirements

| Service | Purpose | Healthy means | Key integrations |
|---------|---------|---------------|-----------------|
| **Technitium DNS** | Internal name resolution | All `*.home.<domain>` names resolve from lab VLAN | Terraform auto-creates records on service deploy |
| **HashiCorp Vault** | Secrets + cert distribution | Unsealed, reachable, Authentik SSO login works | All services pull secrets; certs stored here |
| **Authentik** | SSO, group-based access control | OIDC login works for Vault, GitLab, Technitium, web UIs | Groups: `adults`, `kids` (extensible) |
| **GitLab + Runner** | GitOps platform, Terraform state backend | MR merged → pipeline runs → service deployed | Vault secrets injection; Terraform HTTP backend |
| **PostgreSQL** | Shared DB for services | All dependent services connected and healthy | GitLab, Authentik, MAAS |
| **MAAS** | Bare metal provisioning, lab VLAN DHCP | New server PXE boots, gets OS, appears in inventory | Unifi routes VLAN; MAAS owns DHCP on lab segment |
| **k3s clusters** | Named k8s infrastructure targets; Kubernetes handles internal scheduling | All defined clusters healthy, reachable as Terraform targets | MAAS provisions nodes; Ansible installs k3s; Terraform Helm provider deploys workloads |
| **TLS / Cloudflare** | Publicly trusted certs via DNS-01 | Certs auto-issued and renewed | Vault stores and distributes certs |

---

## Bootstrap Stage Order

Hard dependencies — do not reorder:

1. **Config collection** — TUI or CLI gathers domain, Cloudflare token, passwords
2. **DNS** — Technitium (internal name resolution for all subsequent services)
3. **TLS** — Cloudflare DNS-01 cert issuance; certs stored in Vault
4. **Vault** — Unsealed; `vault-init.json` written; Authentik OIDC configured
5. **Authentik** — SSO operational; groups created; OIDC clients registered
6. **PostgreSQL** — Shared instance; databases created per service
7. **GitLab + Runner** — GitOps online; Terraform state backend configured; Vault integration active
8. **MAAS** — Bare metal provisioning ready; lab VLAN DHCP active
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
health_check: http                     # hook for future monitoring
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
- **Config**: `config.yaml` for domain, credentials, target IPs; populated
  interactively on first run or pre-supplied for automated use
- **Idempotency**: every stage checks preconditions before running; safe to re-run
- **Output**: `vault-init.json` with unseal keys + root token (warning displayed)

---

## Network

- **Lab VLAN**: dedicated, reserved IP range
- **DHCP**: MAAS manages lab VLAN; Unifi manages all other VLANs
- **Internal DNS**: `*.home.<domain>` via Technitium, resolving on lab VLAN only
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

## Monitoring (planned, deferred — not v1)

Uptime Kuma — lightweight, Authentik-compatible. `health_check` field included
in service definition schema now so monitoring wires in without schema changes.

---

## Explicit Out of Scope (v1)

- Proxmox VM lifecycle management
- Multi-site or WAN replication
- Automated alerting / notifications
- Public-facing services
- Backup / restore strategy
- Multi-user GitLab (single admin for v1)
- Monitoring dashboard (schema hook included; implementation deferred)

---

## Scale

- 1 bootstrap/management server
- 1–2 bare metal nodes (provisioned via MAAS, joined to k3s cluster)
- Domain managed in Cloudflare
