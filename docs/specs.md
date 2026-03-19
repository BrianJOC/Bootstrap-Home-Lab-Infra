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
2. Config is split: `config.yaml` holds non-sensitive structural config (domain, IPs, VLAN settings, SSH key path, Vault share counts); `.env` holds secrets (Cloudflare token, admin passwords, tokens). Secrets are loaded into process environment at runtime — never written to logs, TUI plaintext, or `config.yaml`
3. Every stage begins with an idempotency gate: check whether target state exists; skip creation if it does
4. Every `[AUTO]` failure emits to both TUI error panel and log file with absolute path and line reference: `see: <abs-path>/logs/run-<timestamp>/bootstrap.log:<line>`
5. DNS resolution and port reachability are explicit numbered checks, never assumed
6. Every failure mode has an operator decision path: retry, skip, rollback, or abort
7. `vault-init.json` write failure is a hard halt — no recovery path discards unseal keys
8. Every Ansible role and binary selection must branch on detected arch (x86_64 or aarch64)

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
| Internal domain | Operator (TUI prompt) | Yes | e.g. `home.example.com`; used for all DNS and TLS |
| Cloudflare zone ID | Operator (TUI prompt) | Yes | Zone for internal domain |
| Bootstrap server static IP | Operator (TUI prompt) | Yes | Used for DNS records and Vault listener |
| Lab VLAN subnet (CIDR) | Operator (TUI prompt) | Yes | MAAS will manage DHCP on this segment |
| Lab VLAN ID | Operator (TUI prompt) | Yes | VLAN ID configured on Unifi |
| Admin email | Operator (TUI prompt) | Yes | Used for Vault, GitLab, Authentik admin accounts |
| Vault unseal shares count | Operator (TUI prompt, default `5`) | Yes | Minimum `3` |
| Vault unseal threshold | Operator (TUI prompt, default `3`) | Yes | Must be <= shares count |
| Existing `config.yaml` | Filesystem at `./config.yaml` | No | If present, values loaded as defaults; operator reviews before proceeding |

**Secrets (`.env`):**

| Input | Env var | Required | Notes |
|-------|---------|----------|-------|
| Cloudflare API token | `CF_DNS_API_TOKEN` | Yes | Never written to disk or logs |
| Initial admin password | `BOOTSTRAP_ADMIN_PASSWORD` | Yes | Must meet minimum strength; never written to disk |
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

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| `config.yaml` parse error | YAML parse error on load | Operator corrects file or deletes it to trigger fresh collection |
| `.env` missing required var in headless mode | Env var unset at stage start | Hard failure naming the missing var; no prompts shown |
| Input validation failure | Inline TUI validation on field exit | Re-prompt failed field only; retain all other values |
| SSH connection refused | TCP connect to port 22 times out or RST within 10s | Emit specific error; `[PROMPT]` for corrected host/user/key; retry |
| SSH auth failure | SSH handshake succeeds but key rejected | Emit "auth failure"; `[PROMPT]` for corrected key path; retry |
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

Stage 1 deploys Technitium DNS on the target host and configures it as the authoritative resolver for the internal domain. It creates the DNS zone for `home.<domain>` and A records for all bootstrap service hostnames. The target host's resolver is reconfigured to use the local Technitium instance. All subsequent stages depend on internal DNS resolution; without Stage 1, no service hostname resolves and TLS validation cannot proceed. The Ansible role `roles/technitium_dns` performs installation and Docker deployment.

### Inputs

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| `config.yaml` | Stage 0 output | Yes | Must be valid and loaded |
| `config.internal_domain` | `config.yaml` | Yes | e.g. `home.example.com` |
| `config.bootstrap_server_ip` | `config.yaml` | Yes | IP Technitium binds to; used for all A records |
| `BOOTSTRAP_ADMIN_PASSWORD` | `.env` (runtime env) | Yes | Technitium API authentication; never written to disk |
| SSH access to target host | Stage 0 postcondition | Yes | Key-based, no password |
| Target arch | Stage 0 run manifest | Yes | Determines Docker install path if needed |

### Outputs / Postconditions

1. Technitium DNS process is running on `<bootstrap_server_ip>:53` (TCP and UDP) and `<bootstrap_server_ip>:5380` (HTTP API)
2. DNS zone `home.<domain>` exists in Technitium
3. The following A records exist and resolve correctly from the target host:
   - `vault.home.<domain>` → `<bootstrap_server_ip>`
   - `authentik.home.<domain>` → `<bootstrap_server_ip>`
   - `gitlab.home.<domain>` → `<bootstrap_server_ip>`
   - `technitium.home.<domain>` → `<bootstrap_server_ip>`
   - `maas.home.<domain>` → `<bootstrap_server_ip>`
