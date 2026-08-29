# Bootstrap Stage Specs

> Produced by: spec-writer
> Sources: `docs/prd.md`, `docs/workflow.md`
> Last updated: 2026-03-19
> Status: Ready for software-architect

---

## How to Read This Document

Every spec section follows the same structure:

- **Summary** — what the stage does, what it produces, what depends on it
- **Inputs** — I/O contract table
- **Outputs / Postconditions** — verifiable assertions
- **Acceptance Criteria** — independently testable numbered items
- **Failure Modes** — detection and recovery table
- **Test Cases** — pre-organized by happy path / idempotency / failure injection / edge cases

### Test Environment

All stage tests run against a QEMU VM booted from the base qcow2 snapshot:

- Ubuntu 22.04 or 24.04, x86 and ARM builds
- SSH enabled; password auth disabled; key-based auth only
- `ansible` user with passwordless sudo
- Test keypair: public key in `~/.ssh/authorized_keys`; private key at `test/keys/test_id_ed25519`
- No pre-installed services — clean slate

No test may produce a password prompt. Every test must complete unattended once initial config is supplied.

### Global Invariants (carried through every spec)

1. Any stage that touches Vault must assert Vault is unsealed before proceeding
2. Config is split: `config.yaml` holds non-sensitive structural config (domain, IPs, VLAN settings, SSH key path, Vault share counts); `.env` holds bootstrap secrets (Cloudflare token, admin passwords, tokens) with mode `0600`. Secrets never appear in logs, TUI plaintext, process arguments, git, or `config.yaml`. Per ADR-009, core host services may retain minimum root-owned mode `0600` runtime copies
3. Every stage begins with an idempotency gate: check whether target state exists; skip creation if it does
4. Every `[AUTO]` failure emits to both TUI error panel and log file with absolute path and line reference: `see: <abs-path>/logs/run-<timestamp>/bootstrap.log:<line>`
5. DNS resolution and port reachability are explicit numbered checks, never assumed
6. Every failure mode has an operator decision path: retry, skip, rollback, or abort
7. `vault-init.json` write failure is a hard halt — no recovery path discards unseal keys
8. Every Ansible role and binary selection must branch on detected arch (x86_64 or aarch64)
9. Tests never call real external APIs. Stage 2 uses Pebble (`ACME_SERVER_URL`) and a Cloudflare API mock (`CLOUDFLARE_API_BASE_URL`). Both overrides must be supported by the bootstrap tool from initial design — not retrofitted
10. Every managed resource has exactly one lifecycle owner as defined in `docs/resource-ownership.md`: Ansible owns host/runtime lifecycle, Terraform owns durable service API objects, and Go owns orchestration
11. Interactive Terraform applies require approval of a displayed saved plan. Headless mutation requires the explicit `--approve-terraform` flag
12. Clean bootstrap never imports Terraform-owned resources; an unexpected pre-existing resource stops the stage and routes to a separate brownfield adoption workflow
13. Go and Ansible use the same pinned SSH host identity. No authentication or remote command occurs before first-use acceptance or configured fingerprint verification succeeds
14. Backup defaults are displayed before acceptance. `backup_enabled: false`
    is supported, all intervals are configurable, and no headless run infers a
    repository path. Backup operations validate destination mount and repository
    identity before writing, pruning, copying, or restoring
15. After reboot, core services may start from protected runtime credentials
    while Vault is sealed. Platform health remains degraded and Vault-dependent
    operations fail closed until masked manual unseal and reconciliation complete
16. Monitoring runs only on the target within configured CPU, memory, disk, and
    retention bounds. Paging is optional and a full target outage is explicitly
    outside local paging coverage. TrueNAS performs storage only
17. Controller reconstruction verifies a portable bundle and target identity,
    completes a mutation-free audit, and requires explicit adoption. Restic
    credentials alone cannot decrypt the backed-up controller SSH key, and
    Vault init or Shamir material is never automatically included

---

## Stage 0: Config Collection

### Summary

Stage 0 is the entry point for every bootstrap run. It collects all required configuration values either interactively via TUI prompts or from existing `config.yaml` and `.env` files, validates SSH connectivity to the target host, detects target architecture and OS version, and writes the run manifest. Config is split: structural values go to `config.yaml`; secrets go to `.env`. All subsequent stages depend on the outputs of this stage.

### Inputs

**Structural config (`config.yaml`):**

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| Target host IP or hostname | Operator (TUI prompt or `--target` flag) | Yes | Must be SSH-reachable |
| SSH user | Operator (TUI prompt, default `ubuntu`) | Yes | Must have passwordless sudo |
| SSH key path | Operator (TUI prompt, default `~/.ssh/id_ed25519`) | Yes | Path only — key never written to config |
| Expected SSH host fingerprint | `config.yaml` or TUI | No | SHA-256 fingerprint; when supplied, observed key must match |
| Internal domain | Operator (TUI prompt) | Yes | e.g. `home.example.com`; used for all DNS and TLS |
| Cloudflare zone ID | Operator (TUI prompt) | Yes | Zone for internal domain |
| Bootstrap server static IP | Operator (TUI prompt) | Yes | Used for DNS records and Vault listener |
| Lab VLAN subnet (CIDR) | Operator (TUI prompt) | Yes | Network boundary; does not imply DHCP ownership |
| Lab gateway | Operator (TUI prompt) | Yes | Must be contained by the lab subnet |
| Lab VLAN ID | Operator (TUI prompt) | Yes | VLAN ID configured on UniFi |
| Lab VLAN interface | Operator (TUI prompt) | Conditional | Required only when MAAS DHCP is enabled |
| MAAS DHCP range | Operator (TUI prompt) | Conditional | Required only when MAAS DHCP is enabled |
| MAAS DHCP enabled | Operator (TUI prompt, default `false`) | No | Explicitly starts the dedicated-VLAN cutover |
| Backup enabled and repository | Operator (TUI prompt) | No | Wizard recommends enabled; repository required only when enabled |
| Backup/verification/USB intervals | Operator (TUI prompt) | No | Visible defaults from `docs/backup-recovery.md`; each is customizable or disabled |
| Controller recovery options | Operator (TUI prompt) | No | Defaults to enabled, refresh after material changes, and separately encrypted SSH-key inclusion |
| Monitoring intervals, retention, and resource budget | Operator (TUI prompt) | No | Defaults from `docs/observability.md`; target-local only |
| Paging enabled, provider, and thresholds | Operator (TUI prompt) | No | Recommended but defaults disabled until a destination test succeeds |
| Admin email | Operator (TUI prompt) | Yes | Used for Vault, GitLab, Authentik admin accounts |
| Vault unseal shares count | Operator (TUI prompt, default `5`) | Yes | Minimum `3` |
| Vault unseal threshold | Operator (TUI prompt, default `3`) | Yes | Must be <= shares count |
| Existing `config.yaml` | Filesystem at `./config.yaml` | No | If present, values loaded as defaults; operator reviews before proceeding |

**Secrets (`.env`):**

| Input | Env var | Required | Notes |
|-------|---------|----------|-------|
| Cloudflare API token | `CF_DNS_API_TOKEN` | Yes | Stored only in mode `0600` `.env`; never written to logs or process arguments |
| Initial admin password | `BOOTSTRAP_ADMIN_PASSWORD` | Yes | Must meet minimum strength; stored only in mode `0600` `.env`; never logged |
| Primary Restic password | `RESTIC_PRIMARY_PASSWORD` | Conditional | Required only when primary backup is enabled |
| USB Restic password | `RESTIC_USB_PASSWORD` | Conditional | Required only when removable repository is enabled |
| Controller recovery password | `CONTROLLER_RECOVERY_PASSWORD` | Conditional | Required when the controller bundle includes the SSH private key; distinct from Restic passwords |
| Paging destination credential | `PAGING_DESTINATION_URL` | Conditional | Required only when paging is enabled; protected and test-validated |
| Existing `.env` | Filesystem at `./.env` | No | If present, values loaded; operator reviews masked before proceeding |

### Outputs / Postconditions

1. `./config.yaml` exists, is valid YAML, and contains all required non-secret structural fields
2. `./config.yaml` contains no secrets — no tokens, passwords, or credentials of any kind
3. `./.env` exists and contains all required secret env vars; file has mode `0600`
4. `./logs/run-<timestamp>/manifest.json` exists and contains: target host, arch, config hash (not values), start time
5. SSH connectivity to the target host is confirmed (key-based, no password prompt)
6. Target architecture is detected and stored in the run manifest (`x86_64` or `aarch64`)
7. Target OS is confirmed as Ubuntu >= 22.04
8. TUI log contains: `[INFO] stage=config-collection status=complete`
9. Log file contains: `[INFO] bootstrap started; target=<ip>; pid=<pid>`, `[INFO] config written; path=./config.yaml`, `[INFO] env written; path=./.env`
10. The target host key is pinned in the bootstrap-owned known-hosts file and the run manifest records its algorithm and SHA-256 fingerprint

### Acceptance Criteria

1. Running the bootstrap binary with no `config.yaml` or `.env` presents all required TUI prompts in order; confirming produces a valid `config.yaml`, a `.env` with mode `0600`, and a run manifest
2. Running the bootstrap binary when `config.yaml` already exists loads structural values as defaults and presents them for review; `.env` secrets are re-prompted masked
3. Providing a malformed `config.yaml` produces an error identifying the file and parse location; operator is prompted to correct or start fresh
4. Providing an invalid IP address produces an inline validation error without clearing other fields
5. Providing a password that does not meet minimum strength requirements produces an inline validation error specifying the failed requirement
6. SSH connection failure produces an error with the specific reason (refused / auth failure / timeout) and re-prompts for host/user/key
7. Target architecture is correctly detected as `x86_64` or `aarch64` and written to the run manifest
8. A target running Ubuntu 20.04 produces a `[VERIFY]` warning; operator can continue at own risk or abort; both paths are tested
9. `--headless` flag with complete `config.yaml` and `.env` produces no prompts, writes manifest, confirms SSH, exits cleanly; any missing required value causes a hard failure naming the missing field
10. `--stage <name>` flag skips config collection and jumps directly to the named stage
11. No secret value appears in `config.yaml`, TUI output, or log files at any point
12. `.env` file has mode `0600` immediately after write; bootstrap exits with error if it detects mode is wider
13. TUI displays all required observable states in order
14. `./logs/run-<timestamp>/manifest.json` contains a config hash (not values) and start time ISO timestamp
15. First connection displays target, resolved address, key algorithm, and SHA-256 fingerprint; acceptance pins the key and reconnects with strict verification before authentication
16. A changed key displays the old and new fingerprints and requires the operator to confirm an intentional reinstall/replacement by typing the target; successful replacement is atomic and followed by a strict reconnect
17. Headless mode never replaces a changed pin unless both `--replace-host-key` and a matching configured `ssh_host_fingerprint` are supplied
18. The backup screen shows effective destinations, schedules, retention, checks,
    and restore reminder; the operator can accept, edit, or set
    `backup_enabled: false`
19. Headless mode with backups enabled but no repository or primary password
    fails naming the missing field; disabled backups require neither

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| `config.yaml` parse error | YAML parse error on load | Operator corrects file or deletes it to trigger fresh collection |
| `.env` missing required var in headless mode | Env var unset at stage start | Hard failure naming the missing var; no prompts shown |
| Input validation failure | Inline TUI validation on field exit | Re-prompt failed field only; retain all other values |
| SSH connection refused | TCP connect to port 22 times out or RST within 10s | Emit specific error; `[PROMPT]` for corrected host/user/key; retry |
| SSH auth failure | SSH handshake succeeds but key rejected | Emit "auth failure"; `[PROMPT]` for corrected key path; retry |
| Unknown SSH host key | No existing pin and no configured expected fingerprint | Interactive fingerprint confirmation; headless requires expected fingerprint or `--accept-new-host-key` |
| Changed SSH host key | Observed fingerprint differs from pin | Stop before authentication; interactive replacement flow or strict headless requirements from `docs/ssh-host-trust.md` |
| `uname -m` fails | Non-zero SSH exit code | Warn; operator prompted to confirm arch manually or abort |
| OS version check fails | `lsb_release` output does not match Ubuntu >= 22.04 | `[VERIFY]` warning; operator can continue at own risk or abort |
| `.env` write fails (permissions) | Non-zero write return | Emit error with path; abort stage |

### Test Cases

#### Happy Path

