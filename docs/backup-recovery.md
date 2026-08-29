# Backup and Recovery

> Status: Accepted; manifest-backed local-only disposable restore passed — 2026-08-27
> NAS implementation guide: [TrueNAS backup guide](truenas-backup-guide.md)

## Decision

Restic is the default backup engine.

- The automatic primary repository is a dedicated dataset on the operator's
  local TrueNAS system.
- A removable USB repository is an optional offline second copy.
- Backup configuration is recommended during the wizard but can be disabled.
- Schedule, retention, integrity checks, USB copying, and restore reminders are
  configurable. The defaults are visible before the operator accepts them.
- Restic protects and versions application-consistent backup artifacts; it does
  not replace each application's native snapshot/export operation.

Rclone remains a useful transport tool but is not the default backup model.
Restic supplies encrypted repositories, deduplicated snapshots, retention,
integrity checking, and snapshot restore semantics in one tool. Rclone may be
added later for a backend or transfer path that Restic does not support directly.

## Default policy

| Setting | Recommended default | Can be changed or disabled |
|---|---:|---|
| Automated backup | Enabled after repository validation | Yes: `backup_enabled` |
| Primary target | TrueNAS NFS-mounted Restic repository | Yes: any supported repository |
| Backup schedule | Daily at 02:30 local time | Yes: preset or systemd calendar |
| Daily snapshots | 7 | Yes, including `0` |
| Weekly snapshots | 5 | Yes, including `0` |
| Monthly snapshots | 12 | Yes, including `0` |
| Metadata integrity check | Weekly | Yes or disabled |
| Full repository data check | Monthly | Yes or disabled |
| USB copy | Disabled until a repository is configured | Yes |
| USB cadence | Manual/monthly reminder | Yes: manual, weekly, monthly, or disabled |
| Restore drill reminder | Quarterly | Yes or disabled |

Setting `backup_enabled: false` is supported and does not block bootstrap. The
completion screen must display a persistent warning that no automated backup is
configured and show the command or configuration needed to enable it later.

## Wizard interaction

The wizard asks:

1. `Configure automated backups now?` with **Yes** as the recommended selection.
2. Repository preset: **TrueNAS NFS**, SFTP, mounted/local path, or custom Restic URI.
3. Repository location and mount information.
4. Schedule: **Daily at 02:30**, weekly, or custom.
5. Retention values, prefilled with `7 daily / 5 weekly / 12 monthly`.
6. Integrity-check intervals, prefilled with weekly metadata and monthly full-data checks.
7. Whether to configure a removable USB copy now.

Before writing configuration, the TUI displays the resulting schedule, retention
policy, destinations, and estimated failure behavior. The operator can go back,
choose custom values, or disable the feature. Headless mode requires explicit
values; it never invents a repository path.

## Trial profile: no NAS or USB

A user evaluating the platform may not have backup storage ready. Selecting
**Skip for now — trial mode** sets `backup_enabled: false` and is a supported
bootstrap path:

- bootstrap and the GitOps handoff may complete;
- no repository password, mount, Restic repository, timer, or USB device is required;
- the completion summary shows `Backup protection: not configured (trial mode)`;
- platform status continues to show the absence and age of the last verified backup;
- updating the backup fields and re-running the lifecycle stage can enable the
  normal profile later without reinstalling services; and
- destructive maintenance and upgrades display an additional no-verified-backup
  warning and require explicit confirmation.

The upgrade policy is stricter for unattended changes: trial mode may
policy-approve stateless patch/minor updates with immutable rollback, but
data-bearing or major upgrades remain blocked until a verified repository
exists. See [upgrade-policy.md](upgrade-policy.md).

The portable encrypted controller recovery bundle is the supported one-time
controller copy. A separate Restic export-to-laptop command is deferred. A
local checkpoint stored on the homelab server's own data disk is only a rollback
convenience and is never counted as a backup against host or disk loss.