4. Target host `/etc/resolv.conf` (or systemd-resolved config) uses `127.0.0.1` as primary DNS with the original upstream as fallback
5. DNS query from the target host (`dig vault.home.<domain> @127.0.0.1`) returns `<bootstrap_server_ip>`
6. TUI log contains: `[INFO] stage=dns status=complete`
7. Log file contains per-record creation entry: `[INFO] dns-record: created name=<name> ip=<ip>`

### Acceptance Criteria

1. Running Stage 1 on a target with no Technitium instance installs Technitium via `roles/technitium_dns`, starts it, and produces all postconditions
2. Running Stage 1 when Technitium is already running on ports 5380 and 53 skips installation and jumps directly to zone and record verification/creation
3. Running Stage 1 when the DNS zone already exists skips zone creation and proceeds to record upserts
4. Running Stage 1 when all A records already exist produces no changes; all log entries show "already exists" or "skipping"
5. After Stage 1 completes, `dig vault.home.<domain> @<bootstrap_server_ip>` from the operator's machine returns `<bootstrap_server_ip>` (non-blocking if operator's machine cannot reach port 53 — see recovery 8a)
6. After Stage 1 completes, `dig vault.home.<domain> @127.0.0.1` via SSH on the target host returns `<bootstrap_server_ip>`
7. Target host's resolver configuration survives a systemd-resolved restart (or equivalent) without reverting
8. Technitium container log output on failure is written to the bootstrap log file with an explicit path reference in the TUI error panel
9. TUI displays all required observable states: `Installing Technitium DNS...`, `DNS: waiting for Technitium to start (Xs)...`, `DNS: creating zone home.<domain>`, `DNS: creating A records...`, `DNS: verifying resolution...`, `Stage 1: DNS — complete`
10. All A records for downstream service hostnames are present and correct after stage completion; no bootstrap stage beyond Stage 1 may create its first DNS record independently

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Ansible role fails (Docker install, port conflict) | Non-zero Ansible exit code; emitted to log | Emit error + log path to TUI; `[VERIFY]` retry or abort stage |
| Technitium health poll timeout (120s) | No HTTP 200 from `:5380/api/user/login` within timeout | Dump container logs to log file; emit path; `[VERIFY]` retry or abort |
| Technitium API auth failure | HTTP 401 or 403 from API | Emit error identifying credential mismatch; operator corrects `BOOTSTRAP_ADMIN_PASSWORD` in `.env`; retry stage |
| DNS zone creation fails (Technitium API error) | Non-2xx response from Technitium zone API | Emit full API error to log; `[VERIFY]` retry or abort |
| DNS resolution fails from operator's machine (port 53 blocked by firewall) | DNS query from operator machine returns NXDOMAIN or times out | Emit warning (non-blocking); note operator machine may need manual DNS config or split-DNS; target self-resolution is the critical check |
| systemd-resolved config fails (locked config, wrong path) | SSH command returns non-zero exit | Emit specific error; provide manual resolution steps in TUI; `[VERIFY]` retry or abort |
| DNS resolution fails from target (resolver loop) | `dig @127.0.0.1` returns SERVFAIL or times out | Emit error; dump `/etc/resolv.conf` to log; `[VERIFY]` retry resolver config or abort |

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

Stage 2 obtains a publicly trusted wildcard TLS certificate for `*.home.<domain>` using Cloudflare DNS-01 challenge validation. The Cloudflare API token is never written to disk or logs; it is injected as an environment variable at runtime. The issued certificate and private key are stored locally at `./certs/home.<domain>/` for pickup by Stage 3 (Vault), which is not yet running at this point. This stage depends on Stage 1 (internal DNS running) and internet access to the Cloudflare API. The `roles/tls_lego` Ansible role deploys the lego binary for the target arch.

### Inputs

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| `CF_DNS_API_TOKEN` | `.env` (runtime env) | Yes | Never written to disk or logs |
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

### Test Cases

#### Happy Path

- Boot fresh QEMU VM; run Stages 0–1 first to satisfy prerequisites
- Run `just test-stage tls` with valid test Cloudflare credentials
- Assert: `./certs/home.test/fullchain.pem` exists and is parseable
- Assert: `./certs/home.test/privkey.pem` exists
- Assert: `./certs/home.test/meta.json` exists with correct expiry and SANs
- Assert: cert subject is `*.home.test`; SANs include both wildcard and apex
- Assert: log does not contain the Cloudflare token value
- Assert: log contains `[INFO] stage=tls status=complete`

#### Idempotency