- Boot fresh QEMU VM from base qcow2 snapshot
- Run bootstrap binary with no pre-existing `config.yaml` or `.env`
- Fill all TUI prompts with valid test values; supply secrets via masked prompts
- Assert: `config.yaml` written with correct structural values; no secrets present
- Assert: `.env` written with correct env vars; mode is `0600`; no values in logs
- Assert: `manifest.json` exists with correct arch, target, and config hash
- Assert: log contains `[INFO] stage=config-collection status=complete`
- Assert: TUI showed all observable states in order

#### Idempotency

- Run stage 0 a second time with existing `config.yaml` and `.env`
- Assert: structural values loaded as defaults; secrets re-prompted masked
- Assert: new `manifest.json` written (different timestamp); old one not overwritten

#### Failure Injection

- **SSH refused**: non-existent target IP; assert error names "connection refused"; re-prompts host
- **SSH auth failure**: wrong private key; assert "auth failure" error; re-prompts key path
- **Unknown SSH key rejected**: decline first-use confirmation; assert no pin is written and no authentication is attempted
- **Changed SSH key accepted**: simulate a reinstall; assert old/new fingerprints shown, typed target required, entry replaced atomically, and strict reconnect succeeds
- **Changed SSH key rejected**: cancel replacement; assert old pin remains byte-for-byte unchanged and no authentication is attempted
- **Malformed `config.yaml`**: write invalid YAML before run; assert parse error with file path; operator prompted to fix or delete
- **Weak password**: password below minimum strength; assert inline validation error on that field only
- **OS mismatch**: Ubuntu 20.04 QEMU VM; assert `[VERIFY]` warning; assert both continue and abort paths work
- **`.env` too-permissive mode**: manually set `.env` to `0644` before run; assert bootstrap detects and errors

#### Edge Cases

- `--headless` with `config.yaml` missing one structural field: hard failure naming the field; no prompts
- `--headless` with `.env` missing one secret: hard failure naming the env var; no prompts
- Target arch `aarch64`: ARM VM correctly detected; manifest records `aarch64`
- Very long domain name (63-char label): passes validation
- Domain with hyphens: passes validation

---

## Stage 1: DNS (Technitium)

### Summary

Stage 1 deploys Technitium DNS on the target host and configures it as the
authoritative resolver for the internal domain. Ansible owns the container and
host resolver integration; the bootstrap DNS Terraform root owns the zone and A
records from first creation. The target host starts using the local resolver only
after private and external queries pass. Existing router, DHCP, and non-lab
client settings are not changed. All subsequent stages depend on internal DNS
resolution. The staged cutover contract is in
[`docs/network-cutover.md`](network-cutover.md).

### Inputs

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| `config.yaml` | Stage 0 output | Yes | Must be valid and loaded |
| `config.internal_domain` | `config.yaml` | Yes | e.g. `home.example.com` |
| `config.bootstrap_server_ip` | `config.yaml` | Yes | IP Technitium binds to; used for all A records |
| Current target resolver configuration | Stage 1 preflight | Yes | Captured in the run manifest for rollback |
| Reachable upstream DNS servers | Stage 1 preflight | Yes | Technitium must resolve external names before target cutover |
| `BOOTSTRAP_ADMIN_PASSWORD` | `.env` (mode `0600`) | Yes | Technitium API authentication; never logged or placed in structural config |
| SSH access to target host | Stage 0 postcondition | Yes | Key-based, no password |
| Target arch | Stage 0 run manifest | Yes | Determines Docker install path if needed |

### Outputs / Postconditions

1. Technitium DNS process is running on `<bootstrap_server_ip>:53` (TCP and UDP) and `<bootstrap_server_ip>:5380` (HTTP API)
2. Terraform state owns DNS zone `home.<domain>` in Technitium
3. The following A records exist and resolve correctly from the target host:
   - `vault.home.<domain>` → `<bootstrap_server_ip>`
   - `authentik.home.<domain>` → `<bootstrap_server_ip>`
   - `gitlab.home.<domain>` → `<bootstrap_server_ip>`
   - `technitium.home.<domain>` → `<bootstrap_server_ip>`
   - `maas.home.<domain>` → `<bootstrap_server_ip>`
4. Target host resolver uses the local Technitium instance; its prior
   configuration is recorded and is restored automatically if verification fails
5. DNS query from the target host (`dig vault.home.<domain> @127.0.0.1`) returns `<bootstrap_server_ip>`
6. TUI log contains: `[INFO] stage=dns status=complete`
7. Log file contains per-record creation entry: `[INFO] dns-record: created name=<name> ip=<ip>`
8. No router, DHCP, or non-lab client DNS setting was changed

### Acceptance Criteria

1. Running Stage 1 on a target with no Technitium instance first validates the
   server address and checks TCP/UDP 53 and TCP 5380, then installs Technitium
   via `roles/technitium_dns`, starts it, and produces all postconditions
2. Running Stage 1 when Technitium is already running on ports 5380 and 53 skips installation and jumps directly to zone and record verification/creation
3. Running Stage 1 when the DNS zone already exists skips zone creation and proceeds to record upserts
4. Running Stage 1 when all A records already exist produces no changes; all log entries show "already exists" or "skipping"
5. After Stage 1 completes, `dig vault.home.<domain> @<bootstrap_server_ip>` from the operator's machine returns `<bootstrap_server_ip>` (non-blocking if operator's machine cannot reach port 53 — see recovery 8a)
6. After Stage 1 completes, `dig vault.home.<domain> @127.0.0.1` via SSH on the target host returns `<bootstrap_server_ip>`
7. Target host's resolver configuration survives a systemd-resolved restart (or equivalent) without reverting
8. Technitium container log output on failure is written to the bootstrap log file with an explicit path reference in the TUI error panel
9. TUI displays all required observable states: `Installing Technitium DNS...`, `DNS: waiting for Technitium to start (Xs)...`, `DNS: creating zone home.<domain>`, `DNS: creating A records...`, `DNS: verifying resolution...`, `Stage 1: DNS — complete`
10. All A records for downstream service hostnames are present and correct after stage completion; no bootstrap stage beyond Stage 1 may create its first DNS record independently
11. A forced DNS failure after changing the target resolver restores the exact
    captured prior resolver configuration
12. Stage 1 never calls the UniFi API or changes the DNS advertised to existing
    home networks

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Ansible role fails (Docker install, port conflict) | Non-zero Ansible exit code; emitted to log | Emit error + log path to TUI; `[VERIFY]` retry or abort stage |
| Required port belongs to another process | TCP/UDP probe and process inspection do not identify the managed Technitium instance | Stop before install; identify the address, protocol, port, and owning process |
| Technitium health poll timeout (120s) | No HTTP 200 from `:5380/api/user/login` within timeout | Dump container logs to log file; emit path; `[VERIFY]` retry or abort |
| Technitium API auth failure | HTTP 401 or 403 from API | Emit error identifying credential mismatch; operator corrects `BOOTSTRAP_ADMIN_PASSWORD` in `.env`; retry stage |
| DNS zone creation fails (Technitium API error) | Non-2xx response from Technitium zone API | Emit full API error to log; `[VERIFY]` retry or abort |
| DNS resolution fails from operator's machine (port 53 blocked by firewall) | DNS query from operator machine returns NXDOMAIN or times out | Emit warning (non-blocking); note operator machine may need manual DNS config or split-DNS; target self-resolution is the critical check |
| systemd-resolved config fails (locked config, wrong path) | SSH command returns non-zero exit | Restore captured resolver config; emit specific error; `[VERIFY]` retry or abort |
| DNS resolution fails from target (resolver loop or upstream failure) | Private or external test query returns SERVFAIL or times out | Restore captured resolver config; dump resolver diagnostics to log; `[VERIFY]` retry or abort |

### Test Cases

#### Happy Path

- Boot fresh QEMU VM from base qcow2 snapshot
- Run `just test-stage dns` (which runs Stage 0 first with test config, then Stage 1)
- Assert: Technitium running; TCP port 5380 and 53 open on QEMU VM IP
- Assert: `dig vault.home.test @<qemu-ip>` returns `<qemu-ip>` from operator's machine
- Assert: via SSH, `dig vault.home.test @127.0.0.1` returns `<qemu-ip>`
- Assert: all five A records exist
- Assert: log contains `[INFO] stage=dns status=complete`

#### Idempotency

- After happy path run, run Stage 1 a second time without resetting the VM
- Assert: Ansible role reports no changes on second run (`changed=0`)
- Assert: zone creation step logs "already exists; skipping"
- Assert: all record creation steps log "already exists; skipping"
- Assert: resolver config step produces no changes
- Assert: log contains `[INFO] stage=dns status=complete` for the second run
- Assert: no error or warning emitted for a double run

#### Failure Injection

- **Docker install failure**: configure Ansible role to fail the Docker step (e.g. block package download); assert error is emitted to TUI and log with path reference; assert `[VERIFY]` prompt appears
- **Health poll timeout**: start Technitium but misconfigure bind address so health check never succeeds; assert timeout at 120s; assert container logs dumped to log file; assert TUI shows path reference
- **API auth failure**: correct installation but wrong admin password; assert HTTP 401 is caught; assert error names "auth failure"; assert no further configuration is attempted
- **DNS resolution failure from target**: configure a resolver loop (point systemd-resolved at a non-functional forwarder); assert `dig @127.0.0.1` failure is caught; assert log contains the failure; assert `[VERIFY]` prompt appears

#### Edge Cases

- Technitium already installed and running but missing some A records: assert stage adds only the missing records without recreating existing ones
- Target using systemd-resolved stub resolver: assert resolver config correctly handles the stub resolver path (`/etc/systemd/resolved.conf`) rather than directly editing `/etc/resolv.conf`
- ARM64 QEMU VM: assert Docker is installed with ARM64 image; assert Technitium container starts on ARM64

---

## Stage 2: TLS (Cloudflare DNS-01)

### Summary

Stage 2 obtains a publicly trusted wildcard TLS certificate for `*.home.<domain>` using Cloudflare DNS-01 challenge validation. The Cloudflare API token is read from the mode `0600` `.env` bootstrap input and is never written to logs, process arguments, or structural config. The issued certificate and private key are stored locally at `./certs/home.<domain>/` for pickup by Stage 3 (Vault), which is not yet running at this point. This stage depends on Stage 1 (internal DNS running) and internet access to the Cloudflare API. The `roles/tls_lego` Ansible role deploys the lego binary for the target arch.

### Inputs

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| `CF_DNS_API_TOKEN` | `.env` during bootstrap; protected host runtime file after setup | Yes | Zone-scoped; never written to logs, arguments, or structural config |
| `config.cloudflare_zone_id` | `config.yaml` | Yes | Zone ID for the internal domain in Cloudflare |
| `config.internal_domain` | `config.yaml` | Yes | Wildcard cert covers `*.home.<domain>` and `home.<domain>` |
| `config.admin_email` | `config.yaml` | Yes | ACME account email |
| Target arch | Stage 0 run manifest | Yes | Determines lego binary variant |
| Internet access from target host | Network prerequisite | Yes | Cloudflare API must be reachable from target |
| Stage 1 complete | Stage 1 postcondition | Yes | DNS must be running (required by pipeline order; not required for cert issuance itself) |

### Outputs / Postconditions

1. `./certs/home.<domain>/fullchain.pem` exists and contains a valid certificate chain
2. `./certs/home.<domain>/privkey.pem` exists and contains the corresponding private key
3. `./certs/home.<domain>/meta.json` exists and contains: expiry date (ISO 8601), SAN list, issuer
4. Certificate subject matches `*.home.<domain>`; SANs include `*.home.<domain>` and `home.<domain>`
5. Certificate expiry is > 30 days from the time of issuance
6. Certificate issuer is Let's Encrypt (or configured ACME CA)
7. `CF_DNS_API_TOKEN` value does not appear in any log file, TUI output, `config.yaml`, or `.env` after initial write (`.env` stores the var reference, not the value, in log output)
8. TUI log contains: `[INFO] stage=tls status=complete`
9. Log file contains: `[INFO] tls-cert: cert issued; path=./certs/home.<domain>/` and `[INFO] tls-verify: cert valid; expiry=<date>`

### Acceptance Criteria

