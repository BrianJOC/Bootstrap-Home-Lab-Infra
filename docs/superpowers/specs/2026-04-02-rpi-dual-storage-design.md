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
roles consume a `service_data_root` variable that points to `/data` (disk
present) or `/var/lib` (no disk). Two modes: `full` (default — redirects Docker
root and all service data) and `mount_only` (formats and mounts only; user
controls placement).

---

## Section 1: Config Layer

### New fields in `Config` (`internal/config/config.go`)

```go
DataDisk     string `yaml:"data_disk"`      // e.g. "/dev/sda" — empty means SD-card only
DataDiskMode string `yaml:"data_disk_mode"` // "full" (default) or "mount_only"
```

- `data_disk` — device path of the secondary disk. Empty = no secondary disk.
- `data_disk_mode` — `"full"` redirects Docker root and all service data to
  `/data`. `"mount_only"` formats and mounts at `/data` only. Defaults to
  `"full"` when a disk is configured.
- Mount point is always `/data` — not user-configurable.
- `RunConfig` gains a `DataPath() string` helper — returns `/data` if a disk is
  configured, `/var/lib` otherwise. Ansible roles use this via the
  `service_data_root` variable.

### Validation rules

- If `data_disk` is set: must match a block device pattern (`/dev/sd*`,
  `/dev/nvme*`, `/dev/mmcblk*`) and must not be the boot device.
- If `data_disk_mode` is set: `data_disk` must also be set.
- If `data_disk` is empty: `data_disk_mode` is ignored.

---

## Section 2: Stage 0 — TUI Disk Picker

Stage 0 gains a new step after SSH validation and before writing `config.yaml`.
Skipped if `data_disk` is already set in config (headless/re-run path).

### Flow

1. SSH to target; run `lsblk -d -o NAME,SIZE,TYPE,TRAN -J`
2. Identify boot device via `findmnt / -o SOURCE -n`; strip partition suffix
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

### Tasks

| # | Task | Condition |
|---|------|-----------|
| 1 | Detect filesystem via `blkid` on `data_disk` | Always |
| 2 | Format with `mkfs.ext4 -L homelab-data <data_disk>` | Only if blank/unrecognized |
| 3 | Add fstab entry (`LABEL=homelab-data /data ext4 defaults,noatime 0 2`) and mount | Always |
| 4 | Write `/etc/docker/daemon.json` with `"data-root": "/data/docker"` | `full` mode only |
| 5 | Create service data dirs with correct ownership | `full` mode only |

### Service data directories (full mode)

```
/data/docker
/data/postgresql
/data/vault
/data/authentik
/data/gitlab
/data/technitium
/data/maas
```

### Idempotency

- Skip format if filesystem already detected
- Skip fstab entry if already present
- Skip dir creation if already exists
- Safe to re-run at any point

### `mount_only` mode

Tasks 1–3 only. Tasks 4–5 skipped. All roles fall back to their default
`/var/lib` paths.

### Variable interface

`service_data_root` — passed to all subsequent roles:
- Disk configured: `/data`
- No disk: `/var/lib`

---

## Section 4: Existing Role Updates

Each role gets one variable substitution. No structural changes.

### `defaults/main.yml` addition (all roles)

```yaml
service_data_root: /var/lib
```

This default means every role works standalone in Molecule without
`storage_setup` having run. No behavior change unless explicitly overridden.

### Data path substitutions

| Role | Variable / template location | Old value | New value |
|------|------------------------------|-----------|-----------|
| `technitium_dns` | Docker volume in compose template | `/var/lib/technitium` | `{{ service_data_root }}/technitium` |
| `vault_install` | `storage "file"` path in `vault.hcl.j2` | `/var/lib/vault` | `{{ service_data_root }}/vault` |
| `authentik_install` | Docker compose volume | `/var/lib/authentik` | `{{ service_data_root }}/authentik` |
| `postgresql_install` | `PGDATA` env var | `/var/lib/postgresql` | `{{ service_data_root }}/postgresql` |
| `gitlab_install` | GitLab data volume | `/var/lib/gitlab` | `{{ service_data_root }}/gitlab` |
| `maas_install` | MAAS data dir | `/var/lib/maas` | `{{ service_data_root }}/maas` |

Docker install tasks require no changes — `daemon.json` is written by
`storage_setup` before Docker is installed.

---

## Section 5: Testing

### Unit tests (`config_test.go`)

- `data_disk` set to invalid device path → validation error
- `data_disk_mode` set without `data_disk` → validation error
- `data_disk` empty, `data_disk_mode` empty → valid (SD-only)
- `data_disk` set to boot device → validation error

### Stage 0 TUI picker (SSH mock)

- Inject fake `lsblk` JSON response → assert picker presents correct options
- Assert `config.yaml` written with correct `data_disk` and `data_disk_mode`
- "No candidates" path: inject response with only boot device → assert graceful
  skip, no `data_disk` written

### `storage_setup` Molecule tests

| Scenario | What is asserted |
|----------|-----------------|
| Blank disk, `full` mode | Formatted, mounted, dirs created, `daemon.json` written |
| Idempotency | Run twice — assert no changes on second run |
| `mount_only` mode | Mounted; dirs and `daemon.json` not created |
| No disk configured | Role is a no-op |

The "second disk" in Molecule is a loop device created in `prepare.yml` — no
real hardware required.

### Existing role Molecule tests

Add `service_data_root: /tmp/test-data` to converge vars to exercise the
non-default path. No other changes.

### Integration tests (QEMU)

| Test | Disk config | Assertion |
|------|-------------|-----------|
| `just test-bootstrap` | Single disk, no `data_disk` | All service data under `/var/lib`; `storage_setup` is no-op |
| `just test-stage storage` | Second virtual disk (`/dev/vdb`), `full` mode | `/data` mounted; dirs exist; `daemon.json` correct; idempotent |
| `just test-bootstrap-ssd` | Second virtual disk, `full` mode | Full bootstrap sequence; all service data under `/data` |

The secondary disk for QEMU tests is a small ephemeral `qcow2` image (2 GB)
attached via `-drive` at test launch time. Created fresh per run, discarded
after. The `Justfile` and QEMU launch wrapper gain a flag to attach it.

Both `just test-bootstrap` and `just test-bootstrap-ssd` are required — neither
is optional. The single-disk test is the baseline regression guard.

---

## Open Questions

None — all design decisions resolved.

---

## Implementation Notes

- `storage_setup` must run before any role that installs Docker or writes
  service data. It is the first play in `site.yml`.
- The `DataPath()` helper on `RunConfig` is the single source of truth for
  whether a data disk is configured — Ansible inventory generation reads from
  it to set `service_data_root` in group vars at runtime.
- `config.yaml.example` should include `data_disk` and `data_disk_mode` as
  commented-out optional fields with documentation.
