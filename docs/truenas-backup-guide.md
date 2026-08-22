# TrueNAS Guide for the Restic Backup Repository

> This guide configures storage on TrueNAS. Bootstrap does not call the TrueNAS
> API in v1.

## Recommended layout

Create a dedicated dataset rather than exporting a pool root:

```text
<pool>/homelab-backups/bootstrap-home-infra
```

Use the Generic dataset preset and expose only this dataset to the homelab
server. The repository itself is a directory inside this dataset. Do not enable
TrueNAS/ZFS block deduplication merely because Restic is in use; Restic performs
content deduplication inside its own repository.

TrueNAS documents creating a dedicated dataset and restricting an NFS share to
specific hosts or networks in its
[sharing guide](https://www.truenas.com/docs/scale/26/gettingstarted/configure/setupsharing/).

## NFS setup

1. In **Datasets**, create the dedicated Generic dataset.
2. Set ownership/permissions for a dedicated backup identity.
3. In **Shares → Unix Shares (NFS)**, export only that dataset.
4. Restrict allowed hosts to the homelab server IP, or the smallest necessary
   management network.
5. Enable the NFS service and start it automatically.
6. On the homelab server, mount it at a dedicated path such as
   `/mnt/truenas-backups` using `_netdev` and systemd automount behavior.
7. Configure bootstrap with a repository below that mount, for example
   `/mnt/truenas-backups/restic`.

Bootstrap verifies the filesystem source, mountpoint, write access, and Restic
repository ID. If the share is not mounted, it refuses to initialize or write a
repository in the local directory that normally serves as the mountpoint.

SFTP is a supported alternative when the operator prefers a dedicated TrueNAS
user and SSH-based transport. SMB is not the default for this Linux host because
Restic's documentation currently cautions about repositories on CIFS/SMB.

## TrueNAS snapshots

Optionally create a TrueNAS periodic snapshot task for the dedicated backup
dataset after the normal Restic backup window. Dataset snapshots provide a
convenient NAS-local rollback if repository files are accidentally changed or
deleted. They do not replace:

- Restic integrity checks;
- the disconnected USB repository; or
- application-consistent exports produced before Restic runs.

TrueNAS describes scheduled read-only dataset versions in its
[periodic snapshot documentation](https://www.truenas.com/docs/scale/26/dataprotection/periodicsnapshottasks/addingperiodicsnapshottasks/).

## USB repository

The removable repository is created independently from the TrueNAS dataset.
Record its filesystem UUID, Restic repository ID, and password-manager entry.
Keep it disconnected except while copying and verifying snapshots. A USB SSD or
hard drive is preferred for a full copy; a small flash device should be limited
to a deliberately selected recovery-critical backup set.

## Failure boundaries

- TrueNAS unavailable: NAS backup fails visibly; local data remains untouched.
- NFS mounted from the wrong source: repository operations stop.
- Restic repository damaged: disable retention/prune, run integrity diagnostics,
  and use the USB repository as the independent recovery source.
- TrueNAS lost: replace or rebuild it, initialize a new primary repository, copy
  from USB, and then resume normal backups.