1. Running Stage 2 with valid Cloudflare credentials issues a wildcard cert and writes all three output files
2. Running Stage 2 when a valid cert (expiry > 30 days) already exists at `./certs/home.<domain>/` skips issuance, logs "cert exists and valid; skipping issuance", and marks the stage complete
3. Running Stage 2 when a cert exists but expires within 30 days treats it as missing and reissues
4. The Cloudflare API token value does not appear in any log file entry, in TUI output, or in any file written to disk at any point during the stage
5. Cert validation parses the issued PEM and confirms: subject `*.home.<domain>`, SAN includes both `*.home.<domain>` and `home.<domain>`, expiry > 30 days, issuer is Let's Encrypt
6. The lego binary deployed matches the target arch (x86_64 binary on x86_64; aarch64 binary on aarch64)
7. A Cloudflare API auth failure (invalid token) produces an error that surfaces the Cloudflare API error message; the operator is prompted to re-enter the token and zone ID; the stage retries from the challenge step
8. A Let's Encrypt rate-limit error produces a warning displaying the timestamp of the next available attempt; the stage pauses; the operator can retry or abort
9. TUI displays all required observable states: `TLS: checking for existing certificate...`, `TLS: deploying cert tooling...`, `TLS: DNS-01 challenge in progress...`, `TLS: waiting for DNS propagation (Xs)...`, `TLS: certificate issued — *.home.<domain>`, `Stage 2: TLS — complete`
10. `./certs/home.<domain>/meta.json` is written with correct expiry and SAN list parseable by Stage 3

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Cloudflare API unreachable (no internet, firewall) | TCP connect to `api.cloudflare.com:443` fails within 10s | Emit error; `[VERIFY]` wait and retry or abort; suggest checking upstream connectivity |
| Challenge auth failure (invalid token or zone ID) | Cloudflare API returns 401 or 403 | Surface specific API error; `[PROMPT]` to re-enter token and zone ID; retry from step 4 |
| DNS propagation timeout (> 300s) | lego challenge times out | Emit timeout error with elapsed time; `[VERIFY]` retry (propagation may succeed on retry) or abort |
| Let's Encrypt rate limit exceeded | lego returns rate-limit error | Emit warning with next-available timestamp; operator must wait; `[VERIFY]` retry or abort; do not loop automatically |
| Cert validation fails (corrupt PEM, wrong SAN) | PEM parse or SAN check fails after issuance | Emit specific validation failure; delete corrupt file; `[VERIFY]` reissue or abort |

### Test Environment (Stage 2)

Stage 2 tests never call real external APIs. Two containers run alongside the QEMU VM:

- **Pebble** (`letsencrypt/pebble`) + **`challtestsrv`** — local ACME CA; handles DNS-01 challenge verification without real DNS propagation
- **Cloudflare API mock** — HTTP stub responding to list-zones, create-TXT-record, and delete-TXT-record with success; no real Cloudflare calls made

Required overrides (set by test harness, never in `.env` for real runs):
- `ACME_SERVER_URL=https://localhost:14000/dir` — points lego at Pebble
- `CLOUDFLARE_API_BASE_URL=http://localhost:8080` — points lego at the mock

The bootstrap tool must support both overrides. They must be wired in at design time — not retrofitted.

### Test Cases

#### Happy Path

- Start Pebble, `challtestsrv`, and Cloudflare API mock containers
- Boot fresh QEMU VM; run Stages 0–1 first to satisfy prerequisites
- Run `just test-stage tls` with test overrides (`ACME_SERVER_URL`, `CLOUDFLARE_API_BASE_URL`)
- Assert: `./certs/home.test/fullchain.pem` exists and is parseable
- Assert: `./certs/home.test/privkey.pem` exists
- Assert: `./certs/home.test/meta.json` exists with correct expiry and SANs
- Assert: cert subject is `*.home.test`; SANs include both wildcard and apex
- Assert: log does not contain the `CF_DNS_API_TOKEN` value
- Assert: no calls made to `api.cloudflare.com` or `acme-v02.api.letsencrypt.org`
- Assert: log contains `[INFO] stage=tls status=complete`

#### Idempotency

- After happy path run, run Stage 2 again without deleting cert files
- Assert: log contains "cert exists and valid; skipping issuance"
- Assert: lego is not invoked a second time
- Assert: output files are unchanged (same content, same mtime or same hash)
- Assert: stage completes successfully with no errors

#### Failure Injection

- **Invalid Cloudflare token**: configure mock to return 403; assert API error is surfaced; assert `[PROMPT]` for corrected token; assert token value is not in logs
- **Cloudflare API unreachable**: stop the mock container; assert TCP check fails within 10s; assert `[VERIFY]` prompt appears
- **ACME server unreachable**: stop Pebble container; assert connection error surfaces; assert `[VERIFY]` prompt appears
- **Cert file corrupt**: write garbage bytes to `./certs/home.test/fullchain.pem` before run; assert corrupt file detected; assert reissuance attempted

#### Edge Cases

- Cert expiring in 15 days: assert stage treats it as expired and reissues
- ARM64 QEMU VM: assert lego aarch64 binary deployed; cert issuance succeeds
- Domain with multiple labels (e.g. `lab.home.example.com`): assert wildcard covers `*.lab.home.example.com`

### Day-2 renewal contract

After Stage 2, certificate renewal becomes a host lifecycle responsibility as
specified in [certificate-renewal.md](certificate-renewal.md):

- a systemd timer checks eligibility daily with a configurable randomized delay;
- renewal defaults to 30 days before expiry and is configurable or disableable;
- the zone-scoped Cloudflare token is read from a root-owned mode `0600` runtime
  file through lego's `_FILE` support;
- lego writes into a staging directory and never directly overwrites the active
  certificate;
- key/certificate match, configured SANs, issuer, chain, and advancing expiry
  must validate before an atomic `current` symlink switch;
- consumers reload sequentially with health checks and the prior certificate is
  restored if any consumer fails;
- Vault synchronization occurs immediately when unsealed or is recorded as
  pending for post-unseal reconciliation; and
- GitLab, Runner, and Vault availability are not renewal prerequisites.

Acceptance tests use Pebble and the Cloudflare mock, inject consumer reload
failure at each position, verify rollback, and prove sealed-Vault renewal plus
post-unseal reconciliation. Disabling automation must retain expiry monitoring
and a persistent manual-renewal warning.

---

## Stage 3: Vault

### Summary

Stage 3 installs, initializes, and configures HashiCorp Vault as the authoritative secrets store for the platform. Vault runs with Raft integrated storage, is secured with the wildcard TLS cert from Stage 2, and is initialized with Shamir unseal keys. The `vault-init.json` file containing all unseal keys and the root token is written to an operator-selected path outside the repository and must be backed up securely. This file is sacred: failure to write it is a hard halt. Manual unseal is the v1 default. After initialization and unsealing, Stage 3 mounts the `kv-v2` secrets engine, writes the wildcard cert to Vault, enables an audit log, writes bootstrap policies, enables the OIDC auth method (completed after Authentik starts in Stage 5), and generates a 24-hour bootstrap token for use by all subsequent stages. Core host services may retain minimum root-owned mode `0600` runtime copies under ADR-009. The Ansible role `roles/vault_install` handles binary deployment and systemd unit configuration.

### Inputs

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| `config.yaml` | Stage 0 output | Yes | |
| `config.bootstrap_server_ip` | `config.yaml` | Yes | Vault listener binds here |
| `config.vault_shares` | `config.yaml` | Yes | Number of Shamir shares (default 5, min 3) |
| `config.vault_threshold` | `config.yaml` | Yes | Unseal threshold (default 3) |
| `./certs/home.<domain>/fullchain.pem` | Stage 2 output | Yes | TLS cert for Vault HTTPS listener |
| `./certs/home.<domain>/privkey.pem` | Stage 2 output | Yes | TLS key for Vault HTTPS listener |
| DNS resolution of `vault.home.<domain>` | Stage 1 postcondition | Yes | Must resolve before Vault starts |
| Target arch | Stage 0 run manifest | Yes | Determines Vault binary variant |
| Operator unseal keys | Operator (TUI masked input) | Yes | Required on first run; offered auto-read from `vault-init.json` on first-run path |

### Outputs / Postconditions

1. Vault is running at `https://vault.home.<domain>:8200`; `GET /v1/sys/health` returns HTTP 200
2. Vault is initialized (`GET /v1/sys/init` returns `{"initialized": true}`)
3. Vault is unsealed (`GET /v1/sys/seal-status` returns `{"sealed": false}`)
4. The configured `vault_init_output_path` is outside the repository, exists with mode `0600`, and contains valid unseal keys and root token
5. TUI displayed the `vault-init.json` warning box prominently during init
6. `kv-v2` secrets engine is mounted at `secret/`
7. `pki` secrets engine is mounted at `pki/`
8. Wildcard TLS cert is written to `secret/data/bootstrap/tls/wildcard` with fields `cert`, `key`, `expiry`, `domain`
9. Vault audit log is active at `/var/log/vault/audit.log` on the target host
10. Vault policy `bootstrap` is written and allows reading bootstrap secrets
11. OIDC auth method is enabled at `auth/oidc/` (pending Authentik configuration in Stage 5)
12. Root token is stored at `secret/data/bootstrap/vault/root-token`
13. Bootstrap-scoped token (policy `bootstrap`, TTL `24h`) is held in-memory run context; not written to disk or logs
14. TUI cert permissions on target: `/etc/vault.d/tls/privkey.pem` is `vault:vault 0400`; `/etc/vault.d/tls/fullchain.pem` is `0644`
15. TUI log contains: `[INFO] stage=vault status=complete`

### Acceptance Criteria

1. Running Stage 3 on a target with no Vault installation deploys the correct binary for the target arch, writes Vault config with Raft storage and TLS cert paths, creates the systemd unit, starts the service, and produces all postconditions
2. Running Stage 3 when Vault is already running and initialized skips install and init; jumps to seal check
3. Running Stage 3 when Vault is running, initialized, and unsealed skips install, init, and unseal; verifies secrets engines, policies, and OIDC auth method presence
4. On first initialization, `vault-init.json` is written atomically to the configured path immediately upon receiving the init response; the path must resolve outside the repository and the file must have mode `0600`. If the write fails for any reason, bootstrap halts with a CRITICAL error in the TUI and does not proceed
5. The TUI displays a prominent warning box after `vault-init.json` is written: text must include the absolute file path, a warning not to commit it, a recommendation to back it up in a password manager or other secure location, and a statement that bootstrap cannot verify the external backup
6. `vault-init.json` content does not appear in the log file
7. On first run, TUI offers to auto-read unseal keys from `vault-init.json`; this offer is present only on the first-run path; subsequent runs require manual key entry
8. Wrong unseal key entry produces an inline error with "wrong key" and allows re-entry without resetting unseal progress
9. After unsealing, `GET /v1/sys/seal-status` returns `{"sealed": false}`; this assertion must appear in the log: `[INFO] vault-unseal: vault is unsealed`
10. `kv-v2` and `pki` mounts: enabling a mount that already exists does not produce an error; stage is idempotent
11. Wildcard cert written to `secret/data/bootstrap/tls/wildcard` is readable and contains the correct `cert`, `key`, `expiry`, and `domain` fields
12. Vault audit log is active; writing a test secret and then reading `audit.log` on the target shows a corresponding log entry
13. Bootstrap token in run context is not present in any log line, TUI output, or file; it exists only in memory for the current run
14. Bootstrap token has a 24-hour TTL; running bootstrap stages spanning more than 24 hours (detected by token creation time in manifest vs. current time) produces a warning that the token may be expired and Stage 3 must be re-run to refresh it
15. TUI displays all required observable states: `Vault: checking for existing instance...`, `Vault: installing Vault binary...`, `Vault: starting Vault service...`, `Vault: initializing (first run)...`, warning box for `vault-init.json`, `Vault: enter unseal key N of threshold...`, `Vault: unsealed`, `Vault: configuring secrets engines and policies...`, `Stage 3: Vault — complete`
16. Successful bootstrap preserves `.env` and `~/.bootstrap` by default; no automatic cleanup removes recovery or rerun material
17. Any future cleanup flow inventories proposed removals, verifies authoritative Vault copies first, requires explicit confirmation, and never removes `vault-init.json`

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Ansible role fails (arch detection, package download) | Non-zero Ansible exit code | Emit error + log path; `[VERIFY]` retry or abort |
| Vault fails to start (TLS cert path wrong, port conflict) | Health poll timeout 60s; systemd unit status failed | Dump `journalctl -u vault --no-pager -n 50` to log file; emit path to TUI; `[VERIFY]` retry or abort |
| `vault-init.json` write fails (disk full, permissions) | File write returns error | CRITICAL halt; TUI red panel: "CRITICAL: vault-init.json could not be written"; no recovery path that discards keys; operator must fix disk/permissions and re-run `bootstrap --stage vault` |
| Wrong unseal key entered | Vault API returns HTTP 400 | Display inline error; allow re-entry of the incorrect key only; Vault tracks correct progress |
| Root token invalid (`vault-init.json` corrupted) | `POST /v1/auth/token/lookup-self` returns error | Emit error; direct operator to check `vault-init.json` integrity; if file is corrupt, Vault must be re-initialized |
| Secret write fails (policy or auth error) | Vault API returns non-2xx | Emit specific Vault error to log; `[VERIFY]` retry or abort |
| Bootstrap token TTL expired between stages | Token lookup returns 403 | Emit warning in TUI; instruct operator to run `bootstrap --stage vault` to generate a new bootstrap token |