Trial mode does not relax recovery-material handling. `vault-init.json` still
goes to the operator-selected path outside the repository, and the operator is
still instructed to preserve its unseal shares and root recovery material in the
password manager.

## Repository and secret model

The TrueNAS and USB destinations are separate Restic repositories. Each is
encrypted and may use a distinct repository password.

Repository passwords:

- are stored in the operator's password manager;
- may have the minimum root-owned mode `0600` runtime copy needed by systemd;
- are never stored in structural configuration, logs, command arguments, Git,
  or only inside Vault;
- are included in the controller-recovery checklist; and
- are verified during setup by opening and listing the repository.

Keeping the runtime password outside Vault follows the accepted manual-unseal
model: a rebooted host can continue scheduled backups even before Vault is
unsealed, except for backup components that require an unsealed Vault API.

## Backup set

Each run creates application-consistent artifacts in a protected staging
directory, snapshots them with Restic, verifies the new snapshot exists, then
removes the staging artifacts. A failed Restic write preserves the staging data
for retry and reports its protected location.

The critical backup set includes:

| Component | Consistent artifact |
|---|---|
| Vault | Raft snapshot created through the Vault API |
| PostgreSQL | Per-database logical dumps plus cluster roles/globals |
| GitLab | Native application backup; repositories, uploads, artifacts, configuration, secrets, TLS/SSH identity, exact package/version manifest, Runner metadata, and post-apply Git/state checkpoints; database also appears in the PostgreSQL set |
| Authentik | PostgreSQL database plus persistent uploaded/static data, templates, and blueprints when used |
| Technitium | Exported configuration and authoritative-zone data |
| MAAS | PostgreSQL database and non-replaceable configuration; downloaded image cache is excluded |
| k3s | Cluster datastore snapshot, declarative manifests, and recovery metadata when Stage 8 is used |
| Observability | Kuma/Beszel/health-adapter configuration and paging routes; history is low-priority and may be excluded from a small USB set |
| Bootstrap/GitOps | Portable controller recovery bundle, run manifests, known-hosts trust data, backend/ownership metadata, verified release artifact, and verified Terraform state exports |

Replaceable packages, container images, OS caches, MAAS image caches, and build
artifacts are excluded unless an operator adds them explicitly.

TrueNAS executes no backup hook, metric collector, dashboard, alert router, or
other platform compute. It supplies repository storage only. Monitoring
configuration is staged and snapshotted by the target host like every other
native artifact.

A sealed Vault cannot produce a fresh Raft snapshot. The backup run must report
the overall result as incomplete rather than silently treating an older Vault
snapshot as current. Other components may still complete, and the status report
identifies the age of the most recent valid Vault snapshot.

GitLab has a second event-driven protection path. After every successful
infrastructure apply, critical GitOps repositories and decrypted Terraform
states are validated in root-only tmpfs, snapshotted into Restic, reopened and
verified, then removed from staging. This targets the last successful apply
rather than the nightly recovery point. See
[gitlab-disaster-recovery.md](gitlab-disaster-recovery.md).

The controller has a corresponding portable recovery artifact. It is refreshed
after initial bootstrap and material controller/configuration changes, attached
to the post-apply checkpoint, and included in the nightly set. Its SSH private
key is protected by a second controller-only encryption secret in the password
manager, so the target's runtime Restic password cannot reveal the key.
`vault-init.json`, unseal shares, repository passwords, and the second
encryption secret are excluded. See
[controller-recovery.md](controller-recovery.md).

## NAS and USB workflow

### Automatic NAS repository

The host mounts a dedicated TrueNAS dataset using a systemd-aware network mount.
The backup timer requires the mount and refuses to write to the empty local mount
directory if TrueNAS is unavailable. A successful run:

1. creates native application artifacts;
2. runs the Restic backup;
3. verifies the new snapshot is listed;
4. applies the configured retention policy;
5. runs the integrity check when due; and
6. records component-by-component results and snapshot IDs.