- After happy path run, run Stage 2 again without deleting cert files
- Assert: log contains "cert exists and valid; skipping issuance"
- Assert: lego is not invoked a second time
- Assert: output files are unchanged (same content, same mtime or same hash)
- Assert: stage completes successfully with no errors

#### Failure Injection

- **Invalid Cloudflare token**: set `CF_DNS_API_TOKEN` to a garbage value; assert Cloudflare API error is surfaced; assert `[PROMPT]` for corrected token appears; assert token value is not in logs
- **Cloudflare API unreachable**: block outbound port 443 from QEMU VM via firewall rule; assert TCP check fails within 10s; assert `[VERIFY]` prompt appears
- **Cert file corrupt**: write garbage bytes to `./certs/home.test/fullchain.pem` before run; assert corrupt file is detected; assert reissuance is attempted

#### Edge Cases

- Cert expiring in 15 days: assert stage treats it as missing and reissues even though the file exists
- ARM64 QEMU VM: assert lego aarch64 binary is deployed; assert cert issuance works with aarch64 binary
- Domain with multiple labels (e.g. `lab.home.example.com`): assert wildcard covers `*.lab.home.example.com`

---

## Stage 3: Vault

### Summary

Stage 3 installs, initializes, and configures HashiCorp Vault as the central secrets store for the entire platform. Vault runs with Raft integrated storage, is secured with the wildcard TLS cert from Stage 2, and is initialized with Shamir unseal keys. The `vault-init.json` file containing all unseal keys and the root token is written to disk and must be secured by the operator immediately. This file is sacred: failure to write it is a hard halt. After initialization and unsealing, Stage 3 mounts the `kv-v2` secrets engine, writes the wildcard cert to Vault, enables an audit log, writes bootstrap policies, enables the OIDC auth method (pre-configured for Authentik in Stage 4), and generates a 24-hour bootstrap token for use by all subsequent stages. The Ansible role `roles/vault_install` handles binary deployment and systemd unit configuration.

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
4. `./vault-init.json` exists and contains valid unseal keys and root token
5. TUI displayed the `vault-init.json` warning box prominently during init
6. `kv-v2` secrets engine is mounted at `secret/`
7. `pki` secrets engine is mounted at `pki/`
8. Wildcard TLS cert is written to `secret/data/bootstrap/tls/wildcard` with fields `cert`, `key`, `expiry`, `domain`
9. Vault audit log is active at `/var/log/vault/audit.log` on the target host
10. Vault policy `bootstrap` is written and allows reading bootstrap secrets
11. OIDC auth method is enabled at `auth/oidc/` (pending Authentik configuration in Stage 4)
12. Root token is stored at `secret/data/bootstrap/vault/root-token`
13. Bootstrap-scoped token (policy `bootstrap`, TTL `24h`) is held in-memory run context; not written to disk or logs
14. TUI cert permissions on target: `/etc/vault.d/tls/privkey.pem` is `vault:vault 0400`; `/etc/vault.d/tls/fullchain.pem` is `0644`
15. TUI log contains: `[INFO] stage=vault status=complete`

### Acceptance Criteria

1. Running Stage 3 on a target with no Vault installation deploys the correct binary for the target arch, writes Vault config with Raft storage and TLS cert paths, creates the systemd unit, starts the service, and produces all postconditions
2. Running Stage 3 when Vault is already running and initialized skips install and init; jumps to seal check
3. Running Stage 3 when Vault is running, initialized, and unsealed skips install, init, and unseal; verifies secrets engines, policies, and OIDC auth method presence
4. On first initialization, `vault-init.json` is written to `./vault-init.json` immediately upon receiving the init response; if the write fails for any reason, bootstrap halts with a CRITICAL error in the TUI (red panel) and does not proceed
5. The TUI displays a prominent warning box after `vault-init.json` is written: text must include the file path, a warning not to commit to git, and instructions to move to secure offline storage
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
- Assert: `./vault-init.json` exists with `keys` and `root_token` fields; file is valid JSON
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

- **`vault-init.json` write fails**: run Stage 3 on a VM where `./` is read-only or disk is full; assert CRITICAL TUI error in red panel; assert bootstrap halts immediately; assert no further Vault operations are attempted after the write failure
- **Wrong unseal key**: after init, enter an incorrect unseal key; assert HTTP 400 is caught; assert inline error shown; assert progress counter does not regress; assert correct key entry after error continues normally
- **Vault start failure (port conflict)**: bind port 8200 with a dummy process before Stage 3; assert health poll timeout at 60s; assert `journalctl` output is dumped to log file; assert TUI shows log file path; assert `[VERIFY]` prompt appears
- **TLS cert path mismatch**: point Vault config at a non-existent cert path; assert Vault fails to start; assert error is correctly attributed to TLS config

