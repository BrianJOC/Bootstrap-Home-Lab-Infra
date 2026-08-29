# Constrained Observability and Paging

> Status: Accepted; constrained target-local runtime integration passed — 2026-08-26

## Decision

The target homelab server runs the complete v1 observability stack:

- Uptime Kuma for synthetic service checks, certificate visibility, job
  heartbeats, incident history, and a simple availability dashboard;
- Beszel hub and a local Beszel agent for high-level host, disk, network,
  systemd, container, temperature, and SMART metrics; and
- the bootstrap health adapter for platform-specific states such as backup age,
  Vault seal state, certificate synchronization, and upgrade blockers.

TrueNAS is storage only. It does not run an observability hub, agent, collector,
notification router, or any other compute workload. Monitoring configuration
and the small local state databases may be included in Restic; backup storage
does not execute telemetry.

Prometheus, Grafana, Loki, distributed tracing, centralized log ingestion, and
per-request application telemetry are out of scope for v1.

The bootstrap health adapter is the immediately active minimum alerting path:
it performs protocol-aware checks, maintains consecutive-failure counters,
writes the dashboard JSON, and sends at most one incident trigger plus an
optional recovery notification. It does not depend on first-run setup in either
dashboard. Uptime Kuma requires its normal first-user setup before optional
visual monitors are added. Beszel similarly requires the operator to create the
first user and add the local system using the derived hub public key and Unix
socket shown by the installed configuration. Until those steps are complete,
the health adapter and direct paging are active but the two product dashboards
must not be described as fully configured.

## Known availability boundary

Because monitoring and paging run on the monitored target, they can alert on a
failed service, degraded dependency, resource pressure, expiring certificate,
failed job, or partial platform outage while the host retains power and outbound
connectivity.

They cannot page when the entire target loses power, crashes, loses all network
connectivity, or cannot reach the configured notification service. The
dashboard must state this limitation. V1 does not claim external dead-man
monitoring and does not move telemetry compute to TrueNAS to close the gap.

## Collection and retention defaults

| Data | Default interval | Retention |
|---|---:|---:|
| Critical DNS and HTTPS checks | 60 seconds | 7 days |
| Ordinary service checks | 5 minutes | 7 days |
| Job and platform-state heartbeat | 5 minutes or job completion | 7 days |
| Beszel host/container metrics | Native 60-second collection | Hierarchical aggregation through 30 days |
| Local service logs | journald policy | 7 days |
| Centralized logs and traces | Disabled | None |

Beszel's built-in hierarchy is deliberately accepted instead of deploying a
general-purpose time-series stack:

| Resolution | Retention |
|---:|---:|
| 1 minute | 1 hour |
| 10 minutes | 12 hours |
| 20 minutes | 1 day |
| 2 hours | 7 days |
| 8 hours | 30 days |

This is more aggressive than retaining one-minute metrics for one to three
days, but it satisfies the constrained-system objective while retaining useful
trend context. Uptime Kuma heartbeat and incident history is capped at seven
days.

## Functional checks

The default availability dashboard includes:

- a real Technitium DNS query for an expected private record and one external
  record;
- Vault API reachability with `healthy`, `sealed/degraded`, and `unavailable`
  distinguished;
- PostgreSQL readiness;
- Authentik OIDC discovery and the browser login route;
- GitLab readiness, web access, and Runner heartbeat;
- MAAS and k3s API readiness when enabled;
- one declared health check per GitOps service definition;
- active TLS validity and days until expiry;
- certificate renewal and Vault-sync state;
- primary backup age, completeness, and integrity-check age;
- removable-copy age when configured;
- upgrade queue, overdue blockers, last result, and reboot-required age; and
- Kuma, Beszel hub, Beszel agent, and health-adapter self-health.

A listening port alone is not sufficient when a lightweight protocol-native
health operation is available.

## Metrics scope

The default Beszel profile records only high-value system signals:

- CPU utilization and load;
- memory and swap;
- root and configured data-volume capacity and I/O;
- selected network-interface traffic;
- host and critical-container status and resource use;
- systemd status for bootstrap-managed services;
- temperature sensors when available; and
- SMART/eMMC wear or RAID state when supported.

GPU, per-container detail, extra filesystems, sensors, and interfaces are
allowlisted or disableable. No packet capture, flow logging, request bodies,
secret values, command lines, or high-cardinality application labels are
collected.

## Resource budget

The default combined observability budget is:

- 512 MiB memory hard limit;
- 50% of one CPU as the combined sustained quota;
- 5 GiB local persistent-state ceiling; and
- no outbound metric traffic other than configured notification payloads.

These are configurable structural limits. The installer performs an initial
idle and synthetic-load measurement before calling the stack healthy.

When pressure is observed, the health adapter:

1. lengthens noncritical Kuma check intervals;
2. disables optional Beszel collectors such as GPU, extra sensors, and
   noncritical container detail;
3. retains critical functional checks and paging;
4. marks observability `degraded: resource budget`; and
5. never evicts or throttles a core platform service to preserve telemetry.

Beszel's native one-minute collection is not presented as configurable when the
upstream product does not expose that control.

## Paging configuration

