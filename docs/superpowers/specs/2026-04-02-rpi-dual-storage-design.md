# Design: Raspberry Pi Dual-Storage Support

> Status: Approved
> Date: 2026-04-02
> Author: brainstorming session

---

## Overview

Some bootstrap targets (notably Raspberry Pi with an SSD hat) have two storage
devices: an SD card running the OS and an SSD for data. Without intervention,
all service data lands on the SD card — causing early wear-out and poor
performance for write-heavy services (PostgreSQL, GitLab, Vault).

This design adds optional secondary disk support to the bootstrap system. When
configured, all Docker volumes and service data are redirected to the SSD. The
SD card handles only the OS and ephemeral files.

---

## Goals

- Protect SD cards from write-heavy service data by default
- Remain fully optional — single-disk targets are unaffected
- Maintain idempotency — safe to re-run against an already-configured disk
- Support headless/CI use via `config.yaml` pre-supply
- Keep existing roles minimally changed — one variable substitution each

## Non-Goals

- Configurable mount point (always `/data`)
- RAID or multi-disk striping
- Partition management (whole-disk, single partition)
- LVM or advanced filesystem features

---

## Approach

A new `storage_setup` Ansible role runs before Stage 1 (DNS). It formats (if
needed), mounts the secondary disk, and sets up data directories. All subsequent
roles consume a `service_data_root` variable that points to `/data` (full mode)
or `/var/lib` (no disk or mount_only mode). Two modes: `full` (default —
redirects Docker root and all service data) and `mount_only` (formats and mounts
only; user controls placement).

---

## Section 1: Config Layer

### New fields in `Config` (`internal/config/config.go`)

```go
DataDisk          string `yaml:"data_disk"`           // e.g. "/dev/sda" — empty means SD-card only
DataDiskMode      string `yaml:"data_disk_mode"`      // "full" (default) or "mount_only"
DataDiskForceFormat bool  `yaml:"data_disk_force_format"` // true = wipe even if filesystem detected
```

- `data_disk` — device path of the secondary disk. Empty = no secondary disk.
- `data_disk_mode` — `"full"` redirects Docker root and all service data to
  `/data`. `"mount_only"` formats and mounts at `/data` only. Defaults to
  `"full"` when a disk is configured.
- `data_disk_force_format` — when `true`, format the disk even if an existing
  filesystem is detected (other than `LABEL=homelab-data`). Requires explicit
  operator intent. Defaults to `false`.
- Mount point is always `/data` — not user-configurable.

### `DataPath()` helper

The three new fields (`DataDisk`, `DataDiskMode`, `DataDiskForceFormat`) belong
on `Config` — not `RunConfig` — so they participate in the existing `Load` and
`Validate` flow.

`DataPath()` is defined on `Config`:

```go
func (c *Config) DataPath() string {
    if c.DataDisk != "" && c.DataDiskMode != "mount_only" {
        return "/data"
    }
    return "/var/lib"
}
```

Stage runners receive `*config.Config` (not `*config.RunConfig`) in their `Run`
signatures today. Placing `DataPath()` on `Config` means no signature changes
are required — every stage runner calls `cfg.DataPath()` directly.

Returns `/data` only when a disk is configured AND mode is `"full"` (or
unset/defaulted to full). Returns `/var/lib` when no disk is configured or
mode is `"mount_only"`. This is the single source of truth consumed by all
stage runners.

### Validation rules

- If `data_disk` is set: must match a block device pattern (`/dev/sd*`,
  `/dev/nvme*`, `/dev/mmcblk*`, `/dev/vd*`) and must not be the boot device.
  `/dev/vd*` covers virtio block devices used in QEMU test environments.
- If `data_disk_mode` is set: `data_disk` must also be set.
- If `data_disk` is empty: `data_disk_mode` and `data_disk_force_format` are
  ignored.
- Valid values for `data_disk_mode`: `"full"`, `"mount_only"` (empty defaults
  to `"full"` when `data_disk` is set).