#### Edge Cases

- `vault-init.json` already exists from a previous run when Stage 3 is re-run on an already-initialized Vault: assert file is NOT overwritten; assert existing file is used for root token on idempotency path
- Bootstrap token age check: simulate a run manifest with a start time > 23 hours ago; assert warning is shown that bootstrap token may be near expiry
- ARM64 QEMU VM: assert Vault aarch64 binary is installed; assert service starts correctly

---

## Stage 4: Authentik

### Summary

Stage 4 deploys Authentik as the SSO provider for the entire platform. Authentik uses its bundled PostgreSQL during bootstrap (the shared PostgreSQL instance is not yet available) and migrates to the shared instance at the end of Stage 5. Stage 4 creates the `adults` and `kids` groups, registers OIDC providers for Vault and GitLab, writes OIDC client credentials to Vault, completes the Vault OIDC auth method configuration begun in Stage 3, and creates Authentik application entries for services requiring SSO. The Ansible role `roles/authentik_install` handles Docker Compose deployment. This stage has a hard prerequisite on Vault being unsealed; it checks seal state at the start and halts if sealed.

### Inputs

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| `config.yaml` | Stage 0 output | Yes | |
| `config.bootstrap_server_ip` | `config.yaml` | Yes | Authentik Docker Compose binds here |
| Bootstrap token | Stage 3 run context (in-memory) | Yes | Used for all Vault reads/writes in this stage |
| Vault unsealed | Stage 3 postcondition | Yes | Checked at stage start; halt if sealed |
| `kv-v2` at `secret/` | Stage 3 postcondition | Yes | For reading/writing credentials |
| DNS resolution of `authentik.home.<domain>` | Stage 1 postcondition | Yes | Must resolve before health check |
| Wildcard TLS cert | Stage 2 output, also at `secret/data/bootstrap/tls/wildcard` | Yes | Used for Authentik HTTPS |
| Docker available on target | Stage 1 postcondition (Docker installed by `roles/technitium_dns`) | Yes | Docker Compose required |
| Ports 9000 (HTTP) and 9443 (HTTPS) available on target | Network prerequisite | Yes | |

### Outputs / Postconditions

1. Authentik is running at `https://authentik.home.<domain>`; `GET /api/v3/root/config/` returns HTTP 200
2. Authentik admin account exists with password stored at `secret/data/bootstrap/authentik/admin`
3. Authentik group `adults` exists
4. Authentik group `kids` exists
5. Authentik OIDC provider `vault` exists with correct redirect URI: `https://vault.home.<domain>/ui/vault/auth/oidc/oidc/callback`
6. Authentik OIDC provider `gitlab` exists with correct redirect URI: `https://gitlab.home.<domain>/users/auth/openid_connect/callback`
7. OIDC client credentials for `vault` are in Vault at `secret/data/bootstrap/authentik/oidc/vault` with fields `client_id` and `client_secret`
8. OIDC client credentials for `gitlab` are in Vault at `secret/data/bootstrap/authentik/oidc/gitlab` with fields `client_id` and `client_secret`
9. Vault OIDC auth method at `auth/oidc/` is fully configured with Authentik as the provider: `oidc_discovery_url`, `client_id`, `client_secret`, `oidc_scopes` are set
10. Authentik application `vault` pointing to the Vault OIDC provider exists
11. Authentik application `gitlab` pointing to the GitLab OIDC provider exists
12. OIDC discovery document for Vault is fetchable: `GET https://authentik.home.<domain>/application/o/vault/.well-known/openid-configuration` returns valid issuer and authorization endpoint
13. TUI log contains: `[INFO] stage=authentik status=complete`

### Acceptance Criteria