Paging is recommended during setup but can be disabled independently of
monitoring and dashboards.

Supported v1 destinations:

- Discord webhook;
- Telegram bot and chat;
- ntfy;
- generic HTTPS webhook; and
- any additional provider supported by the deployed tools only after a tested
  configuration is added.

The wizard collects the selected credential using masked input, shows the
effective routing and thresholds, and sends a test alert. A failed test cannot
be recorded as configured. The operator may retry or explicitly choose
**Dashboard only — paging disabled**.

Notification credentials follow ADR-009: recovery copy in the password manager,
minimum root-owned mode `0600` runtime copy, and authoritative inventory in
Vault after it is available. Credentials never appear in arguments, logs,
structural configuration, dashboards, alert bodies, or generated systemd unit
text.

## Default paging policy

| Setting | Default |
|---|---|
| Paging | Recommended; enable after a successful destination test |
| Critical check cadence | 60 seconds |
| Critical service trigger | 3 consecutive failures |
| Ordinary check cadence | 5 minutes |
| Ordinary service trigger | 3 consecutive failures |
| CPU or memory warning | Threshold sustained for 15 minutes |
| Disk warning / critical | 85% / 95% |
| SMART or array failure | Immediate |
| Backup, renewal, or upgrade job failure | Immediate after the job is conclusively failed |
| Recovery notification | Enabled |
| Unresolved critical reminder | Every 6 hours |
| Warning quiet hours | Configurable; disabled by default |
| Critical quiet hours | Not allowed; maintenance suppression is used instead |

The operator can change trigger counts, sustained durations, thresholds,
reminder cadence, recovery messages, warning quiet hours, destinations, and
per-monitor routing. Paging can be disabled globally or for an individual
warning class.

Platform status displays `monitoring active; paging disabled` when dashboard
checks continue without a notification destination. This is a supported mode,
not a false healthy claim.

## Noise control and maintenance

- Expected downtime from the configured maintenance window suppresses only the
  affected monitors.
- Resource alerts require sustained pressure rather than a single spike.
- An incident sends one trigger, configured reminders, and one recovery message.
- Dependency suppression prevents a failed DNS service from creating a separate
  page for every hostname that consequently fails.
- Vault `sealed` after reboot is a known degraded state, not an unavailable page;
  failure to unseal by a configurable age may page.
- Trial mode's missing backup is a persistent dashboard warning; it pages only
  if the operator enables that warning route.

Maintenance suppression never disables data collection or hides the final
post-maintenance health result.

## Dashboard and access

Kuma and Beszel receive internal DNS names and wildcard TLS. Authentik protects
normal browser access. A local break-glass account is retained in the password
manager because the dashboards are most useful when Authentik is unhealthy.

Dashboards remain private; no public status page or inbound internet exposure is
created. Notification delivery is outbound-only. Alert payloads contain service
identity, state, duration, and a private dashboard link but no secret or
sensitive diagnostic content.

## Backup and recovery

Restic includes:

- Kuma monitor, notification-routing, maintenance, and incident configuration;
- Beszel users, system registration, alert thresholds, and dashboard state;
- monitoring credentials through the established protected-secret inventory;
  and
- the bootstrap health-adapter configuration.

Historical metric and heartbeat databases are low-priority recovery data.
Configuration and routing must restore even if history is intentionally omitted
from a smaller USB critical set. Monitoring failure never blocks restoration of
DNS, Vault, PostgreSQL, Authentik, or GitLab.

## Required evidence

Evidence recorded 2026-08-26: Uptime Kuma, Beszel hub, local Beszel agent, and
the health-adapter timer ran together on the ARM64 QEMU target with the declared
container/systemd budgets; the adapter checked all installed platform services.
Paging-disabled mode was the tested default. Provider-specific delivery tests,
dashboard first-user setup, retention aging, deliberate alert/recovery
injection, and x86_64 budget measurements remain acceptance work.

- All functional checks distinguish application readiness from an open port.
- Discord, Telegram, ntfy, generic webhook, failed-test, and paging-disabled
  setup paths are exercised without contacting production services in CI.
- Alert debounce, dependency suppression, maintenance suppression, reminders,
  and recovery notifications behave deterministically.
- Notification secrets never appear in arguments, logs, manifests, dashboards,
  test artifacts, or alert payloads.
- Retention deletes Kuma history after seven days and confirms Beszel's expected
  aggregation tiers.
- Idle and synthetic-load tests stay within the configured memory, CPU, and
  disk ceilings on x86_64 and ARM64.
- Budget pressure sheds optional collectors and noncritical cadence before
  critical checks.
- A full host shutdown demonstrates and documents the local-only paging blind
  spot rather than claiming an alert was possible.
- Restore tests recover monitoring configuration independently of metric
  history.

## Upstream references

- [Uptime Kuma monitoring and notification support](https://github.com/louislam/uptime-kuma)
- [Beszel metrics and alerts](https://beszel.dev/)
- [Beszel notification providers](https://beszel.dev/guide/notifications/)
- [Beszel environment controls](https://beszel.dev/guide/environment-variables)
- [Beszel retention implementation](https://github.com/henrygd/beszel/blob/main/internal/records/records_deletion.go)