### Test Cases

#### Happy Path

- Boot fresh QEMU VM; run Stages 0–2 first
- Run `just test-stage vault`
- Assert: Vault running; `https://vault.home.test:8200/v1/sys/health` returns 200
- Assert: the configured `vault_init_output_path` exists with mode `0600`, contains `keys` and `root_token` fields, and is valid JSON
- Assert: TUI warning box was shown (inspect TUI log for the warning message)
- Assert: `GET /v1/sys/seal-status` returns `{"sealed": false}`
- Assert: `kv-v2` mount at `secret/` and `pki` mount at `pki/` are active
- Assert: `secret/data/bootstrap/tls/wildcard` is readable and contains correct fields
- Assert: `/var/log/vault/audit.log` exists on target and is non-empty
- Assert: log contains `[INFO] stage=vault status=complete`

#### Idempotency

- After happy path run, run Stage 3 a second time without resetting the VM
- Assert: install step is skipped (log: "already running; skipping install")
- Assert: init step is skipped (log: Vault already initialized)
- Assert: unseal step is skipped (log: already unsealed)
- Assert: `kv-v2` enable is skipped (mount already exists)
- Assert: `pki` enable is skipped (mount already exists)
- Assert: `bootstrap` policy write is skipped (policy already exists)
- Assert: OIDC auth method enable is skipped (auth method already exists)
- Assert: stage completes without error
- Assert: `vault-init.json` is NOT overwritten on the idempotency path

#### Failure Injection

- **`vault-init.json` write fails**: configure a read-only or full destination filesystem; assert CRITICAL TUI error in red panel; assert bootstrap halts immediately; assert no further Vault operations are attempted after the write failure
- **Wrong unseal key**: after init, enter an incorrect unseal key; assert HTTP 400 is caught; assert inline error shown; assert progress counter does not regress; assert correct key entry after error continues normally
- **Vault start failure (port conflict)**: bind port 8200 with a dummy process before Stage 3; assert health poll timeout at 60s; assert `journalctl` output is dumped to log file; assert TUI shows log file path; assert `[VERIFY]` prompt appears
- **TLS cert path mismatch**: point Vault config at a non-existent cert path; assert Vault fails to start; assert error is correctly attributed to TLS config

#### Edge Cases

- `vault-init.json` already exists from a previous run when Stage 3 is re-run on an already-initialized Vault: assert file is NOT overwritten; assert existing file is used for root token on idempotency path
- Bootstrap token age check: simulate a run manifest with a start time > 23 hours ago; assert warning is shown that bootstrap token may be near expiry
- ARM64 QEMU VM: assert Vault aarch64 binary is installed; assert service starts correctly

---

## Stage 4: PostgreSQL

### Summary

Stage 4 deploys the shared PostgreSQL service before any dependent application.
It creates isolated databases and roles for Authentik, GitLab, and MAAS, stores
their credentials in Vault, and verifies role boundaries. The Ansible role
`roles/postgresql_install` owns installation and host configuration. The complete
decision is in [`docs/database-architecture.md`](database-architecture.md).

### Inputs

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| `config.yaml` | Stage 0 output | Yes | |
| `config.bootstrap_server_ip` | `config.yaml` | Yes | PostgreSQL service address |
| Bootstrap token and unsealed Vault | Stage 3 | Yes | Used for database credential records |
| Target arch | Stage 0 run manifest | Yes | PostgreSQL package selection |
| Port 5432 available | Network prerequisite | Yes | |
| Target disk space >= 20 GB | Target prerequisite | Yes | |

### Outputs / Postconditions

1. The pinned PostgreSQL version is running and healthy.
2. PostgreSQL admin credentials are stored at `secret/data/bootstrap/postgres/admin`.
3. Dedicated `authentik`, `gitlab`, and `maas` databases and roles exist.
4. Each service credential is stored at `secret/data/bootstrap/postgres/<service>`.
5. Each role can connect to its own database and cannot connect to another
   service's database.
6. Network access is limited to configured service hosts/networks.
7. TUI log contains `[INFO] stage=postgres status=complete`.

### Acceptance Criteria

1. A fresh Stage 4 installs PostgreSQL and creates all postconditions.
2. A second run makes no destructive database or role changes.
3. Existing non-empty databases are never dropped, emptied, or silently adopted.
4. Passwords do not appear in logs, TUI output, process arguments, or structural config.
5. A database/role mismatch stops with an explicit repair decision.
6. TUI states include checking, installing, creating service databases,
   verifying role isolation, and `Stage 4: PostgreSQL — complete`.

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Vault sealed | Seal-status check | Halt, instruct unseal, retry Stage 4 |
| Install or service start fails | Ansible/systemd failure | Preserve data; emit logs; retry |
| Conflicting database or role | Catalog differs from expected ownership | Stop without mutation; present conflict |
| Connectivity or isolation check fails | Positive or negative `psql` check differs | Stop; show database/role involved without secret values |

### Test Cases

- Fresh QEMU run after Stages 0–3 creates all three databases and Vault records.
- A second Stage 4 run reports no unintended changes.
- Cross-service connection attempts are rejected.
- A pre-existing non-empty `authentik` database is not overwritten or inferred
  to be a supported brownfield import.
- Sealed Vault and occupied-port injections halt before database creation.

---

## Stage 5: Authentik

### Summary

Stage 5 deploys Authentik directly against the final PostgreSQL database created
in Stage 4. The bootstrap compose definition has no bundled PostgreSQL service.
After first-start schema initialization and health checks, Terraform creates
groups, OIDC providers, applications, and policy bindings. The stage completes
Vault's OIDC configuration and records generated client credentials in Vault.

### Inputs

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| `config.yaml` | Stage 0 output | Yes | |
| Bootstrap token and unsealed Vault | Stage 3 | Yes | Vault reads/writes |
| Authentik database and credential | Stage 4 | Yes | Final database used from first start |
| DNS and wildcard TLS | Stages 1–2 | Yes | Service hostname and HTTPS |
| Docker and ports 9000/9443 | Target prerequisite | Yes | |

### Outputs / Postconditions

1. Authentik is healthy at `https://authentik.home.<domain>`.
2. Its runtime configuration points to the Stage 4 PostgreSQL database.
3. No bundled PostgreSQL container, temporary database, or migration artifact exists.
4. Bootstrap admin credentials are stored in Vault.
5. Groups `adults` and `kids` exist.
6. Vault and GitLab OIDC providers/applications exist with correct redirect URIs.
7. Their client credentials are stored under
   `secret/data/bootstrap/authentik/oidc/<consumer>`.
8. Vault OIDC discovery and callback configuration are verified.
9. TUI log contains `[INFO] stage=authentik status=complete`.

### Acceptance Criteria

1. Stage 5 refuses to start if Vault or the final Authentik database is unhealthy.
2. The rendered compose and runtime configuration contain no bundled PostgreSQL service.
3. First startup waits up to 300 seconds for schema initialization and health.
4. An interrupted first startup can be retried without dropping the database.
5. Groups, providers, applications, and Vault records reconcile idempotently.
6. No database or OIDC secret is printed or written to structural configuration.
7. TUI states include database verification, deploying, schema initialization,
   health, OIDC configuration, and `Stage 5: Authentik — complete`.

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Vault sealed or token expired | Vault API check | Halt; unseal or refresh Stage 3 token; retry |
| Final database unreachable | Authentik-role connection check | Make no container change; repair Stage 4 and retry |
| Compose deployment fails | Non-zero Compose result | Preserve DB and runtime config; emit logs; retry |
| First-start health timeout | Health endpoint and container logs | Preserve initialized data; diagnose and retry; never restore/drop automatically |
| OIDC reconciliation fails | Authentik or Vault API error | Leave healthy service running; retry configuration idempotently |

### Test Cases

- A fresh Stage 5 starts against the Stage 4 database and completes OIDC setup.
- Compose inspection proves no PostgreSQL service or migration command exists.
- A second run produces no duplicate groups, providers, or applications.
- A forced first-start interruption resumes without data loss.
- Seeded users, groups, flows, and OIDC configuration survive a PostgreSQL
  backup/restore drill.
- Browser login through Authentik to Vault succeeds.

---

## Stage 6: GitLab + Runner

### Summary

Stage 6 installs GitLab CE and a GitLab Runner, configures GitLab as the GitOps platform, establishes the Terraform HTTP state backend, and wires Vault JWT auth for pipeline secret injection. GitLab installation uses the Omnibus package for arch compatibility. GitLab is configured with Authentik OIDC for login and with the shared PostgreSQL instance. After installation, Stage 6 creates the `infrastructure/iac` project (Terraform state repository), registers the Runner with tags `bootstrap`, `terraform`, `ansible`, and configures Vault JWT auth with a `gitlab-ci` role. This is the last stage required before Terraform state migration and the formal GitOps handoff. The Ansible role `roles/gitlab_install` handles Omnibus deployment.

### Inputs

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| `config.yaml` | Stage 0 output | Yes | |
| `config.bootstrap_server_ip` | `config.yaml` | Yes | GitLab binds here |
| Bootstrap token | Stage 3 run context (in-memory) | Yes | All Vault reads/writes |
| Vault unsealed | Stage 3 postcondition | Yes | Checked at stage start; halt if sealed |
| PostgreSQL DSN for `gitlab` db | `secret/data/bootstrap/postgres/gitlab` in Vault | Yes | Injected as env var; not written to disk in plaintext |
| Authentik OIDC client for GitLab | `secret/data/bootstrap/authentik/oidc/gitlab` in Vault | Yes | `client_id` and `client_secret` |
| DNS resolution of `gitlab.home.<domain>` | Stage 1 postcondition | Yes | |
| Wildcard TLS cert | `secret/data/bootstrap/tls/wildcard` in Vault | Yes | Used for GitLab HTTPS |
| Ports 80 and 443 available on target | Network prerequisite | Yes | GitLab Omnibus defaults |
| Target: >= 4 GB RAM, >= 20 GB disk | Target prerequisite | Yes | GitLab Omnibus minimum |
| Target arch | Stage 0 run manifest | Yes | Omnibus package selection (x86_64 or aarch64) |

### Outputs / Postconditions

1. GitLab CE is running at `https://gitlab.home.<domain>`; `GET /-/health` returns HTTP 200
2. GitLab admin account exists; credentials in Vault at `secret/data/bootstrap/gitlab/admin`
3. GitLab is configured with Authentik OIDC provider for login
4. GitLab top-level group `infrastructure` exists
5. GitLab top-level group `services` exists
6. GitLab project `infrastructure/iac` exists with Terraform HTTP backend configured
7. Project access token for Terraform state is in Vault at `secret/data/bootstrap/gitlab/terraform-token`
8. GitLab Runner `bootstrap-runner` is registered with tags `bootstrap`, `terraform`, `ansible`; Runner service is running on the target
9. Vault JWT auth method is enabled for GitLab CI token injection
10. Vault role `gitlab-ci` exists: allows reading `secret/data/services/*` and `secret/data/infrastructure/*` with TTL `1h`
11. Vault JWT auth configured: GitLab CI pipelines can authenticate to Vault using JWT tokens
12. `terraform/backend.tf.example` is committed to `infrastructure/iac` repo showing HTTP backend config format
13. TUI log contains: `[INFO] stage=gitlab status=complete`
14. Log file contains: `[INFO] gitlab-tf-backend: terraform state project configured` and `[INFO] gitlab-runner: registered name=bootstrap-runner`