1. Running Stage 4 on a target with no Authentik instance deploys via `roles/authentik_install` (Docker Compose), waits up to 300s for health, and produces all postconditions
2. Running Stage 4 when Authentik is already running skips installation and jumps to group/OIDC/application idempotency checks
3. Authentik admin password is generated if not present in Vault at `secret/data/bootstrap/authentik/admin`; if present, the existing value is used without regeneration
4. Groups `adults` and `kids` are created once; running Stage 4 a second time does not create duplicate groups
5. OIDC providers `vault` and `gitlab` are created once; if they already exist by name, their existing `client_id` is read and Vault is updated only if the stored value differs
6. OIDC client credentials for both `vault` and `gitlab` providers are present in Vault immediately after the stage completes; `client_secret` values are not present in log files or TUI output
7. Vault OIDC auth method is fully configured: a `vault list auth/oidc/config` against the running Vault returns a non-empty configuration with the Authentik discovery URL
8. Authentik startup health poll logs progress every 30 seconds: `[INFO] authentik-wait: elapsed=Xs`
9. If Vault is sealed at stage start, Stage 4 halts immediately with a TUI error: `Vault is sealed — unseal Vault before continuing Stage 4`; no Authentik operations are attempted
10. TUI displays all required observable states: `Authentik: verifying Vault is unsealed...`, `Authentik: deploying...`, `Authentik: waiting for startup (Xs)...`, `Authentik: creating groups...`, `Authentik: registering OIDC clients...`, `Authentik: storing credentials in Vault...`, `Stage 4: Authentik — complete`

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Vault sealed at stage start | `GET /v1/sys/seal-status` returns `{"sealed": true}` | Halt; TUI error: "Vault is sealed"; instruct operator to unseal via `vault operator unseal` using `vault-init.json` keys; retry `bootstrap --stage authentik` |
| Docker Compose deploy fails (resource limits, image pull failure) | `docker compose up` non-zero exit | Emit full Docker Compose output to log file; emit path to TUI; `[VERIFY]` retry or abort |
| Authentik health timeout (300s) | No HTTP 200 from health endpoint within timeout | Dump Authentik container logs to log file; emit path; `[VERIFY]` retry or abort |
| OIDC provider creation fails (API error, name conflict) | Authentik API returns non-2xx | Emit Authentik API error to log; `[VERIFY]` retry or abort |
| Vault write fails (bootstrap token expired after 24h TTL) | Vault API returns 403 | Emit token expiry warning; instruct operator to run `bootstrap --stage vault` to refresh token; then retry `bootstrap --stage authentik` |
| Vault OIDC config fails (Authentik not fully initialized) | Vault API returns error writing OIDC config | Emit error; `[VERIFY]` retry (Authentik may need more startup time) or abort |

### Test Cases

#### Happy Path

- Boot fresh QEMU VM; run Stages 0–3
- Run `just test-stage authentik`
- Assert: `https://authentik.home.test/api/v3/root/config/` returns HTTP 200
- Assert: `adults` and `kids` groups exist via Authentik API
- Assert: OIDC providers `vault` and `gitlab` exist via Authentik API
- Assert: `secret/data/bootstrap/authentik/oidc/vault` readable from Vault with `client_id` and `client_secret` fields
- Assert: `secret/data/bootstrap/authentik/oidc/gitlab` readable from Vault with `client_id` and `client_secret` fields
- Assert: Vault OIDC auth method has `oidc_discovery_url` configured
- Assert: OIDC discovery doc fetchable: `GET .../application/o/vault/.well-known/openid-configuration` returns 200
- Assert: log contains `[INFO] stage=authentik status=complete`

#### Idempotency

- After happy path run, run Stage 4 a second time without resetting VM
- Assert: Ansible role `roles/authentik_install` reports no changes (`changed=0`)
- Assert: groups `adults` and `kids` creation steps log "already exists; skipping"
- Assert: OIDC provider creation steps log "already exists; skipping"
- Assert: Vault writes for OIDC credentials are skipped or produce no net change (value unchanged)
- Assert: stage completes without error

#### Failure Injection

- **Vault sealed**: seal Vault before Stage 4; assert stage halts at step 1; assert TUI error message is present; assert no Docker Compose operations are attempted
- **Authentik health timeout**: deploy Authentik with a misconfigured database so it never becomes healthy; assert timeout at 300s; assert container logs are dumped; assert TUI shows log path; assert `[VERIFY]` prompt appears
- **Bootstrap token expired**: run Stage 4 with a manually expired bootstrap token; assert HTTP 403 from Vault is caught; assert token expiry warning is shown; assert operator is directed to refresh via `bootstrap --stage vault`

#### Edge Cases

- OIDC provider already exists with a different redirect URI: assert stage detects the mismatch and updates the provider to the correct URI
- Authentik startup takes > 240s on a slow VM: assert health poll continues up to the 300s limit without spurious failure

---

## Stage 5: PostgreSQL

### Summary

Stage 5 deploys PostgreSQL as the shared database backing service for GitLab, Authentik, and MAAS. It generates per-service database credentials, stores them in Vault, creates the databases and users, verifies connectivity, and migrates Authentik from its bundled PostgreSQL to the shared instance. The migration is a stop-export-import-restart sequence; the original bundled database is preserved until migration is confirmed. The Ansible role `roles/postgresql_install` handles installation and base configuration. This stage has a hard prerequisite on Vault being unsealed.