---

## Section 2: Stage 0 — TUI Disk Picker

Stage 0 gains a new step after SSH validation and before writing `config.yaml`.
Skipped if `data_disk` is already set in config (headless/re-run path).

### Boot device detection

Boot device is identified with:
```
findmnt / -o SOURCE -n
```
The resulting partition device (e.g. `/dev/mmcblk0p1`, `/dev/nvme0n1p1`,
`/dev/sda1`) is resolved to its parent disk using:
```
lsblk -no pkname <partition>
```
This is architecture-agnostic and avoids fragile string manipulation. The
parent device name returned by `pkname` is used directly for filtering.

### Flow

1. SSH to target; run `lsblk -d -o NAME,SIZE,TYPE,TRAN -J`
2. Identify boot device via `findmnt /` + `lsblk --pkname` (see above)
3. Filter out boot device — remaining devices are candidates
4. **No candidates:** display "no secondary disk detected" notice; continue in
   SD-card only mode (no `data_disk` written)
5. **Candidates found:** present TUI list with device name, size, and transport
   (usb/sata/nvme); user selects a device or chooses "none / skip"
6. **Device selected:** prompt mode — "Full data offload (recommended)" vs
   "Mount only"
7. Write selection to `config.yaml` before proceeding

### Headless mode

If `--stage config` runs non-interactively and `data_disk` is absent, disk
setup is skipped (SD-card only). If `data_disk` is pre-set in `config.yaml`,
no prompt is shown.

---

## Section 3: `storage_setup` Ansible Role

Runs as the first Ansible play — before any Docker or service roles — because
Docker's root directory must be configured before Docker is installed.

### Pre-flight disk guard

Before any formatting or mounting, the role inspects the disk:

| `blkid` result | `data_disk_force_format` | Action |
|---|---|---|
| No filesystem detected | either | Proceed to format |
| `LABEL=homelab-data` detected | either | Skip format; mount and proceed (idempotent re-run) |
| Filesystem detected, label is NOT `homelab-data` | `false` | **Halt with error**: "Disk /dev/X has existing filesystem (TYPE=Y, LABEL=Z). Set `data_disk_force_format: true` to wipe it." |
| Filesystem detected, label is NOT `homelab-data` | `true` | Proceed to format (operator opted in) |

This guard prevents silent data loss on misconfigured runs.

### Tasks

| # | Task | Condition |
|---|------|-----------|
| 1 | Run pre-flight disk guard (see above) | Always when `data_disk` is set |
| 2 | Format with `mkfs.ext4 -L homelab-data <data_disk>` | Blank disk, or force_format=true |
| 3 | Create `/data` directory, add fstab entry (`LABEL=homelab-data /data ext4 defaults,noatime 0 2`), mount | Always when `data_disk` is set |
| 4 | Write `/etc/docker/daemon.json` with `"data-root": "/data/docker"`; restart Docker if already running | `full` mode only |
| 5 | Create service data dirs with ownership (see below) | `full` mode only |

### Docker daemon handling (Task 4)

When `storage_setup` runs:
- **Fresh bootstrap (Docker not yet installed):** `daemon.json` is written as a
  static file; Docker install tasks pick it up on first start. No restart needed.
- **Re-run (Docker already installed and running):** Writing `daemon.json`
  triggers a handler (`restart docker`) only if the file content changed.
  Pre-existing containers are expected to be absent at this point in a fresh
  bootstrap. On a re-run where containers already exist, the operator is
  responsible for resolving the data-root mismatch — this is documented as a
  known limitation for mid-bootstrap re-runs.

### Service data directories (full mode, Task 5)

Created by `storage_setup` as placeholders with root ownership. Each role
reasserts correct ownership on its own first run (existing behavior is
preserved).