### Acceptance Criteria

1. Running Stage 6 on a target with no GitLab installs via `roles/gitlab_install` (Omnibus), runs `gitlab-ctl reconfigure`, waits up to 600s for health, and produces all postconditions
2. Running Stage 6 when GitLab is already running skips installation and jumps to Runner registration and Vault integration idempotency checks
3. All secrets (PostgreSQL DSN, Authentik OIDC client secret, GitLab secrets) are injected as environment variables into `gitlab.rb` template rendering; none appear in log files, TUI, or on-disk plaintext config
4. GitLab startup health poll logs progress every 60 seconds: `[INFO] gitlab-wait: elapsed=Xs`
5. GitLab groups `infrastructure` and `services` are created once; second run skips creation
6. `infrastructure/iac` project is created once; second run skips creation; Terraform HTTP backend configuration is idempotent
7. Terraform project access token is stored in Vault at `secret/data/bootstrap/gitlab/terraform-token`; token value is not in any log file or TUI output
8. Runner `bootstrap-runner` is registered once; if already registered (detected by Runner name in API response), registration is skipped; Runner health is verified
9. Vault JWT auth and `gitlab-ci` role are created once; second run detects existing auth method and role and skips
10. OIDC smoke test passes: Authentik redirect URI for GitLab callback `https://gitlab.home.<domain>/users/auth/openid_connect/callback` matches the registered Authentik application
11. TUI displays all required observable states: `GitLab: checking for existing instance...`, `GitLab: installing (5–10 min)...`, `GitLab: waiting for startup (Xs)...`, `GitLab: configuring admin and groups...`, `GitLab: registering Runner...`, `GitLab: configuring Vault JWT auth...`, `Stage 6: GitLab + Runner — complete`

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Vault sealed at stage start | `GET /v1/sys/seal-status` returns sealed | Halt; TUI error; instruct operator to unseal; retry stage |
| GitLab reconfigure fails (config syntax error, port conflict, PostgreSQL unreachable) | `gitlab-ctl reconfigure` non-zero exit | Dump reconfigure output to log file; TUI notes common causes (port 80 in use, PostgreSQL unreachable); `[VERIFY]` retry or abort |
| GitLab health timeout (600s) | No HTTP 200 from `/-/health` within timeout | Dump `gitlab-ctl tail` to log file; emit path to TUI; `[VERIFY]` retry (GitLab may still be migrating DB) or abort |
| API config fails (wrong admin token, API rate limit) | GitLab API non-2xx response | Emit specific API error; `[VERIFY]` retry or abort |
| Runner registration fails (wrong registration token) | Runner register returns error | Emit specific error; check token generation step; `[VERIFY]` retry |
| Vault JWT config fails (bootstrap token expired) | Vault API returns 403 | Emit token expiry warning; instruct operator to run `bootstrap --stage vault` to refresh; retry stage |

### Test Cases

#### Happy Path

- Boot fresh QEMU VM (minimum 4 GB RAM assigned); run Stages 0–5
- Run `just test-stage gitlab`
- Assert: `GET https://gitlab.home.test/-/health` returns HTTP 200
- Assert: `infrastructure/iac` project exists via GitLab API
- Assert: `bootstrap-runner` is registered and active via `GET /api/v4/runners`
- Assert: Vault JWT auth method exists via `vault auth list`
- Assert: Vault role `gitlab-ci` exists with correct policies
- Assert: `secret/data/bootstrap/gitlab/terraform-token` readable from Vault
- Assert: log contains `[INFO] stage=gitlab status=complete`

#### Idempotency

- After happy path run, run Stage 6 a second time without resetting VM
- Assert: installation step is skipped (log: "already running")
- Assert: group `infrastructure` creation is skipped
- Assert: group `services` creation is skipped
- Assert: project `infrastructure/iac` creation is skipped
- Assert: Runner registration is skipped (runner already registered)
- Assert: Vault JWT auth enable is skipped
- Assert: stage completes without error

#### Failure Injection

- **GitLab reconfigure fails**: write an invalid PostgreSQL DSN to Vault before Stage 6; assert reconfigure fails; assert output is dumped to log; assert TUI shows common causes; assert `[VERIFY]` prompt appears
- **Health timeout**: misconfigure GitLab to prevent startup (e.g. occupy port 443); assert timeout at 600s; assert `gitlab-ctl tail` output in log; assert `[VERIFY]` prompt
- **Vault sealed**: seal Vault before Stage 6; assert halt at seal check; assert no GitLab operations attempted

#### Edge Cases

- ARM64 QEMU VM: assert Omnibus ARM64 package is installed; assert GitLab starts on ARM
- GitLab startup takes 8 minutes: assert health poll does not give up before 600s

---

## Stage 7: MAAS

### Summary

Stage 7 deploys MAAS (Metal as a Service) as the bare metal provisioning
system for the lab VLAN. Ansible installs and initializes MAAS; Terraform owns
the fabric, VLAN, subnet, and ranges. DHCP remains disabled by default. When
`maas_dhcp_enabled` is explicitly true, an interactive cutover may make MAAS
the DHCP authority for only the dedicated lab VLAN after the operator disables
UniFi DHCP for that VLAN and all safety gates pass. Image import is asynchronous
and does not block stage completion.

### Inputs

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| `config.yaml` | Stage 0 output | Yes | |
| `config.bootstrap_server_ip` | `config.yaml` | Yes | MAAS installs here |
| `config.lab_vlan_subnet` | `config.yaml` | Yes | CIDR for lab segment (e.g. `10.10.10.0/24`) |
| `config.lab_vlan_id` | `config.yaml` | Yes | VLAN ID configured on UniFi |
| `config.lab_vlan_interface` | `config.yaml` | Conditional | Required when MAAS DHCP is enabled; receives the lab VLAN |
| `config.lab_gateway` | `config.yaml` | Yes | Must be contained by the lab subnet |
| `config.dhcp_range_low`, `config.dhcp_range_high` | `config.yaml` | Conditional | Required when MAAS DHCP is enabled; must not include infrastructure addresses |
| `config.maas_dhcp_enabled` | `config.yaml` | No | Defaults to `false`; starts the deliberate DHCP cutover |
| Bootstrap token | Stage 3 run context (in-memory) | Yes | All Vault reads/writes |
| Vault unsealed | Stage 3 postcondition | Yes | Checked at stage start; halt if sealed |
| PostgreSQL DSN for `maas` db | `secret/data/bootstrap/postgres/maas` in Vault | Yes | Injected as env var; not logged |
| DNS resolution of `maas.home.<domain>` | Stage 1 postcondition | Yes | |
| Technitium DNS IP | Stage 1 postcondition | Yes | Set as MAAS upstream DNS |
| Lab VLAN trunked to bootstrap server network interface | Physical/UniFi prerequisite | Conditional | Required before enabling MAAS DHCP |
| Ports 5240 (HTTP) and 5241 (HTTPS) available on target | Network prerequisite | Yes | |
| Target arch | Stage 0 run manifest | Yes | MAAS snap/deb variant |

### Outputs / Postconditions

1. MAAS is running at `http://maas.home.<domain>:5240/MAAS`; `GET /MAAS/api/2.0/version/` returns HTTP 200
2. MAAS admin credentials are in Vault at `secret/data/bootstrap/maas/admin`
3. MAAS upstream DNS is configured to use the Technitium DNS IP
4. MAAS domain is set to `maas.home.<domain>`
5. Lab VLAN fabric and subnet (`config.lab_vlan_subnet`) are defined in MAAS and
   owned by the MAAS Terraform state
6. DHCP remains disabled unless `config.maas_dhcp_enabled` is true and the
   approved cutover completes
7. Ubuntu 22.04 and 24.04 image imports are initiated (background); import status is logged
8. MAAS Terraform API key is in Vault at `secret/data/bootstrap/maas/terraform-api-key`
9. TUI log contains: `[INFO] stage=maas status=complete`
10. Log file contains the network validation result and an explicit
    `dhcp=disabled`, `dhcp=enabled`, or `dhcp=rollback` outcome

### Acceptance Criteria

1. Running Stage 7 on a target with no MAAS installs via `roles/maas_install`, runs `maas init region+rack`, waits up to 300s for the API, and produces all postconditions
2. Running Stage 7 when MAAS is already running skips installation and jumps to network config idempotency
3. PostgreSQL DSN is injected via environment variable into the `maas init` command; DSN is not present in any log file or TUI output
4. With `maas_dhcp_enabled: false`, Stage 7 makes no DHCP-authority change and
   completes with `MAAS: DHCP disabled (default)`
5. OS image import is initiated as a background operation; stage completion does not wait for images to finish downloading; TUI shows "MAAS: importing OS images (background — may take several minutes)..." and moves on
6. MAAS Terraform API key is stored in Vault and not present in any log file or TUI output
7. `maas.home.<domain>` A record is verified (may already exist from Stage 1); no duplicate records are created
8. Upstream DNS in MAAS is set to Technitium's IP; MAAS does not use a public resolver directly
9. An enabled cutover shows the interface, VLAN, subnet, gateway, range, current
   DHCP observation, proposed MAAS authority, UniFi step, and rollback procedure
10. After the operator confirms UniFi DHCP is disabled on only the lab VLAN, a
    repeated bounded probe must observe no conflicting DHCP offer before MAAS is enabled
11. Failed enablement disables partial MAAS DHCP and instructs the operator to
    restore the recorded UniFi DHCP settings
12. TUI displays all required observable states: `MAAS: checking for existing instance...`, `MAAS: installing...`, `MAAS: configuring network...`, `MAAS: DHCP disabled (default)` or the explicit cutover states, `MAAS: importing OS images (background)...`, `Stage 7: MAAS — complete`

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Vault sealed at stage start | `GET /v1/sys/seal-status` returns sealed | Halt; TUI error; instruct operator to unseal; retry stage |
| MAAS snap install fails (network, snap store unreachable) | Non-zero snap install exit code | Emit error + log path; `[VERIFY]` retry or abort |
| MAAS database init fails (wrong DSN, PostgreSQL not accepting connections) | `maas init` non-zero exit | Check PostgreSQL connectivity from target; verify `maas` user permissions; emit instructions; retry `bootstrap --stage maas` |
| MAAS health timeout (300s) | No HTTP 200 from `/MAAS/api/2.0/version/` within timeout | Emit timeout error; `[VERIFY]` retry or abort |
| DHCP prerequisites fail | Interface/VLAN missing; invalid or overlapping subnet/range; Technitium unhealthy | Refuse cutover without changing DHCP |
| Existing DHCP authority detected | Bounded probe observes a DHCP offer after the operator's UniFi confirmation | Refuse to enable MAAS; identify the observed server when possible |
| MAAS DHCP enable or verification fails | MAAS API error or test client cannot obtain a correct lease | Disable partial MAAS DHCP; direct operator to restore UniFi DHCP for the lab VLAN |
| OS image import fails (no internet, wrong arch) | MAAS image import status API returns error | Non-blocking warning emitted to TUI; images can be re-imported post-bootstrap; stage continues |

### Test Cases

#### Happy Path

- Boot fresh QEMU VM with lab VLAN configured; run Stages 0–6
- Run `just test-stage maas`
- Assert: `GET http://maas.home.test:5240/MAAS/api/2.0/version/` returns HTTP 200
- Assert: lab VLAN subnet visible in MAAS fabric/subnet API
- Assert: DHCP remains disabled with the default configuration
- Assert: `secret/data/bootstrap/maas/terraform-api-key` readable from Vault
- Assert: MAAS upstream DNS is Technitium IP
- Assert: log contains `[INFO] stage=maas status=complete`

#### Idempotency

- After happy path run, run Stage 7 a second time without resetting VM
- Assert: MAAS install step is skipped (log: "already running")
- Assert: VLAN/subnet config step is skipped (already exists)
- Assert: DHCP remains disabled and no authority-change API request is made
- Assert: image import step notes images already imported or import already in progress
- Assert: stage completes without error

#### Failure Injection

- **Database init fails**: configure `maas` Vault credentials with wrong password before Stage 7; assert `maas init` failure is caught; assert error message surfaces database connectivity issue; assert `[VERIFY]` prompt appears
- **DHCP conflict**: enable the opt-in in an isolated test network with another
  DHCP responder; assert bootstrap refuses to enable MAAS
- **DHCP activation rollback**: force the post-enable lease check to fail; assert
  MAAS DHCP is disabled and the TUI displays the UniFi restoration step