### Inputs

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| `config.yaml` | Stage 0 output | Yes | |
| `config.bootstrap_server_ip` | `config.yaml` | Yes | PostgreSQL binds to this host; `pg_hba.conf` allows connections from here |
| Bootstrap token | Stage 3 run context (in-memory) | Yes | All Vault reads/writes |
| Vault unsealed | Stage 3 postcondition | Yes | Checked at stage start; halt if sealed |
| Authentik running | Stage 4 postcondition | Yes | Will be migrated to shared PostgreSQL at end of this stage |
| Target arch | Stage 0 run manifest | Yes | PostgreSQL package selection |
| Port 5432 available on target | Network prerequisite | Yes | |
| Target disk space >= 20 GB available | Target prerequisite | Yes | |

### Outputs / Postconditions

1. PostgreSQL 16 (or latest stable) is running on `<target>:5432`
2. PostgreSQL admin credentials are in Vault at `secret/data/bootstrap/postgres/admin`
3. Database `authentik` exists with user `authentik`; credentials at `secret/data/bootstrap/postgres/authentik`
4. Database `gitlab` exists with user `gitlab`; credentials at `secret/data/bootstrap/postgres/gitlab`
5. Database `maas` exists with user `maas`; credentials at `secret/data/bootstrap/postgres/maas`
6. All three service users can connect to their respective databases from the target host
7. Authentik is running at `https://authentik.home.<domain>` and healthy after migration to shared PostgreSQL
8. Authentik config points to shared PostgreSQL DSN (not the bundled instance)
9. TUI log contains: `[INFO] stage=postgres status=complete`
10. Log file contains: `[INFO] authentik-migration: migrated to shared postgres` and `[INFO] authentik-migration: health check passed`

### Acceptance Criteria

1. Running Stage 5 on a target with no PostgreSQL installs PostgreSQL via `roles/postgresql_install`, configures `listen_addresses = '*'` and `pg_hba.conf` with the bootstrap server IP, and produces all postconditions
2. Running Stage 5 when PostgreSQL is already running skips installation and jumps to database/user idempotency checks
3. PostgreSQL admin password is generated if not present in Vault at `secret/data/bootstrap/postgres/admin`; existing value is used if present
4. Each per-service database and user is created once; running Stage 5 a second time detects existing objects and skips creation without error
5. Per-service credentials (passwords) are written to Vault and not present in any log file, TUI output, or config file
6. Connectivity verification (`psql` check) passes for all three service users against their respective databases
7. Authentik migration: Authentik is stopped, data is exported from the bundled PostgreSQL, imported into the shared instance, and Authentik is restarted with the new DSN; health check passes after restart (up to 300s wait)
8. If Authentik health check fails after migration, the TUI presents a `[VERIFY]` prompt offering rollback to the bundled DSN or retry; rollback must restore Authentik to a healthy state using the original bundled database
9. If Authentik is already using the shared PostgreSQL (DSN in Authentik config matches shared instance), the migration step is skipped entirely
10. TUI displays all required observable states: `PostgreSQL: checking for existing instance...`, `PostgreSQL: installing...`, `PostgreSQL: creating service databases...`, `PostgreSQL: migrating Authentik to shared DB...`, `PostgreSQL: verifying connectivity...`, `Stage 5: PostgreSQL — complete`

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Vault sealed at stage start | `GET /v1/sys/seal-status` returns sealed | Halt; TUI error; instruct operator to unseal; retry stage |
| PostgreSQL install fails (package repo unreachable, disk space) | Non-zero Ansible exit code | Emit error + log path; `[VERIFY]` retry or abort |
| PostgreSQL service fails to start (config error) | systemd service non-active after start attempt | Dump `journalctl -u postgresql` to log; `[VERIFY]` retry or abort |
| User/DB creation fails (permission error, conflicting objects) | `psql` command non-zero exit | Emit specific psql error; `[VERIFY]` retry or abort |
| Authentik migration fails mid-import (pg_dump error, import conflict) | pg_dump or psql import non-zero exit | Stop: Authentik is stopped; original bundled DB still intact; revert Authentik config to bundled DSN; restart Authentik; resolve conflict; retry `bootstrap --stage postgres` |
| Authentik unhealthy after migration (data corruption) | Health check returns non-200 within 300s | `[VERIFY]` prompt: rollback to bundled DSN (restart Authentik with original config) or retry migration |

### Test Cases

#### Happy Path

- Boot fresh QEMU VM; run Stages 0–4
- Run `just test-stage postgres`
- Assert: TCP port 5432 open on QEMU VM
- Assert: `psql -U authentik -d authentik -c '\q'` succeeds
- Assert: `psql -U gitlab -d gitlab -c '\q'` succeeds
- Assert: `psql -U maas -d maas -c '\q'` succeeds
- Assert: all credentials readable from Vault at `secret/data/bootstrap/postgres/*`
- Assert: `GET https://authentik.home.test/api/v3/root/config/` returns 200 after migration
- Assert: Authentik config DSN points to shared PostgreSQL (not bundled)
- Assert: log contains `[INFO] stage=postgres status=complete`