### Offline USB repository

The USB device is normally disconnected. When connected, the operator runs the
documented copy command or responds to the configured reminder. Bootstrap:

1. validates the expected filesystem UUID and repository ID;
2. opens both repositories;
3. copies eligible verified snapshots from NAS to USB;
4. verifies the destination snapshots;
5. safely unmounts the filesystem; and
6. instructs the operator to disconnect it.

A scheduled reminder never treats an absent USB device as a failed NAS backup.
The status view instead reports how long it has been since the last successful
offline copy.

For a complete second repository, prefer a USB SSD or hard drive. A consumer
flash stick is acceptable for the smaller recovery-critical set but is not the
recommended sole second copy of GitLab and PostgreSQL data.

## Retention and destructive maintenance

Backup creation and integrity checks may run automatically. Retention deletion
and pruning use the configured policy but must:

- operate only on the validated repository ID;
- take and log a repository lock;
- show a dry-run during initial setup;
- never run concurrently with backup or repository copying;
- fail closed if the destination mount or repository identity differs; and
- preserve the offline USB repository independently.

Changing retention in the TUI or config does not immediately delete snapshots.
The next maintenance run shows the proposed effect, and large or first-time
prunes require explicit confirmation.

## Restore and proof

A backup is not considered healthy solely because a timer exited successfully.

- Weekly: metadata check.
- Monthly: full repository data check.
- Quarterly: restore a selected backup into an isolated directory or disposable VM.
- After material platform changes: perform a component restore drill before
  expiring the last known-good pre-change snapshot.

The platform restore order is:

1. base host, storage mounts, and protected recovery configuration;
2. Technitium and network resolution;
3. Vault Raft data, followed by manual unseal;
4. PostgreSQL roles and databases;
5. Authentik;
6. GitLab and Terraform state;
7. MAAS and k3s, when used; and
8. GitOps reconciliation and end-to-end login/service checks.

If the administrator laptop is also unavailable, controller recovery precedes
this sequence: restore and verify the portable bundle, perform the read-only
audit, and explicitly adopt the target. The controller bundle is a recovery map,
not authoritative platform data.

Restore procedures default to a new target or isolated paths. They do not
overwrite a running service until the operator reviews the selected snapshot,
target, expected data loss window, and rollback point.

## Required implementation evidence

Evidence recorded across the latest disposable run: the supported no-repository trial profile passed
the full bootstrap and a no-change rerun. A separate local-only QEMU profile
(explicitly not counted as durable protection) initialized Restic, created
native PostgreSQL, GitLab, k3s, Vault, configuration, and controller artifacts,
recorded a `complete=true` snapshot, then restored final snapshot `355a6d5c`
(2.016 GiB) into a new isolated directory. All 30 checksum-manifest entries
verified, including the native GitLab archive, every PostgreSQL dump, the Vault
snapshot, protected runtime configuration, and controller bundle. The isolated
copy was then removed and the VM returned to `backup_enabled: false` trial mode;
the Restic evidence repository was retained. Failure injection also proved
incomplete staging is preserved. TrueNAS mount-loss, repository identity
mismatch, retention deletion, and optional USB hardware/copy/unmount remain
external acceptance tests.

- Wizard acceptance of defaults, custom intervals, and `backup_enabled: false`.
- Trial-mode completion, persistent status, later `backup configure`, and
  one-time controller export tests.
- Blank-laptop controller recovery, separate SSH-key envelope encryption,
  read-only audit, explicit adoption, and lost-or-compromised rotation tests.
- No repository path is inferred in headless mode.
- NAS unavailability cannot redirect a backup into the local mount directory.
- Application artifact failure makes the run incomplete and identifies the component.
- Repository identity mismatch stops retention, copy, and restore operations.
- NAS backup, retention dry-run, integrity check, USB copy, and safe unmount tests.
- Quarterly full restore drill from NAS and at least one restore from USB.