- **Image import non-blocking**: block outbound HTTP on QEMU VM after MAAS starts; assert image import failure emits warning (not error); assert stage completes despite image import failure

#### Edge Cases

- ARM64 QEMU VM: assert MAAS ARM64 snap/deb is installed; assert API starts correctly on ARM
- Lab VLAN subnet conflicts with an existing MAAS subnet: assert the Terraform
  plan surfaces the conflict and no DHCP setting changes

---

## Stage 8: k3s Clusters

### Summary

Stage 8 deploys k3s Kubernetes clusters as defined in `infrastructure/clusters/` within the `infrastructure/iac` GitLab project. Unlike Stages 1–7, this stage is IaC-driven: bootstrap's role is to trigger the initial Ansible and Terraform runs that establish each defined cluster. Node provisioning is decoupled from cluster definition — nodes may come from MAAS, Proxmox VMs, Proxmox LXC containers, or any other provisioner; Stage 8 only requires that nodes are SSH-reachable. For each cluster, Stage 8 installs k3s via `roles/k3s_install`, writes kubeconfigs to Vault, registers clusters as named Terraform targets, and triggers an initial Terraform apply via GitLab CI pipeline. Subsequent cluster changes go through GitOps (MR → pipeline → apply). If no cluster definitions exist, Stage 8 warns and completes successfully; bootstrap is still complete.

### Inputs

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| `config.yaml` | Stage 0 output | Yes | |
| Bootstrap token | Stage 3 run context (in-memory) | Yes | Vault reads/writes and GitLab clone |
| Vault unsealed | Stage 3 postcondition | Yes | Checked at stage start; halt if sealed |
| Cluster definitions | `infrastructure/clusters/*.yaml` in `infrastructure/iac` GitLab project | No | If none found, stage warns and completes |
| Node definitions | `infrastructure/nodes/*.yaml` in `infrastructure/iac` GitLab project | No | Each node definition references a provisioner; nodes must be SSH-reachable |
| SSH access to each node | Node provisioner postcondition | Yes (per node) | Key from `config.yaml`; unreachable nodes are warned and skipped |
| GitLab running with Terraform HTTP backend | Stage 6 postcondition | Yes | Terraform apply runs via GitLab CI pipeline |
| MAAS running (for MAAS-provisioned nodes) | Stage 7 postcondition | Conditional | Required only if any node uses `provisioner: maas` |
| Vault JWT auth for GitLab CI | Stage 6 postcondition | Yes | Pipeline uses JWT to authenticate to Vault |
| MAAS Terraform API key | `secret/data/bootstrap/maas/terraform-api-key` in Vault | Conditional | Required for `provisioner: maas` nodes |

### Outputs / Postconditions

1. For each defined cluster: k3s is installed on all reachable nodes with correct roles (control-plane, worker)
2. For each defined cluster: all nodes are `Ready` per `kubectl get nodes`
3. For each defined cluster: kubeconfig is written to `secret/data/bootstrap/k3s/<cluster-name>/kubeconfig` in Vault
4. For each defined cluster: cluster endpoint and kubeconfig reference are in Vault at `secret/data/infrastructure/clusters/<name>`
5. For each defined cluster: initial Terraform apply via GitLab CI pipeline has completed; cluster state is in GitLab HTTP backend
6. If no cluster definitions exist: stage emits warning and completes; no failure
7. Unreachable nodes are warned and skipped; cluster deploy continues with available nodes; no hard failure for a single unreachable node
8. TUI log contains: `[INFO] stage=k3s status=complete`

### Acceptance Criteria

1. Running Stage 8 with one cluster definition containing one node that is SSH-reachable installs k3s on that node, writes kubeconfig to Vault, and triggers a GitLab CI pipeline that runs `terraform apply` for the cluster Terraform module
2. Running Stage 8 when no cluster definitions exist produces a `[VERIFY]` prompt warning that no clusters are defined; operator confirmation allows stage to complete without deploying any clusters
3. Running Stage 8 when a cluster's kubeconfig already exists in Vault (`secret/data/bootstrap/k3s/<cluster-name>/kubeconfig`) skips k3s installation for that cluster and verifies health instead
4. A node that is not SSH-reachable at stage time produces a warning in TUI and log; that node is skipped; the cluster deploy proceeds with remaining nodes
5. After k3s install, `kubectl get nodes --kubeconfig=<path>` shows all installed nodes as `Ready` before the Terraform pipeline is triggered
6. GitLab CI pipeline triggered for each cluster: pipeline URL is emitted to TUI; stage polls pipeline status up to 600s; pipeline success is required for the cluster to be marked complete
7. If a Terraform pipeline fails, the pipeline URL is emitted to TUI for operator inspection; `[VERIFY]` prompt offers retry or skip; a skipped cluster does not fail the overall stage
8. Kubeconfig written to Vault does not appear in any log file or TUI output
9. Node provisioner type (MAAS, Proxmox VM, Proxmox LXC) does not affect Stage 8 behavior; Stage 8 only requires SSH-reachability, regardless of how the node was provisioned
10. TUI displays all required observable states: `k3s: installing cluster <name>...`, `k3s: joining node <node> to <cluster>...`, `k3s: verifying cluster <name>...`, `k3s: running Terraform apply via GitLab CI...`

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Vault sealed at stage start | Seal check returns sealed | Halt; TUI error; instruct operator to unseal; retry stage |
| Node SSH unreachable | SSH connect attempt fails within timeout | Warn and skip node; do not fail entire stage; log: `[WARN] k3s: node=<node> unreachable; skipping` |
| k3s install fails (incompatible arch, resource limits) | Non-zero exit from `roles/k3s_install` | Emit error + log path; `[VERIFY]` retry cluster or skip cluster |
| Cluster not healthy after install (node not joining, network issue) | `kubectl get nodes` does not show all nodes `Ready` within 120s | Dump `kubectl describe node` to log file; `[VERIFY]` retry or skip cluster |
| Terraform pipeline fails (GitLab CI config error, Vault auth failure) | Pipeline API shows failed status | Emit pipeline URL to TUI; `[VERIFY]` retry or skip |

### Test Cases

#### Happy Path

- Boot fresh QEMU VM (or two VMs for a two-node cluster test); run Stages 0–7
- Define a cluster in `infrastructure/clusters/test-cluster.yaml` with one control-plane node (the QEMU VM)
- Define the node in `infrastructure/nodes/test-node.yaml` with `provisioner: proxmox-vm` (or equivalent) pointing at the QEMU VM IP
- Run `just test-stage k3s`
- Assert: k3s is installed on the test node
- Assert: `kubectl get nodes` shows node `Ready`
- Assert: kubeconfig at `secret/data/bootstrap/k3s/test-cluster/kubeconfig` is readable
- Assert: Terraform pipeline triggered and completed successfully
- Assert: log contains `[INFO] stage=k3s status=complete`

#### Idempotency

- After happy path run, run Stage 8 a second time
- Assert: k3s install step is skipped (kubeconfig already in Vault)
- Assert: cluster health is verified instead of re-installed
- Assert: Terraform pipeline: `terraform plan` shows no changes; `terraform apply` makes no changes
- Assert: stage completes without error

#### Failure Injection

- **Node SSH unreachable**: define a node with an unreachable IP; assert warning is emitted; assert node is skipped; assert cluster deploys with remaining nodes (if any)
- **Terraform pipeline fails**: configure a deliberate Terraform error in the test cluster module; assert pipeline failure is detected; assert pipeline URL is emitted to TUI; assert `[VERIFY]` prompt appears
- **No cluster definitions**: run Stage 8 with no `infrastructure/clusters/*.yaml` files; assert `[VERIFY]` warning prompt appears; assert confirming completes the stage without error

#### Edge Cases

- Two-node cluster (one control-plane, one worker): assert control-plane is installed first; worker joins successfully; `kubectl get nodes` shows both `Ready`
- Cluster with MAAS-provisioned nodes: assert Stage 8 does not care about provisioner type; only SSH reachability matters
- Cluster with Proxmox LXC nodes: same assertion — provisioner is opaque to Stage 8

---

## Cross-Cutting Flow: Terraform State Migration

### Summary

Terraform state migration is the formal completion event of the bootstrap process (PRD SC-14). It runs automatically after all 9 stages (0–8) complete. Local `.tfstate` files created during bootstrap are migrated to the GitLab HTTP backend one at a time; each is verified before the local copy is deleted. The migration is non-destructive: local state remains authoritative until each individual file is confirmed migrated. After all state is migrated, a `bootstrap-complete.json` marker is written and the TUI displays the completion summary. This flow uses `terraform/modules/<name>` backend configs and the Terraform project access token from `secret/data/bootstrap/gitlab/terraform-token`.

### Inputs

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| All stages 0–8 complete | Stage completion markers | Yes | Migration does not run if any stage failed |
| Local `*.tfstate` files | `./terraform/` directory | Conditional | If none exist, migration is a no-op |
| GitLab `infrastructure/iac` project | Stage 6 postcondition | Yes | Terraform HTTP backend target |
| Terraform project access token | `secret/data/bootstrap/gitlab/terraform-token` in Vault | Yes | HTTP backend authentication |
| GitLab HTTP backend URL | `config.yaml` + Stage 6 output | Yes | `https://gitlab.home.<domain>/api/v4/projects/<id>/terraform/state/<workspace>` |
| Vault unsealed | Runtime check | Yes | Token read at migration time |

### Outputs / Postconditions

1. All local `*.tfstate` files that existed before migration are gone from `./terraform/`
2. Each migrated state is readable from the GitLab HTTP backend: `GET /api/v4/projects/<id>/terraform/state/<workspace>` returns the state content
3. `./logs/run-<timestamp>/bootstrap-complete.json` exists with: timestamp, all stage completion times, GitLab URL, Vault URL
4. TUI displays bootstrap completion summary: all service URLs, `vault-init.json` location reminder, GitLab first-login instructions, GitOps handoff notice
5. Log file contains: `[INFO] bootstrap: COMPLETE; all stages done; state migrated to gitlab`
6. No local `.tfstate` files remain after confirmed migration

### Acceptance Criteria

1. For each local `*.tfstate` file: `backend.tf` is updated to point to the GitLab HTTP backend; `terraform init -migrate-state` succeeds; state is readable from GitLab API before the local file is deleted
2. Local `*.tfstate` file is NOT deleted until the GitLab HTTP backend read is confirmed successful
3. A migration failure for one state file produces a `[VERIFY]` prompt; the operator can retry that state or skip it; skipping leaves local state intact as fallback; migration continues with remaining files
4. If no local `*.tfstate` files exist, migration completes immediately as a no-op with no error
5. Operator receives a `[VERIFY]` confirmation prompt after all migrations succeed before the completion marker is written
6. `bootstrap-complete.json` is written only after operator confirms
7. The completion summary TUI screen includes: Vault URL, GitLab URL, Authentik URL, Technitium URL, MAAS URL, location of `vault-init.json`, and the statement that all future changes go through `infrastructure/iac` GitOps
8. Terraform project access token used for HTTP backend authentication is not present in any log file, TUI output, or `backend.tf` file on disk after migration (token is in Vault; `backend.tf` references the env var form)
9. Each migrated state has the same lineage and serial in GitLab as the local state used to initiate migration
10. A clean-bootstrap handoff requires no Terraform imports because Terraform created all Terraform-owned resources

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| `terraform init -migrate-state` fails (wrong backend URL, auth failure) | Non-zero exit from `terraform init` | Do NOT delete local state file; emit full `terraform` error output to TUI; `[VERIFY]` retry individual state or skip (local state remains valid fallback) |
| GitLab HTTP backend not responding during verification | HTTP GET to state endpoint returns non-2xx or times out | Do NOT delete local state file; emit error; `[VERIFY]` retry or skip |

### Test Cases

#### Happy Path

- After full bootstrap run (`just test-bootstrap`), one or more `*.tfstate` files exist in `./terraform/`
- Run Terraform state migration (triggered automatically or via `bootstrap --migrate-state`)
- Assert: `terraform init -migrate-state` succeeds for each state workspace
- Assert: each state is readable from GitLab HTTP backend via API call
- Assert: local `*.tfstate` files are deleted after confirmed migration
- Assert: `./logs/run-<timestamp>/bootstrap-complete.json` exists with correct fields
- Assert: TUI completion summary displayed with all service URLs

#### Idempotency