#### Idempotency

- After happy path run, run Stage 5 a second time without resetting VM
- Assert: PostgreSQL install step is skipped
- Assert: all database/user creation steps log "already exists; skipping"
- Assert: Authentik migration step is skipped (DSN already points to shared PostgreSQL)
- Assert: stage completes without error

#### Failure Injection

- **Migration fails mid-import**: corrupt the pg_dump output to simulate an import error; assert migration halts; assert Authentik is reverted to bundled DSN; assert Authentik becomes healthy again after rollback; assert `[VERIFY]` prompt appeared
- **Authentik unhealthy after migration**: simulate a migration that completes but leaves Authentik unhealthy (misconfigured DSN); assert health check timeout; assert `[VERIFY]` rollback prompt; assert rollback restores health
- **Vault sealed**: seal Vault before Stage 5; assert stage halts at seal check; assert no PostgreSQL operations are attempted

#### Edge Cases

- PostgreSQL on a separate host (configured in `config.yaml` to point to different IP): assert Ansible role targets the correct host; connection checks use the external IP
- Authentik migration on a VM with < 500 MB free disk: assert stage detects insufficient disk before attempting pg_dump

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

Stage 7 deploys MAAS (Metal as a Service) as the bare metal provisioning system for the lab VLAN. MAAS is configured with the shared PostgreSQL instance, given the lab VLAN fabric and subnet, and made the DHCP authority for that segment. Ubuntu OS images (22.04 and 24.04, amd64 and arm64) are imported asynchronously. A MAAS Terraform API key is generated and stored in Vault. After Stage 7, new physical servers can be PXE-booted on the lab VLAN and will appear in MAAS for commissioning. The Ansible role `roles/maas_install` handles MAAS snap/deb installation. OS image import is a background operation and does not block stage completion.

### Inputs

| Input | Source | Required | Notes |
|-------|--------|----------|-------|
| `config.yaml` | Stage 0 output | Yes | |
| `config.bootstrap_server_ip` | `config.yaml` | Yes | MAAS installs here |
| `config.lab_vlan_subnet` | `config.yaml` | Yes | CIDR for lab segment (e.g. `10.10.10.0/24`) |
| `config.lab_vlan_id` | `config.yaml` | Yes | VLAN ID configured on Unifi |
| Bootstrap token | Stage 3 run context (in-memory) | Yes | All Vault reads/writes |
| Vault unsealed | Stage 3 postcondition | Yes | Checked at stage start; halt if sealed |
| PostgreSQL DSN for `maas` db | `secret/data/bootstrap/postgres/maas` in Vault | Yes | Injected as env var; not logged |
| DNS resolution of `maas.home.<domain>` | Stage 1 postcondition | Yes | |
| Technitium DNS IP | Stage 1 postcondition | Yes | Set as MAAS upstream DNS |
| Lab VLAN trunked to bootstrap server network interface | Physical/Unifi prerequisite | Yes | Operator must configure Unifi before this stage |
| Ports 5240 (HTTP) and 5241 (HTTPS) available on target | Network prerequisite | Yes | |
| Target arch | Stage 0 run manifest | Yes | MAAS snap/deb variant |

### Outputs / Postconditions

1. MAAS is running at `http://maas.home.<domain>:5240/MAAS`; `GET /MAAS/api/2.0/` returns HTTP 200
2. MAAS admin credentials are in Vault at `secret/data/bootstrap/maas/admin`
3. MAAS upstream DNS is configured to use the Technitium DNS IP
4. MAAS domain is set to `maas.home.<domain>`
5. Lab VLAN fabric and subnet (`config.lab_vlan_subnet`) are defined in MAAS
6. DHCP is enabled on the lab VLAN subnet with MAAS as the DHCP server
7. Ubuntu 22.04 and 24.04 image imports are initiated (background); import status is logged
8. MAAS Terraform API key is in Vault at `secret/data/bootstrap/maas/terraform-api-key`
9. TUI log contains: `[INFO] stage=maas status=complete`
10. Log file contains: `[INFO] maas-network: lab vlan configured; dhcp enabled` and `[INFO] maas-terraform: api key written to vault`

### Acceptance Criteria

