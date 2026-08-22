# Operating System and Application Upgrade Policy

> Status: Accepted target state — 2026-07-28

## Decision

The platform gives the administrator a bounded review period for application
upgrades. When that period expires, the update becomes automatically approved
for the next configured maintenance window. Automatic approval replaces missing
human approval; it never bypasses compatibility, backup, capacity, health, or
vendor upgrade-path gates.

Ubuntu security packages install automatically. They never trigger an automatic
reboot. Application and platform versions remain pinned until an approved or
deadline-eligible maintenance run changes them.

## Default policy

| Update class | Review period | Default action after deadline |
|---|---:|---|
| Critical application security | 3 days | Eligible at next maintenance window |
| Other application security | 7 days | Eligible at next maintenance window |
| Patch or minor application | 21 days | Eligible at next maintenance window |
| Major application | 30 days | Eligible only with a validated component-specific plan |
| Ubuntu security packages | Immediate | Install automatically; never reboot automatically |
| Other Ubuntu packages | 21 days | Treat as an application update |

The default general maintenance window is Sunday at 03:00 target-host local
time, with a three-hour admission window. Weekly, monthly, and custom systemd
calendar schedules are supported. The administrator may disable scheduled
application upgrades, but status then carries a persistent manual-maintenance
warning and continues to report overdue updates.

An update whose review period expires just after a window waits for the next
window. Therefore the effective maximum delay is the configured review period
plus one maintenance interval.

## Review and automatic approval

Renovate proposes pinned dependency changes through GitLab merge requests and
maintains a Dependency Dashboard. Each proposal records:

- component, installed version, and target version or digest;
- update class and security classification;
- discovery time, review deadline, and next eligible window;
- release notes, compatibility evidence, and required intermediate versions;
- expected downtime and consumer impact;
- backup and rollback requirements; and
- preflight and post-upgrade health checks.

Before the deadline, the administrator may approve, reject the specific
version, or select another supported target. A rejected version is never
installed automatically, but the next version creates a new proposal. Changing
the general deadline or maintenance schedule is structural configuration and is
visible in the dashboard.

At the deadline, the proposal receives automatic approval. The maintenance
coordinator still evaluates it only inside the next window. Deadline expiry is
not represented as a human review and is explicitly labeled
`approved-by-policy`.

## Maintenance execution

The coordinator processes one component at a time:

1. refresh the proposal and verify the target still exists and is not rejected;
2. calculate the vendor-supported path and refuse skipped required versions;
3. confirm platform health, disk space, time synchronization, and repository
   reachability;
4. create or verify an application-consistent backup when the component is
   data-bearing;
5. verify the rollback or restore procedure and prior artifact availability;
6. pause affected jobs or writes and announce expected downtime;
7. apply exactly one safe upgrade step;
8. wait for migrations and component readiness;
9. run authentication, data, API, DNS, TLS, and workload-specific checks; and
10. either continue to the next safe step while time remains or leave a
    resumable overdue plan for the next window.

No new component starts after the three-hour admission window closes. An
already-running step may finish so it is not abandoned mid-migration.

The host-side oneshot executor is independent of GitLab remaining available
after execution begins. GitLab remains the review and audit plane; upgrading
GitLab must not kill the operation that is responsible for bringing GitLab back.

## Safety gates

Automatic approval never overrides:

- an invalid or unsupported version path;
- incomplete required background migrations;
- a failed pre-upgrade health check;
- insufficient disk space;
- a missing required artifact or package;
- a missing or stale verified backup for a data-bearing component;
- an untested restoration contract for a migration that cannot be downgraded;
- a rejected or withdrawn version;
- an active incident or maintenance lock; or
- a prior failed upgrade awaiting diagnosis.

A blocked update remains overdue, reports the failed gate, and retries
preflight at the next maintenance window. It does not repeatedly mutate the
host.

## Trial mode without a backup repository

Trial mode permits deadline-driven patch and minor upgrades only when the
component is stateless or can be rolled back safely to its prior immutable
artifact without restoring data.

The following remain blocked until a verified backup repository exists:

- data-bearing component upgrades;
- database or storage migrations;
- major application upgrades;
- GitLab, PostgreSQL, Vault, and Authentik upgrades that can change persisted
  data; and
- any update whose rollback contract requires restoring files or a database.

A same-host checkpoint may accelerate rollback but does not satisfy the backup
gate. Status explains that the overdue update is blocked by trial-mode recovery
risk rather than presenting the system as current.

## Ubuntu policy

Ubuntu's unattended-upgrades service applies official security updates. The
bootstrap-managed drop-in:

- enables security origins only by default;
- excludes application repositories such as GitLab from unattended mutation;
- sets automatic reboot to `false`;
- records whether `/var/run/reboot-required` exists; and
- reports package failures and reboot age to platform status.

The operator schedules the reboot separately. The reboot uses the accepted
manual-unseal recovery flow and is never bundled silently into an application
maintenance window.

## Component-specific constraints

### GitLab

- Calculate the vendor-required upgrade stops from installed to target version.
- Use the latest supported patch at each required stop.
- Pause Runner job pickup and wait for active jobs before downtime.
- Run GitLab preflight checks and verify secrets can be decrypted.
- Do not advance until required background migrations are complete.
- Treat the single-node outage as expected maintenance.
- After each step, verify login, projects, merge requests, clone, push, Runner,
  registry when enabled, and backup readiness.

### K3s

- Do not skip unsupported Kubernetes minor versions.
- Upgrade server/control-plane nodes before agents.
- Use concurrency one by default.
- Cordon and drain when supported by the workload and topology.
- Restrict system-upgrade-controller plans to the general maintenance window.
- Verify node readiness, system workloads, DNS, ingress, storage, and a test
  workload before advancing.

### Other stateful services

PostgreSQL, Vault, Authentik, Technitium, and MAAS require a
component-specific compatibility plan, native backup artifact, migration
completion check, and restore procedure. A package downgrade is not described as
rollback when a database schema has already advanced.

### Stateless workloads

Container and Helm workloads use immutable tags or digests. A failed rollout
returns to the prior known-good artifact and verifies workload health. This is
the only update class allowed to proceed automatically in trial mode.

## Failure behavior

| Failure point | Required result |
|---|---|
| Preflight gate | Make no change; retain overdue state and report the gate |
| Artifact download | Keep current version; retry next window |
| Health failure before migration | Restore prior immutable artifact when supported |
| Failure after irreversible migration | Stop, preserve diagnostics, and require the documented restore decision |
| Window expires between safe steps | Finish current step; resume remaining path next window |
| GitLab unavailable during its own upgrade | Host executor completes or restores service independently |
| Host requires reboot | Report reboot required; never reboot automatically |
| Previous upgrade failed | Block later components until failure is resolved |

## Required evidence

- Review-deadline tests cover each update class and the next-window calculation.
- Manual approval and `approved-by-policy` follow the same execution gates.
- An overdue proposal with a failed gate never mutates the target.
- Trial mode upgrades a stateless test workload and blocks every data-bearing
  fixture.
- Ubuntu security updates cannot update third-party application repositories or
  reboot the host.
- GitLab fixtures prove required-stop calculation, migration waiting, Runner
  pause, host-side execution, and resumability across multiple windows.
- K3s fixtures prove minor-version sequencing, server-before-agent ordering,
  concurrency one, and window enforcement.
- Failure injection covers every preflight, migration, health, rollback, and
  time-window boundary.

## Upstream references

- [Ubuntu automatic updates](https://documentation.ubuntu.com/server/how-to/software/automatic-updates/)
- [GitLab upgrade paths](https://docs.gitlab.com/update/upgrade_paths/)
- [GitLab pre-upgrade planning](https://docs.gitlab.com/update/plan_your_upgrade/)
- [GitLab background migrations](https://docs.gitlab.com/update/background_migrations/)
- [K3s automated upgrades](https://docs.k3s.io/upgrades/automated)
- [Renovate Dependency Dashboard](https://docs.renovatebot.com/key-concepts/dashboard/)