- Run migration a second time when no local `*.tfstate` files exist
- Assert: migration completes as a no-op without error
- Assert: completion summary still displays correctly

#### Failure Injection

- **`terraform init -migrate-state` fails**: configure backend URL to point at a wrong endpoint; assert local state file is NOT deleted; assert error is emitted to TUI; assert `[VERIFY]` prompt appears with retry and skip options
- **GitLab backend verification fails**: successfully run `terraform init -migrate-state` but block the subsequent GET verification; assert local state is NOT deleted; assert `[VERIFY]` prompt

#### Edge Cases

- Multiple state workspaces: assert each is migrated independently; a failure on one does not prevent migration of others
- State workspace that was already migrated (no local file exists): assert migration is skipped for that workspace without error

---

## Cross-Cutting Flow: Post-Bootstrap Service Deploy

### Summary

Post-bootstrap service deployment is entirely GitOps-driven via the `infrastructure/iac` GitLab project. A developer writes a service definition YAML using the service definition schema from the PRD, opens a merge request, reviews the automated `terraform plan` output posted as an MR comment, and merges. On merge, `terraform apply` runs automatically: DNS record, optional Authentik OIDC provider + application, and Helm chart deployment (or MAAS provisioning + Ansible role for bare metal) are all handled without any operator interaction with the bootstrap tool.

### Inputs

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| Service definition YAML | Developer, committed to `infrastructure/iac` repo | Yes | Must conform to service definition schema in PRD |
| `infrastructure/iac` project accessible | Stage 6 postcondition | Yes | GitLab must be running |
| Terraform HTTP backend | Stage 6 postcondition | Yes | State stored in GitLab |
| Vault secrets for the service | Developer pre-populated or auto-generated | Conditional | Required if service needs secrets |
| Named k3s cluster (for `target.cluster`) | Stage 8 postcondition | Conditional | Required if service targets a k3s cluster |
| Named MAAS machine (for `target.maas_machine`) | Stage 7 postcondition | Conditional | Required if service targets bare metal |

### Outputs / Postconditions

1. DNS A record for `service.hostname` exists in Technitium
2. If `authentik: true`: Authentik OIDC provider and application exist; outbound proxy is configured; service requires login
3. If `authentik: false`: service is reachable directly; no SSO proxy; access enforced by `allowed_groups` at application level
4. Service is reachable at `service.hostname` within 10 minutes of MR merge
5. GitLab CI pipeline for the merge shows success status
6. No bootstrap tool involvement after Stage 6 completes

### Acceptance Criteria

1. Merging a valid service definition MR triggers a GitLab CI pipeline that runs `terraform apply` within the pipeline SLA
2. `terraform plan` output is posted as an MR comment before merge; plan shows exactly the resources to be created (DNS record, optional Authentik objects, Helm release)
3. After `terraform apply` completes, DNS record for `service.hostname` is resolvable from the lab VLAN
4. A service with `authentik: true` requires Authentik login when accessed at its hostname; unauthenticated requests are redirected to Authentik
5. A service with `authentik: false` is reachable directly without SSO proxy
6. `allowed_groups` is enforced: a user not in any listed group is denied access
7. An invalid service definition (schema violation) causes the `terraform plan` step to fail; the MR is blocked; the developer receives an error message in the MR comment
8. The full deploy cycle (MR opened → plan comment → merge → apply → service reachable) completes in < 10 minutes

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| `terraform plan` fails (schema violation, missing cluster, invalid hostname) | Pipeline fails on plan step | MR blocked; plan output in MR comment; developer fixes definition and pushes |
| Helm chart deploy fails | Pipeline fails on apply step; GitLab CI shows error | Developer inspects GitLab CI logs; fixes chart values or cluster config; re-runs pipeline |
| DNS record creation fails (Technitium API error) | Terraform Technitium provider returns error | Pipeline fails; developer or operator inspects Terraform output; resolves Technitium API issue; re-runs pipeline |

### Test Cases

#### Happy Path

- After full bootstrap (`just test-bootstrap`), write a minimal service definition to `services/test-svc.yaml`
- Open MR against `infrastructure/iac` default branch
- Assert: CI pipeline runs `terraform plan`; plan output posted as MR comment
- Assert: plan shows DNS record creation for `test-svc.home.test`
- Merge MR
- Assert: CI pipeline runs `terraform apply`; pipeline shows success
- Assert: DNS record `test-svc.home.test` resolves to correct IP from lab VLAN
- Assert: deploy completes within 10 minutes of merge

#### Failure Injection

- **Invalid schema**: write a service definition missing the required `target` field; open MR; assert `terraform plan` fails; assert error is surfaced in MR comment; assert MR is blocked
- **Authentik OIDC creation fails**: configure Authentik to be unreachable during apply; assert `terraform apply` fails for the Authentik resource; assert pipeline shows failure

#### Edge Cases

- Service with `authentik: false` and `allowed_groups: [adults]`: assert service is reachable directly; assert group enforcement is configured correctly at application level
- Bare metal service (`target.maas_machine`): assert MAAS Terraform provider provisions machine; assert Ansible role is applied post-provision

---

## Cross-Cutting Flow: Reboot and Manual Unseal

### Summary

An ordinary reboot restores core platform services from protected runtime
credentials while Vault remains sealed. The platform reports degraded status and
fails Vault-dependent work closed. Masked manual unseal triggers reconciliation.
The complete contract is in
[`docs/reboot-recovery.md`](reboot-recovery.md).

### Postconditions before unseal

1. Technitium resolves private and external names.
2. PostgreSQL, Authentik, and GitLab pass application-level health checks.
3. MAAS and k3s start when configured, subject to their non-Vault dependencies.
4. Vault process is reachable but reports sealed.
5. Vault-dependent CI, providers, agents, secret delivery, and Raft snapshot
   creation are paused or fail closed.
6. Platform status is `degraded: Vault sealed`, never healthy.

### Acceptance Criteria

1. `bootstrap vault unseal` verifies pinned SSH and Vault cluster identity first.
2. Shares use masked input and never enter arguments, shell history, logs,
   manifests, temporary files, or crash output.
3. Wrong shares preserve valid Vault progress and re-prompt safely.
4. Successful unseal verifies Vault/Raft/audit health, OIDC, GitLab JWT, runtime
   secret-version metadata, deferred checks, and missed Vault backup.
5. Stale runtime copies produce a reviewed atomic reconciliation rather than a
   silent overwrite/restart.
6. Administrator absence leaves a stable documented degraded state.

### Required Tests

- Cold boot and core service health before unseal.
- Vault-dependent GitLab job and new workload-secret delivery fail closed.
- Correct, incorrect, reset-progress, and cluster-identity-change unseal paths.
- Matching and stale runtime-copy reconciliation.
- Complete/partial backup state before and after unseal.

---

## Cross-Cutting Flow: Backup and Recovery

### Summary

An Ansible-managed systemd timer creates native application-consistent artifacts
and stores them in the configured encrypted Restic repository. The wizard
presents the defaults and can disable or customize the feature. The full policy
and restore order are defined in
[`docs/backup-recovery.md`](backup-recovery.md).

### Inputs

| Input | Required | Default |
|---|---|---|
| `backup_enabled` | No | Wizard recommends `true`; structural default is `false` until a repository is supplied |
| `backup_repository` | When enabled | No inferred value |
| `backup_on_calendar` | No | Daily at 02:30 local |
| Daily/weekly/monthly retention | No | `7 / 5 / 12` |
| Metadata/full-data check intervals | No | Weekly / monthly |
| USB repository and cadence | No | Disabled; monthly reminder when enabled |
| Restore-drill interval | No | Quarterly |

### Outputs / Postconditions

1. Every successful run has a Restic snapshot ID and component manifest.
2. Native artifacts cover Vault, PostgreSQL, GitLab, Authentik, Technitium,
   MAAS, k3s when used, and bootstrap/GitOps recovery metadata.
3. Missing or failed component artifacts mark the run incomplete.
4. Retention, check, copy, and restore operations validate mount source and
   repository ID.
5. The status view reports last successful primary backup, last complete Vault
   snapshot, integrity-check result, and offline-copy age.

### Acceptance Criteria

1. Wizard users can accept all defaults, change every interval/retention field,
   or select `backup_enabled: false`.
2. Headless enablement without a repository fails validation.
3. A missing TrueNAS mount cannot result in a repository being written into the
   empty local mount directory.
4. The initial retention operation is dry-run-only; first large prune requires
   explicit approval.
5. USB copying validates filesystem UUID and both repository IDs, then verifies
   and safely unmounts the destination.
6. A NAS restore and a USB restore both pass in an isolated target.

### Failure Modes

| Failure | Recovery |
|---|---|
| TrueNAS unavailable or wrong mount | Stop before staging/backup; preserve live data and retry |
| Native artifact fails | Complete other artifacts, mark snapshot set incomplete, retain staging evidence |
| Vault sealed | Record incomplete Vault component and age of last valid Raft snapshot |
| Repository integrity failure | Disable prune/copy, preserve repository, diagnose or recover from USB |
| USB absent | Report offline-copy age; primary NAS backup remains successful |

---

## Cross-Cutting Flow: Upgrades and Maintenance

### Summary

Renovate proposes pinned application updates for normal GitLab review. A
proposal that reaches its configurable deadline becomes `approved-by-policy`
and is eligible in the next maintenance window. The host coordinator applies
one component at a time and never lets elapsed time bypass safety gates. The
complete contract is in [upgrade-policy.md](upgrade-policy.md).

### Inputs

| Input | Required | Default |
|---|---|---|
| `application_upgrades_enabled` | No | `true` |
| `upgrade_maintenance_on_calendar` | No | Sunday 03:00 target local time |
| `upgrade_maintenance_admission_window` | No | Three hours |
| Critical/other security deadline | No | `3d / 7d` |
| Patch-minor/major deadline | No | `21d / 30d` |
| `os_security_updates_enabled` | No | `true` |
| `os_automatic_reboot` | No | `false`; true is not accepted in v1 |

### Acceptance Criteria

1. Manual approval and deadline approval run the identical compatibility,
   backup, capacity, health, migration, and rollback gates.
2. A deadline labels the proposal `approved-by-policy`; it does not impersonate
   a human reviewer.
3. Only one component changes at a time and no new component starts after the
   admission window closes.
4. A failed gate makes no target mutation, preserves overdue state, and reports
   the exact blocker.
5. Trial mode permits stateless patch/minor immutable rollbacks but blocks
   data-bearing and major upgrades.
6. GitLab upgrades honor every required stop, pause Runner work, wait for
   background migrations, and continue through a host executor that survives
   GitLab downtime.
7. K3s never skips unsupported minor versions, upgrades server nodes before
   agents, and defaults to concurrency one.
8. Ubuntu automatic security updates exclude third-party application
   repositories and record—but never initiate—a required reboot.

### Failure Modes

| Failure | Recovery |
|---|---|
| Backup, health, compatibility, or capacity gate fails | Make no change; retain overdue state and retry preflight next window |
| Stateless rollout health fails | Restore prior immutable artifact and verify health |
| Failure after irreversible data migration | Stop; preserve diagnostics; require documented restore decision |
| Window closes between safe steps | Finish current step and resume remaining path next window |
| GitLab stops during its own upgrade | Host executor completes the current step independently |
| Prior component upgrade failed | Block the remaining queue until resolved |

---

## Cross-Cutting Flow: Constrained Observability

### Summary

Uptime Kuma, Beszel hub/agent, and the bootstrap health adapter run on the
target. TrueNAS supplies only Restic storage. Paging is independently
configurable and disableable. The full contract is in
[observability.md](observability.md).

### Inputs

| Input | Required | Default |
|---|---|---|
| `monitoring_enabled` | No | `true` |
| Critical / ordinary check interval | No | `60s / 5m` |
| Kuma and journal retention | No | `7d / 7d` |
| Memory / CPU / state budget | No | `512 MiB / 50% of one core / 5 GiB` |
| `paging_enabled` | No | `false` until a destination test succeeds |
| Paging provider | When paging enabled | Discord, Telegram, ntfy, or generic webhook |
| Failure counts / resource sustain | No | `3 / 3 / 15m` |
| Disk warning / critical | No | `85% / 95%` |
| Critical reminder | No | `6h` |

### Acceptance Criteria

1. Kuma materializes HTTP, TCP, DNS, certificate, and push checks from platform
   and service-definition intent.