1. Running Stage 7 on a target with no MAAS installs via `roles/maas_install`, runs `maas init region+rack`, waits up to 300s for the API, and produces all postconditions
2. Running Stage 7 when MAAS is already running skips installation and jumps to network config idempotency
3. PostgreSQL DSN is injected via environment variable into the `maas init` command; DSN is not present in any log file or TUI output
4. Lab VLAN DHCP: if DHCP is already enabled on the MAAS-managed subnet, the DHCP configuration step is skipped without error
5. OS image import is initiated as a background operation; stage completion does not wait for images to finish downloading; TUI shows "MAAS: importing OS images (background — may take several minutes)..." and moves on
6. MAAS Terraform API key is stored in Vault and not present in any log file or TUI output
7. `maas.home.<domain>` A record is verified (may already exist from Stage 1); no duplicate records are created
8. Upstream DNS in MAAS is set to Technitium's IP; MAAS does not use a public resolver directly
9. TUI displays all required observable states: `MAAS: checking for existing instance...`, `MAAS: installing...`, `MAAS: configuring network and DHCP...`, `MAAS: importing OS images (background)...`, `Stage 7: MAAS — complete`

### Failure Modes

| Failure | Detection | Recovery |
|---------|-----------|----------|
| Vault sealed at stage start | `GET /v1/sys/seal-status` returns sealed | Halt; TUI error; instruct operator to unseal; retry stage |
| MAAS snap install fails (network, snap store unreachable) | Non-zero snap install exit code | Emit error + log path; `[VERIFY]` retry or abort |
| MAAS database init fails (wrong DSN, PostgreSQL not accepting connections) | `maas init` non-zero exit | Check PostgreSQL connectivity from target; verify `maas` user permissions; emit instructions; retry `bootstrap --stage maas` |
| MAAS health timeout (300s) | No HTTP 200 from `/MAAS/api/2.0/` within timeout | Emit timeout error; `[VERIFY]` retry or abort |
| DHCP config fails (VLAN not present, subnet conflict) | MAAS API returns error on DHCP enable | Emit MAAS API error; `[VERIFY]` prompt noting operator must verify Unifi VLAN config and lab VLAN trunk; retry |
| OS image import fails (no internet, wrong arch) | MAAS image import status API returns error | Non-blocking warning emitted to TUI; images can be re-imported post-bootstrap; stage continues |

### Test Cases

#### Happy Path

- Boot fresh QEMU VM with lab VLAN configured; run Stages 0–6
- Run `just test-stage maas`
- Assert: `GET http://maas.home.test:5240/MAAS/api/2.0/` returns HTTP 200
- Assert: lab VLAN subnet visible in MAAS fabric/subnet API
- Assert: DHCP enabled on lab VLAN subnet via MAAS API
- Assert: `secret/data/bootstrap/maas/terraform-api-key` readable from Vault
- Assert: MAAS upstream DNS is Technitium IP
- Assert: log contains `[INFO] stage=maas status=complete`

#### Idempotency

- After happy path run, run Stage 7 a second time without resetting VM
- Assert: MAAS install step is skipped (log: "already running")
- Assert: VLAN/subnet config step is skipped (already exists)
- Assert: DHCP config step is skipped (already enabled)
- Assert: image import step notes images already imported or import already in progress
- Assert: stage completes without error

#### Failure Injection

- **Database init fails**: configure `maas` Vault credentials with wrong password before Stage 7; assert `maas init` failure is caught; assert error message surfaces database connectivity issue; assert `[VERIFY]` prompt appears
- **DHCP config fails**: configure MAAS to point at a non-existent VLAN ID; assert MAAS API error is caught; assert `[VERIFY]` prompt with Unifi VLAN instructions appears
- **Image import non-blocking**: block outbound HTTP on QEMU VM after MAAS starts; assert image import failure emits warning (not error); assert stage completes despite image import failure

#### Edge Cases

- ARM64 QEMU VM: assert MAAS ARM64 snap/deb is installed; assert API starts correctly on ARM
- Lab VLAN subnet conflicts with an existing MAAS subnet: assert MAAS API error is surfaced clearly; stage halts on DHCP step with `[VERIFY]` prompt

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

## Cross-Cutting Flow: Server Adoption (`just adopt-server`)

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
2. MR title: `adopt: add node <hostname> (<ip>)`
3. MR body contains: hardware summary table (CPU, RAM, disk, NICs, MACs, arch), BMC/IPMI status (detected or not detected), suggested cluster assignment (if any)
4. `infrastructure/nodes/<hostname>.yaml` is committed to the MR branch with collected metadata and provisioner type
5. MR URL is printed to the operator's terminal
6. After MR merge: MAAS machine record is created with MAC address, arch, and BMC details
7. After MR merge: MAAS commissions machine (PXE boot, hardware discovery, marks as `Ready`)
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
- Assert: MR title matches `adopt: add node <hostname> (<ip>)` pattern
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