| Directory | Initial owner:group | Mode |
|-----------|---------------------|------|
| `/data/docker` | root:root | 0710 |
| `/data/postgresql` | root:root | 0755 |
| `/data/vault` | root:root | 0755 |
| `/data/authentik` | root:root | 0755 |
| `/data/gitlab` | root:root | 0755 |
| `/data/technitium` | root:root | 0755 |
| `/data/maas` | root:root | 0755 |

Each role's existing `file:` tasks that set ownership on their data directory
run unconditionally and will correct ownership on first use. No role-level
changes are needed for ownership.

### `mount_only` mode

Tasks 1–3 only. Tasks 4–5 skipped. `DataPath()` returns `/var/lib` in this
mode, so all roles use their default paths. The disk is mounted at `/data` but
no service data is redirected automatically.

### Idempotency

- Pre-flight guard skips format if `LABEL=homelab-data` is present
- fstab entry check before adding (idempotent via `ansible.builtin.mount`)
- Directory existence check before creating
- `daemon.json` write is idempotent (handler only fires on change)
- Safe to re-run at any point

### Variable interface

`service_data_root` is injected into every Ansible playbook invocation by each
stage runner (see Section 4). Its value comes from `cfg.DataPath()`.

---

## Section 4: Existing Role Updates and Stage Runner Contract

### Stage runner contract (`service_data_root` injection)

Every stage runner that invokes an Ansible role listed below **must** include
`service_data_root` in its extra-vars map. Stage runners already receive
`*config.Config` as `cfg` in their `Run` signatures — `DataPath()` is defined
on `Config`, so no signature changes are required:

```go
extraVars["service_data_root"] = cfg.DataPath()
```

Affected Go files:
- `internal/stages/stage1_dns.go`
- `internal/stages/stage3_vault.go`
- `internal/stages/stage4_authentik.go`
- `internal/stages/stage5_postgres.go`
- `internal/stages/stage6_gitlab.go`
- `internal/stages/stage7_maas.go`

A new `internal/stages/stage0b_storage.go` (or equivalent) handles the
`storage_setup` role invocation. This stage runs between Stage 0 (config) and
Stage 1 (DNS) and also passes `service_data_root` for consistency.

If `data_disk` is empty in config, `stage0b_storage.go` is a no-op (skipped).

### `defaults/main.yml` addition (all roles)

```yaml
service_data_root: /var/lib
```

This default means every role works standalone in Molecule without
`storage_setup` having run. No behavior change unless explicitly overridden
via extra-vars.

### Data path substitutions

| Role | Template / var location | New value |
|------|------------------------|-----------|
| `technitium_dns` | Docker volume in compose template | `{{ service_data_root }}/technitium` |
| `vault_install` | `storage "file"` path in `vault.hcl.j2` | `{{ service_data_root }}/vault` |
| `authentik_install` | Docker compose volume | `{{ service_data_root }}/authentik` |
| `postgresql_install` | `PGDATA` env var | `{{ service_data_root }}/postgresql` |
| `gitlab_install` | GitLab data volume | `{{ service_data_root }}/gitlab` |
| `maas_install` | MAAS data dir (see note below) | `{{ service_data_root }}/maas` |

**MAAS note:** MAAS is installed as a snap. Snap confinement may redirect
data to `~/snap/maas/` regardless of the configured path. Before implementing
the MAAS substitution, verify that the snap-based install honours the
configured data directory. If it does not, the MAAS entry may be a no-op and
the substitution should be skipped for that role.

Docker install tasks require no changes — `daemon.json` is written by
`storage_setup` before Docker is installed.

---

## Section 5: Testing

### Unit tests (`config_test.go`)

- `data_disk` set to invalid device path → validation error
- `data_disk_mode` set without `data_disk` → validation error
- `data_disk` empty, `data_disk_mode` empty → valid (SD-only); `DataPath()` returns `/var/lib`
- `data_disk` set, mode `"full"` → `DataPath()` returns `/data`
- `data_disk` set, mode `"mount_only"` → `DataPath()` returns `/var/lib`
- `data_disk` set, mode unset → `DataPath()` returns `/data` (defaults to full)