2. Beszel records the accepted CPU, memory, disk/I/O, network, systemd,
   container, temperature, and SMART subset through a local Unix-socket agent.
3. History observes Kuma's seven-day cap and Beszel's one-minute through
   eight-hour aggregation tiers.
4. The stack remains inside configurable CPU, memory, and disk limits on x86_64
   and ARM64; optional collectors and noncritical cadence shed before core
   checks.
5. Discord, Telegram, ntfy, and webhook credentials pass masked entry and a
   test notification before paging is enabled.
6. `paging_enabled: false` preserves dashboards and reports
   `monitoring active; paging disabled`.
7. Maintenance and dependency suppression prevent cascaded noise; sustained
   thresholds, reminders, and recovery notifications follow the selected
   policy.
8. No observability workload is scheduled on TrueNAS.
9. A full target outage is documented as locally unpageable rather than
   reported as covered.

### Failure Modes

| Failure | Recovery |
|---|---|
| Notification test fails | Keep paging disabled; retry or select dashboard-only mode |
| Kuma or Beszel exceeds budget | Shed optional collection/cadence and mark telemetry degraded |
| Individual monitor fails | Apply debounce/dependency rules, page when configured, send one recovery |
| Monitoring state corrupt | Restore configuration; metric history is optional recovery data |
| Whole target/power/network fails | No local page is possible; retain the documented boundary |

---

## Cross-Cutting Flow: GitLab Loss and GitOps Recovery

### Summary

GitLab recovery combines a nightly full-instance set with an encrypted critical
checkpoint after each successful apply. An incident freeze blocks every
Terraform/GitOps mutation until exact-version restoration, optional newer
checkpoint reconciliation, verification, and explicit unfreeze. The complete
contract is in
[gitlab-disaster-recovery.md](gitlab-disaster-recovery.md).

### Inputs

| Input | Required | Default |
|---|---|---|
| Full GitLab backup schedule | No | Part of nightly 02:30 backup |
| `gitlab_checkpoint_after_apply` | No | `true` when a primary repository exists |
| `gitlab_freeze_iac_when_unavailable` | No | `true` |
| Full backup selection | Restore only | Operator selects complete verified set |
| Critical checkpoint selection | Restore only | Newest compatible commit/lineage/serial |
| Incident ID and unfreeze approval | Restore only | Required |

### Acceptance Criteria

1. Nightly protection contains the native GitLab archive, isolated database,
   configuration, secrets, TLS/SSH identity, Runner metadata, exact
   version/edition/package artifact and hashes, and one manifest.
2. Every successful apply creates and verifies Git bundles plus decrypted state
   for its applied commit, lineage, and serial in encrypted Restic.
3. Plaintext state and bundles exist only in root-only tmpfs and are absent
   after success or failure cleanup.
4. GitLab unavailability places a freeze marker honored by Runner, Terraform,
   GitOps, upgrade, and maintenance workflows.
5. Restore installs the exact version/edition and replaces only GitLab's
   isolated shared-PostgreSQL database.
6. A newer checkpoint reconciles only matching commit graphs and Terraform
   workspace/lineage/serial; conflicts require review.
7. Login, repository, clone/push, state re-download, read-only plan,
   decryption, Runner, backup, and monitoring checks pass before unfreeze.
8. Local Terraform state is break-glass-only and must reconcile back into
   GitLab before normal automation resumes.
9. Trial mode reports that post-apply checkpoints are not protected and may use
   the one-time encrypted controller export.

### Failure Modes

| Failure | Recovery |
|---|---|
| Missing config/secrets or wrong version/package | Block restore before mutation |
| Native restore fails | Keep freeze, preserve evidence, and allow bounded retry |
| Git bundle diverges | Stop automatic ref import and require review |
| Terraform lineage/serial conflicts | Keep base restored state and require state-selection review |
| Verification fails | Keep all automation frozen |
| Checkpoint fails after apply | Keep live infrastructure, report/page recovery gap, retry checkpoint |

---

## Cross-Cutting Flow: Controller Laptop Recovery

### Summary

A blank replacement laptop uses a verified bootstrap release, the password
manager, a verified Restic repository, and target access. It reconstructs
portable local state, verifies the pinned target identity, performs a read-only
audit, and requires explicit adoption. The complete contract is in
[controller-recovery.md](controller-recovery.md).

### Inputs

| Input | Required | Default |
|---|---|---|
| Verified bootstrap release or preserved artifact | Yes | Newest compatible verified artifact from bundle |
| Restic repository and password | Yes for automated recovery | Expected repository ID from operator records |
| Controller recovery password | When SSH key included | Password-manager entry; distinct from Restic |
| Target network or console access | Yes | Existing network access |
| Recovery mode | Yes at adoption | Old controller unavailable |
| Vault recovery material | For Vault unseal only | Entered separately; never sourced from bundle |

### Acceptance Criteria

1. Initial bootstrap and every material controller/configuration change create
   a schema-versioned, hashed, encrypted bundle.
2. The newest bundle joins nightly backups and every post-apply critical
   checkpoint.
3. The bundle contains portable configuration, target fingerprint,
   controller/target markers, backend/ownership metadata, run manifests, and a
   verified bootstrap artifact.
4. The included SSH private key requires both Restic access and the separate
   controller recovery password.
5. `vault-init.json`, Shamir shares, Restic passwords, and plaintext `.env`
   contents are absent.
6. Recovery rebinds laptop-specific paths and writes all protected local files
   with mode `0600`.
7. Existing-target recovery rejects a changed SSH fingerprint before
   authentication and uses the explicit changed-key flow for an intentional
   target replacement.
8. No remote mutation occurs during the audit; adoption requires the target
   name and selected mode.
9. Unavailable mode preserves old credentials and warns if the old controller
   was recently active.
10. Lost-or-compromised mode verifies replacement SSH/token access before
    revoking prior access and reports any incomplete rotation.
11. A new bundle is restore-opened and verified before recovery completes.
12. Trial mode can create a verified external export but remains unprotected
    when the only copy is on the controller laptop.

### Failure Modes

| Failure | Recovery |
|---|---|
| Repository ID, bundle hash/schema, or target ID differs | Stop and require source/target selection review |
| Saved SSH fingerprint differs | Stop before authentication; use explicit changed-key recovery |
| SSH key envelope cannot open | Offer new-key enrollment through physical or already trusted access |
| Another controller is recently active | Warn and require explicit adoption; do not auto-revoke |
| Credential rotation partly fails | Preserve verified replacement access and list unrevoked credentials |
| New recovery checkpoint fails | Keep adoption but mark controller protection degraded |

---

## Cross-Cutting Flow: Server Adoption (`just adopt-server`)

> Implementation record — 2026-08-27: the read-only audit, default-route PXE
> NIC selection, local BMC evidence, schema-valid node YAML, exact display,
> interactive/explicit approval, short-lived PAT refresh, draft MR creation, and
> duplicate-definition/open-MR guards are implemented and disposable-integration
> verified. Generated node definitions visibly authorize Ubuntu 24.04 deployment,
> and schema-validated Terraform creates both the reviewed `maas_machine` and
> the separate `maas_instance` allocation/deployment lifecycle. Main-branch
> apply runs after the reviewed MR merge. Physical PXE/deployment proof remains
> an external acceptance test. Optional k3s join is intentionally held at the
> accepted post-deploy SSH fingerprint gate; the target-state steps below are
> retained and must not be described as unattended automation yet.

### Summary

Server adoption is the day-2 workflow for adding a new physical server to the homelab. The operator runs `just adopt-server <ip>`; the bootstrap binary connects to the target, gathers hardware metadata, attempts BMC/IPMI detection, generates node and MAAS machine definitions, and opens a GitLab MR with pre-populated configuration. After the operator reviews and merges the MR, GitLab CI provisions the server via the Terraform MAAS provider, MAAS commissions it, and (if a cluster is referenced in the node definition) k3s is installed to join the node to the cluster. The operator never manually configures MAAS or writes Ansible inventory.

### Inputs

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| `<ip>` | Operator CLI argument | Yes | SSH-reachable target server IP |
| `config.yaml` | Filesystem | Yes | Must exist from prior bootstrap run |
| SSH key | `config.yaml` | Yes | Same key used during bootstrap |
| GitLab `infrastructure/iac` project | Stage 6 postcondition | Yes | MR target |
| Short-lived GitLab clone token | Vault (read at adopt time using bootstrap token or stored token) | Yes | Used to clone repo; expires after use |
| MAAS running | Stage 7 postcondition | Yes | MR merge triggers MAAS provisioning |

### Outputs / Postconditions

1. GitLab MR is opened at `https://gitlab.home.<domain>/infrastructure/iac/-/merge_requests/<id>`
2. MR title: `Draft: adopt: add node <hostname> (<ip>)`
3. MR body contains: hardware summary table (CPU, RAM, disk, NICs, MACs, arch), BMC/IPMI status (detected or not detected), suggested cluster assignment (if any)
4. `infrastructure/nodes/<hostname>.yaml` is committed to the MR branch with collected metadata and provisioner type
5. MR URL is printed to the operator's terminal
6. After MR merge: MAAS machine record is created with MAC address, arch, and BMC details
7. After MR merge: MAAS commissions machine (PXE boot, hardware discovery, marks as `Ready`) and deploys the reviewed `distro_series`
8. After MR merge (if node definition references a cluster): Ansible `roles/k3s_install` runs; node appears as `Ready` in cluster

### Acceptance Criteria

1. Running `just adopt-server <ip>` with a valid SSH-reachable target produces a GitLab MR with all required content within a reasonable time (target: < 2 minutes from command execution to MR URL printed)
2. Hardware metadata collected: CPU model, core count, RAM, disk layout, all network interface MACs, architecture
3. BMC/IPMI detection: IPMI port 623/UDP is probed; if detected, BMC IP and supported features are included in MR body; if not detected, MR body notes "no BMC detected"
4. IPMI auth failure (wrong credentials) does not block the MR from being opened; it is noted in MR body as "BMC detected but auth failed"; the node definition is still created
5. SSH connectivity failure produces a specific error in the terminal (connection refused vs. auth failure); no MR is created; operator is directed to correct the IP or key
6. A duplicate `infrastructure/nodes/<hostname>.yaml` (node already adopted) produces a warning; no second MR is created for the same hostname; operator is directed to the existing MR or merged node
7. After MR merge: `kubectl get nodes` in the target cluster shows the new node as `Ready` (if cluster was referenced)
8. After MR merge: `maas <profile> machines list` shows the new machine in `Ready` state

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| SSH unreachable | TCP connect or SSH handshake failure within 10s | Emit specific error; print corrected command form; no MR created |
| IPMI auth failure | IPMI auth returns error | Non-blocking; note in MR body; continue |
| GitLab clone fails (token expired, GitLab unreachable) | `git clone` non-zero exit | Emit error; operator must check GitLab reachability and token; retry `just adopt-server <ip>` |
| MAAS commission fails (PXE boot failure, wrong MAC, BIOS settings) | MAAS machine stays in `Failed commissioning` state | Emit error in GitLab CI pipeline; operator inspects MAAS UI; common causes documented in pipeline output; operator retries commissioning from MAAS UI |

### Test Cases

#### Happy Path

- After full bootstrap, run `just adopt-server <qemu-vm-ip>` against a second QEMU VM
- Assert: MR opened at expected URL within 2 minutes
- Assert: MR body contains hardware summary with CPU, RAM, disk, NIC MACs
- Assert: `infrastructure/nodes/<hostname>.yaml` is in the MR branch
- Assert: MR title matches `Draft: adopt: add node <hostname> (<ip>)` pattern
- Merge MR (or simulate pipeline)
- Assert: MAAS machine record created with correct MAC
- Assert: MAAS machine reaches `Ready` state after commissioning

#### Failure Injection

- **SSH unreachable**: run `just adopt-server 192.0.2.1` (non-routable IP); assert error printed to terminal with "connection refused" or timeout; assert no MR is created
- **GitLab unreachable during clone**: stop GitLab between adopt invocation start and clone step; assert git clone failure is caught; assert specific error message; assert no partial MR

#### Edge Cases

- Server with no BMC/IPMI: assert "no BMC detected" is noted in MR body; MR is still created normally
- Server already adopted (same hostname exists in `infrastructure/nodes/`): assert duplicate warning; assert no second MR is created
- ARM64 server: assert `aarch64` arch is correctly detected and written to node definition YAML

---

*End of specs document.*