### Stage 0 TUI picker (SSH mock)

- Inject fake `lsblk` JSON + `findmnt` + `lsblk --pkname` responses → assert
  picker presents correct options
- Assert `config.yaml` written with correct `data_disk` and `data_disk_mode`
- "No candidates" path: inject response where `pkname` returns the only disk →
  assert graceful skip, no `data_disk` written
- NVMe/eMMC device: inject `mmcblk0p1` as boot partition, assert `pkname`
  lookup correctly filters `mmcblk0` from candidates

### `storage_setup` Molecule tests

A loop device is created in `prepare.yml` as the simulated second disk — no
real hardware required.

| Scenario | Assertions |
|----------|------------|
| Blank disk, `full` mode | `/data` mounted; all service dirs exist root:root; `/etc/docker/daemon.json` contains `/data/docker` |
| Idempotency | Run twice — zero changed tasks on second run |
| `mount_only` mode | `/data` mounted; service dirs do NOT exist; `daemon.json` absent |
| No disk configured (`data_disk` empty) | `/data` does not exist; no fstab entry for `homelab-data`; `daemon.json` absent |
| Existing `LABEL=homelab-data` disk (re-run) | No format; mounts and proceeds; idempotent |
| Existing filesystem, wrong label, `force_format: false` | Task fails with descriptive error message |
| Existing filesystem, wrong label, `force_format: true` | Disk wiped and reformatted; proceeds normally |

### Existing role Molecule tests

Add `service_data_root: /tmp/test-data` to converge vars to exercise the
non-default path. No other changes.

### Integration tests (QEMU)

| Test | Disk config | Assertion |
|------|-------------|-----------|
| `just test-bootstrap` | Single disk, no `data_disk` | All service data under `/var/lib`; `storage_setup` is no-op; no `/data` dir |
| `just test-stage storage-setup` | Second virtual disk (`/dev/vdb`), `full` mode | `/data` mounted; dirs exist; `daemon.json` correct; idempotent on second run |
| `just test-bootstrap-ssd` | Second virtual disk, `full` mode | Full bootstrap sequence; all service data under `/data` |

The secondary disk for QEMU tests is a small ephemeral `qcow2` image (2 GB)
attached via `-drive` at test launch time. Created fresh per run, discarded
after. The `Justfile` and QEMU launch wrapper gain a `--second-disk` flag.

Both `just test-bootstrap` and `just test-bootstrap-ssd` are required — neither
is optional. The single-disk test is the baseline regression guard.

---

## Implementation Notes

- `storage_setup` must run before any role that installs Docker or writes
  service data. It is the first play in `site.yml` after config validation.
- A new `StorageSetupStage` Go struct is required in
  `internal/stages/stage0b_storage.go` with `Name()` returning
  `"storage-setup"`. Without it, `just test-stage storage-setup` has no target.
- `DataPath()` on `RunConfig` is the single source of truth for whether a data
  disk is configured in full mode. All stage runners read from it.
- `config.yaml.example` should include `data_disk`, `data_disk_mode`, and
  `data_disk_force_format` as commented-out optional fields with inline
  documentation. If this file does not exist, create it.
- The MAAS snap data directory behaviour must be verified before implementing
  the MAAS role substitution (see Section 4 MAAS note).

---

## Open Questions

1. **MAAS snap data directory** — Does the snap-based MAAS install honour a
   configured data directory, or does snap confinement override it? Must be
   confirmed before implementing the MAAS role substitution. If snap overrides
   it, the MAAS entry in Section 4 is a no-op and should be removed.

2. **Docker re-run with existing containers** — The design documents this as a
   known limitation (mid-bootstrap re-run with containers already running is
   operator-responsibility). If this is unacceptable, a migration path (stop
   containers, rsync data, update daemon.json, restart) should be designed as
   a follow-on. Deferred for v1.
