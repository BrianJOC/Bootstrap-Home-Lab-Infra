# Bootstrap Workflow Map

> Produced by: workflow-architect
> Source of truth: `docs/prd.md`
> Last updated: 2026-03-19
> Status: Ready for spec-writer

---

## How to Read This Document

Each stage is self-contained. Steps are marked:

- `[AUTO]` — runs without user input; emits a status line to TUI and log file
- `[PROMPT]` — pauses for user input via TUI
- `[VERIFY]` — pauses for user confirmation before continuing

Every `[AUTO]` failure emits to both TUI error panel and the log file with a
full path reference (e.g. `see: logs/bootstrap-2026-03-19T14:00:00.log:142`).

Network checks (DNS resolution, port reachability) are always explicit numbered
steps, never assumed. Vault seal state is always checked before any
Vault-dependent step.

---

## Stage 0: Config Collection

### Prerequisites

- Bootstrap binary is present and executable on the operator's machine
- Operator has network access to the target server (SSH reachable on port 22)
- Operator has a Cloudflare account with API token capability
- Operator knows the target server's IP or hostname

### Happy Path

1. `[AUTO]` Bootstrap binary starts; TUI initializes; multi-pane layout renders
   (progress pane, live log pane, error panel)
   - TUI emits: `Bootstrap v<version> starting`
   - Log: `[INFO] bootstrap started; target=<ip>; pid=<pid>`

2. `[AUTO]` Check for existing `config.yaml` at `./config.yaml`
   - If found: load values as defaults; skip to step 4 for review
   - If not found: begin interactive collection (step 3)

3. `[PROMPT]` Collect configuration interactively in the TUI:
   - **Target host** — IP or hostname of the Ubuntu server to bootstrap
   - **SSH user** — default `ubuntu`
   - **SSH key path** — default `~/.ssh/id_ed25519`
   - **Internal domain** — e.g. `home.example.com`
   - **Cloudflare API token** — for DNS-01 challenge (masked input)
   - **Cloudflare zone ID** — for the domain above
   - **Bootstrap server static IP** — used for DNS and Vault listener binding
   - **Lab VLAN subnet and gateway** — routing boundary for the lab segment
   - **MAAS DHCP** — defaults off; if enabled, also collect VLAN ID, target
     interface, and dynamic range for the dedicated lab network
   - **Admin email** — used for Vault, GitLab, Authentik admin accounts
   - **Initial admin password** — master password for all bootstrap admin accounts
     (masked input; must meet minimum strength requirements)
   - **Vault unseal shares count** — default `5`; minimum `3`
   - **Vault unseal threshold** — default `3`
   - **Backups** — recommend TrueNAS/Restic; present daily 02:30,
     `7 daily / 5 weekly / 12 monthly`, weekly/monthly integrity checks, and
     quarterly restore reminder; allow edit or disable
   - **Optional USB repository** — disabled by default; if enabled, collect its
     path, masked password, and manual/weekly/monthly copy cadence
   - **Monitoring** — target-local Kuma + Beszel, seven-day incidents/logs,
     built-in metrics aggregation, and configurable resource budget
   - **Paging** — recommend Discord, Telegram, ntfy, or webhook; show default
     debounce/threshold/reminder policy, test the destination, or select
     dashboard-only mode

4. `[VERIFY]` Display collected config summary (credentials masked); prompt:
   `Proceed with these values? [y/N]`

5. `[AUTO]` Write `config.yaml` to working directory (credentials stored as
   references, not plaintext — Cloudflare token and admin password stored in
   local OS keychain or as environment variable references)
   - Log: `[INFO] config written; path=./config.yaml`

6. `[VERIFY]` Establish and validate SSH host identity:
   - Connect only far enough to obtain the host public key; timeout 10s
   - If no pin exists, display target, resolved address, algorithm, and SHA-256
     fingerprint; accept and pin it, or abort
   - If `ssh_host_fingerprint` is configured, require an exact match
   - If the key differs from the pin, stop before authentication; display the
     old and new fingerprints and ask whether the server was intentionally
     reinstalled/replaced
   - Replacement requires typing the target, atomically updates only that
     entry, and reconnects to verify the same new key
   - Headless replacement requires `--replace-host-key` plus the configured
     expected new fingerprint; it never prompts or silently replaces
   - Reconnect using strict verification, then authenticate with the provided
     key
   - On success: log `[INFO] SSH reachable; host=<ip>; user=<user>`
   - On failure: emit to TUI error panel and log; go to Recovery 6a

7. `[AUTO]` Detect target architecture (x86_64 or aarch64) via SSH command
   `uname -m`
   - Log: `[INFO] target arch=<arch>`

8. `[AUTO]` Detect target OS and version via SSH command
   `lsb_release -rs` and `lsb_release -is`
   - Verify Ubuntu >= 22.04; on mismatch emit warning and prompt to continue

9. `[AUTO]` Write run manifest: `./logs/run-<timestamp>/manifest.json` with
   target, arch, config hash, and start time
   - Log: `[INFO] run manifest written; path=./logs/run-<timestamp>/manifest.json`

### Branch Conditions

- Existing `config.yaml` found → load defaults, skip interactive collection,
  jump to step 4 for review
- `--stage <name>` flag present → skip config collection after load, jump to
  named stage directly (requires valid `config.yaml`)
- `--headless` flag present → skip TUI; read `config.yaml` only; no prompts;
  fail hard on any missing required value

### Failure Modes

| Step | Failure | Trigger |
|------|---------|---------|
| 2 | `config.yaml` parse error | Malformed YAML |
| 3 | Input validation failure | Invalid IP, weak password, malformed token format |
| 6 | SSH connection refused | Wrong IP, firewall, SSH daemon not running |
| 6 | SSH auth failure | Wrong key, wrong user |
| 6 | SSH host key unknown | First connection has not been approved or pre-pinned |
| 6 | SSH host key changed | Server was reinstalled/replaced or may be impersonated |
| 7 | `uname` command fails | Minimal OS image without standard utils |
| 8 | OS version check fails | Non-Ubuntu or Ubuntu < 22.04 |

### Recovery Paths

- **6a** SSH failure: display error with specific reason; allow operator to
  correct host/user/key values and retry; `[PROMPT]` for corrected values;
  re-run step 6
- **6b** Changed host key: display a red identity warning with old and new
  fingerprints; operator may cancel or confirm an intentional replacement by
  typing the target. Cancellation preserves the old pin
- **3a** Input validation failure: inline TUI validation; re-prompt the
  specific field that failed; do not clear other fields
- **8a** OS mismatch: `[VERIFY]` warning prompt; operator can choose to
  continue at own risk or abort

### Handoff Contract

Produces:
- `config.yaml` — validated configuration; referenced by all subsequent stages
- `logs/run-<timestamp>/manifest.json` — run metadata
- Confirmed SSH access to target host

### Observable States

| State | TUI | Log |
|-------|-----|-----|
| Starting | `Stage 0: Config collection — starting` | `[INFO] stage=config-collection status=start` |
| Collecting | Input fields visible in TUI form | `[INFO] prompting for config` |
| SSH check | `Verifying SSH connectivity...` | `[INFO] ssh-check host=<ip>` |
| First-use trust | Fingerprint verification panel | `[INFO] ssh-host-key action=pin fingerprint=<sha256>` |
| Key changed | Red old/new fingerprint panel | `[WARN] ssh-host-key action=changed old=<sha256> new=<sha256>` |
| SSH OK | `SSH connection verified` | `[INFO] ssh-check status=ok` |
| Complete | `Stage 0: Config collection — complete` | `[INFO] stage=config-collection status=complete` |
| Error | Red error panel; `see: <log path>:<line>` | `[ERROR] stage=config-collection error=<msg>` |

---

## Stage 1: DNS (Technitium)

### Prerequisites

- Stage 0 complete; `config.yaml` valid and loaded
- SSH access to target host confirmed
- Target host has internet access (for package download)
- `config.internal_domain` is set

### Happy Path

0. `[AUTO]` Run the network preflight:
   - Confirm `bootstrap_server_ip` belongs to the expected target interface
   - Probe TCP and UDP 53 plus TCP 5380 and identify any owning process
   - Capture the target's current resolver configuration and upstream servers in
     the run manifest for rollback
   - Verify at least one upstream resolver is reachable

1. `[AUTO]` Check if Technitium DNS is already running on target:
   - TCP port check: `<target>:5380` (Technitium web UI) and `<target>:53` (DNS)
   - If both reachable: log `[INFO] technitium already running; skipping install`
     and jump to step 7 (idempotency check)
   - If not reachable: continue to step 2

2. `[AUTO]` Transfer Ansible role to target or invoke via control machine;
   log `[INFO] dns-install: uploading ansible role`

3. `[AUTO]` Run Ansible role `technitium_dns`:
   - Install Docker if not present (or system package per arch)
   - Deploy Technitium DNS container
   - Configure Technitium: bind to `<bootstrap_server_ip>:53` and `:5380`
   - Log: `[INFO] dns-install: ansible role started`
   - TUI: `Installing Technitium DNS...`

4. `[AUTO]` Wait for Technitium HTTP API to become healthy:
   - Poll `http://<target>:5380/api/user/login` every 5s; timeout 120s
   - On timeout: emit error to TUI and log; go to Recovery 4a

5. `[AUTO]` Authenticate to Technitium API; obtain session token
   - Credentials: from `config.yaml` admin credentials
   - Log: `[INFO] dns-auth: session token obtained`

6. `[AUTO][TERRAFORM bootstrap-dns]` Create the initial DNS zone for
   `config.internal_domain`
   - Display the saved Terraform plan and require interactive approval
   - Headless mutation requires `--approve-terraform`
   - Check if zone exists first (idempotency)
   - If zone exists: log `[INFO] dns-zone: already exists; skipping`
   - If not: create zone; log `[INFO] dns-zone: created domain=<domain>`

7. `[AUTO][TERRAFORM bootstrap-dns]` Create A records for bootstrap services:
   - `vault.home.<domain>` → `<bootstrap_server_ip>`
   - `authentik.home.<domain>` → `<bootstrap_server_ip>`
   - `gitlab.home.<domain>` → `<bootstrap_server_ip>`
   - `technitium.home.<domain>` → `<bootstrap_server_ip>`
   - `maas.home.<domain>` → `<bootstrap_server_ip>`
   - Check each record before creating (idempotency)
   - Log per record: `[INFO] dns-record: created name=<name> ip=<ip>`

8. `[AUTO]` Verify DNS resolution works from the operator's machine:
   - Run DNS query: `<bootstrap_server_ip>:53` for `vault.home.<domain>`
   - Expect: resolves to `<bootstrap_server_ip>`
   - On failure: go to Recovery 8a

9. `[AUTO]` Atomically configure the target host resolver to use the local
   Technitium instance
   - Log: `[INFO] dns-resolver: target host resolver configured`

10. `[AUTO]` Verify DNS resolution from target host itself:
    - Resolve both `vault.home.<domain>` and an external control name
    - On either failure, restore the resolver snapshot from step 0
    - Log: `[INFO] dns-verify: target host resolution confirmed`

11. `[AUTO]` Record that router, DHCP, and non-lab DNS settings were not
    changed. Whole-home rollout is a separate post-bootstrap UniFi procedure.

### Branch Conditions

- Technitium already running on port 5380 → skip install; jump to zone/record
  creation (idempotency path)
- Docker already installed on target → skip Docker install step within Ansible role
- DNS zone already exists → skip zone creation; proceed to record upserts
- An unrelated process owns TCP/UDP 53 or TCP 5380 → stop before installation;
  do not mistake an open port for a healthy managed Technitium instance

### Failure Modes

| Step | Failure | Trigger |
|------|---------|---------|
| 3 | Ansible role fails | Docker install failure; port conflict |
| 4 | Health poll timeout | Container crash; misconfigured bind address |
| 5 | Auth failure | Wrong admin credentials in config |
| 6 | Zone creation fails | Technitium API error |
| 8 | DNS resolution fails from operator machine | Firewall blocking port 53 |
| 9 | Resolver config fails | systemd-resolved locked; wrong file path |
| 10 | DNS resolution fails from target | Loop in resolver config |

### Recovery Paths

- **4a** Health timeout: dump Technitium container logs to log file; emit
  path to TUI; `[VERIFY]` prompt: retry or abort stage
- **8a** DNS resolution failure from operator machine: emit warning; note that
  operator machine may need manual DNS configuration or VPN split-DNS; this
  does not block the bootstrap (target self-resolution is the critical check)
- **9a/10a** Resolver change or verification failure: restore the exact resolver
  snapshot from step 0, verify external DNS through the old path, then offer retry
- **General retry**: `bootstrap --stage dns` re-runs entire stage idempotently

### Handoff Contract

Produces:
- Technitium DNS running on `<bootstrap_server_ip>:53` and `:5380`
- Zone `home.<domain>` with A records for all bootstrap service hostnames
- Target host `/etc/resolv.conf` points to local Technitium instance
- Prior resolver details and verification evidence written to run manifest
- Existing UniFi, DHCP, and non-lab client settings unchanged

### Observable States

| State | TUI | Log |
|-------|-----|-----|
| Already running | `DNS: already running — verifying records` | `[INFO] stage=dns status=already-running` |
| Installing | `DNS: installing Technitium...` | `[INFO] stage=dns status=installing` |
| Waiting for health | `DNS: waiting for Technitium to start (Xs)...` | `[INFO] stage=dns health-poll attempt=N` |
| Creating zone | `DNS: creating zone home.<domain>` | `[INFO] stage=dns action=create-zone` |
| Creating records | `DNS: creating A records...` | `[INFO] stage=dns action=create-record name=<name>` |
| Verifying | `DNS: verifying resolution...` | `[INFO] stage=dns action=verify-resolution` |
| Complete | `Stage 1: DNS — complete` | `[INFO] stage=dns status=complete` |
| Error | Red error panel; `see: <log path>:<line>` | `[ERROR] stage=dns error=<msg>` |

---

## Stage 2: TLS (Cloudflare DNS-01)

### Prerequisites

- Stage 1 complete; Technitium DNS running and resolving `*.home.<domain>`
- `config.cloudflare_api_token` and `config.cloudflare_zone_id` are set
- Vault is NOT yet running (TLS stage precedes Vault stage by design — wildcard
  cert is obtained first, then stored in Vault when Vault comes up)
- Target host has internet access (Cloudflare API must be reachable)

### Happy Path

1. `[AUTO]` Network check: verify Cloudflare API is reachable from target host
   - TCP check to `api.cloudflare.com:443`; timeout 10s
   - Log: `[INFO] tls-preflight: cloudflare api reachable`

2. `[AUTO]` Check if a valid wildcard cert already exists at
   `./certs/home.<domain>/` with expiry > 30 days:
   - If valid cert found: log `[INFO] tls: cert exists and valid; skipping issuance`
     and jump to step 8 (Vault storage — handled in Vault stage); mark stage
     complete
   - If expired or missing: continue

3. `[AUTO]` Deploy cert issuance tooling to target via Ansible role `tls_certbot`
   or `tls_lego` (lego preferred — single binary, DNS-01 native):
   - Transfer binary for target arch
   - Log: `[INFO] tls-install: lego binary deployed; arch=<arch>`

4. `[AUTO]` Run DNS-01 challenge via lego:
   - Command (injected at runtime, never logged in plaintext):
     `lego --email=<admin_email> --dns=cloudflare --domains=*.home.<domain>
     --domains=home.<domain> run`
   - Cloudflare API token injected as environment variable `CF_DNS_API_TOKEN`
   - Token value never written to log file or TUI
   - Log: `[INFO] tls-challenge: dns-01 challenge started; domain=*.home.<domain>`
   - TUI: `TLS: requesting wildcard certificate for *.home.<domain>...`

5. `[AUTO]` Poll for challenge completion; timeout 300s (DNS propagation buffer)
   - Log every 30s: `[INFO] tls-challenge: waiting for dns propagation (Xs elapsed)`

6. `[AUTO]` On success: copy issued cert and key to `./certs/home.<domain>/`:
   - `fullchain.pem`
   - `privkey.pem`
   - Log: `[INFO] tls-cert: cert issued; path=./certs/home.<domain>/`
   - TUI: `TLS: certificate issued successfully`

7. `[AUTO]` Verify cert validity locally:
   - Parse cert; confirm: subject matches `*.home.<domain>`, expiry > 30 days,
     issuer is Let's Encrypt or configured CA
   - Log: `[INFO] tls-verify: cert valid; expiry=<date>`

8. `[AUTO]` Store cert and key in encrypted local cache for Vault pickup:
   - Write `./certs/home.<domain>/meta.json` with expiry and SAN list
   - Certs remain here until Vault is initialized (Stage 3 will read this path)
   - Log: `[INFO] tls-cache: cert stored for vault pickup`

### Branch Conditions

- Valid cert already exists with > 30 days expiry → skip issuance; mark stage
  complete; cert will be loaded into Vault when Stage 3 runs
- Cert exists but expires within 30 days → treat as missing; reissue
- Cert issuance fails with rate-limit error → enter exponential backoff with
  operator notification

### Failure Modes

| Step | Failure | Trigger |
|------|---------|---------|
| 1 | Cloudflare API unreachable | No internet, DNS failure, firewall |
| 4 | Challenge auth failure | Invalid API token, wrong zone ID |
| 4 | Challenge DNS propagation timeout | Slow Cloudflare propagation |
| 4 | Rate limit exceeded | Too many cert requests (Let's Encrypt: 5/week per domain) |
| 7 | Cert validation fails | Corrupt cert file, wrong SAN |

### Recovery Paths

- **1a** Cloudflare unreachable: emit error; `[VERIFY]` prompt: wait and retry
  or abort; suggest checking upstream connectivity
- **4a** Auth failure: surface specific Cloudflare API error; `[PROMPT]` to
  re-enter API token and zone ID; retry from step 4
- **4b** Rate limit: emit warning with timestamp of next available attempt;
  operator must wait; bootstrap paused — `[VERIFY]` prompt to retry or abort
- **General retry**: `bootstrap --stage tls` re-runs idempotently (skips if
  valid cert present)

### Handoff Contract

Produces:
- `./certs/home.<domain>/fullchain.pem` — wildcard TLS cert
- `./certs/home.<domain>/privkey.pem` — corresponding private key
- `./certs/home.<domain>/meta.json` — expiry and SAN metadata
- Cert is NOT yet in Vault (Vault is not running); Stage 3 will load it

### Observable States

| State | TUI | Log |
|-------|-----|-----|
| Checking existing | `TLS: checking for existing certificate...` | `[INFO] stage=tls action=cert-check` |
| Cert valid | `TLS: certificate valid until <date> — skipping` | `[INFO] stage=tls status=cert-valid` |
| Installing lego | `TLS: deploying cert tooling...` | `[INFO] stage=tls action=deploy-lego` |
| Challenge running | `TLS: DNS-01 challenge in progress...` | `[INFO] stage=tls action=dns-challenge` |
| Waiting propagation | `TLS: waiting for DNS propagation (Xs)...` | `[INFO] stage=tls dns-wait elapsed=Xs` |
| Cert issued | `TLS: certificate issued — *.home.<domain>` | `[INFO] stage=tls status=cert-issued` |
| Complete | `Stage 2: TLS — complete` | `[INFO] stage=tls status=complete` |
| Error | Red error panel; `see: <log path>:<line>` | `[ERROR] stage=tls error=<msg>` |

### Day-2 automatic renewal

Bootstrap installs a host systemd timer and oneshot service after initial
issuance. The default timer checks daily, with randomized delay, and renews when
the active certificate is within 30 days of expiry. These values are
configurable; automation can be disabled with a persistent expiry warning.

The job reads the zone-scoped Cloudflare token from a root-owned mode `0600`
runtime file, obtains a candidate in staging, verifies the chain, configured
SANs, issuer, expiry, and private-key match, then atomically switches the active
version. Consumers reload one at a time with health checks. Any failure restores
the previous version and revalidates affected consumers.

Renewal proceeds while Vault is sealed. Successful local deployment records
Vault synchronization as pending; the post-unseal reconciliation flow completes
the sync. GitLab and Runner are not in the renewal critical path. The complete
contract is in [certificate-renewal.md](certificate-renewal.md).

---

## Stage 3: Vault

### Prerequisites

- Stage 1 (DNS) complete: `vault.home.<domain>` resolves
- Stage 2 (TLS) complete: wildcard cert exists at `./certs/home.<domain>/`
- Target host DNS resolution is working (self-resolves `vault.home.<domain>`)
- Port 8200 not already bound by another process on target

### Happy Path

1. `[AUTO]` Check if Vault process is already running on target:
   - TCP port check: `<bootstrap_server_ip>:8200`
   - HTTP check: `GET http://<bootstrap_server_ip>:8200/v1/sys/health`
   - If Vault responding and initialized: jump to step 6 (seal check)
   - If port not open: continue to install

2. `[AUTO]` Run Ansible role `vault_install`:
   - Install Vault binary for target arch (x86_64 or aarch64)
   - Write Vault config: listener on `0.0.0.0:8200`; TLS cert paths pointing
     to staged certs from Stage 2; storage backend: `raft` (integrated, no
     external dep)
   - Create systemd unit; enable; do NOT start yet
   - Log: `[INFO] vault-install: binary and config deployed`

3. `[AUTO]` Copy TLS cert and key to Vault config paths on target:
   - Source: `./certs/home.<domain>/fullchain.pem` and `privkey.pem`
   - Destination: `/etc/vault.d/tls/` on target
   - Set permissions: `vault:vault 0400` for key, `0644` for cert
   - Log: `[INFO] vault-tls: certs deployed to vault config path`

4. `[AUTO]` Start Vault service via systemd:
   - `systemctl start vault`
   - Poll `GET https://vault.home.<domain>:8200/v1/sys/health` every 5s;
     timeout 60s
   - Log: `[INFO] vault-start: waiting for vault listener`

5. `[AUTO]` Check Vault init status:
   - `GET /v1/sys/init` → `{"initialized": false}` → proceed to init (step 5a)
   - `{"initialized": true}` → proceed to seal check (step 6)

   5a. `[AUTO]` Initialize Vault:
       - `PUT /v1/sys/init` with `secret_shares=<config>` and
         `secret_threshold=<config>`
       - Response contains unseal keys and root token
       - Write complete response to `./vault-init.json` immediately
       - Log: `[INFO] vault-init: initialized; unseal-keys=<count>; path=./vault-init.json`
       - TUI: display prominent warning box:
         `vault-init.json written — contains unseal keys and root token.
          Move this file to secure offline storage immediately.
          DO NOT commit this file to git.`

6. `[AUTO]` Check Vault seal status:
   - `GET /v1/sys/seal-status`
   - If `{"sealed": false}`: log `[INFO] vault-seal: already unsealed`; jump
     to step 8
   - If `{"sealed": true}`: continue to unseal (step 7)

7. `[VERIFY]` Unseal Vault using Shamir shares:
   - TUI prompts operator to provide unseal keys one at a time (masked input)
   - Each key is submitted to `PUT /v1/sys/unseal`
   - After each key: display progress `Unseal progress: N/threshold`
   - Repeat until `{"sealed": false}`
   - Log (after all keys submitted): `[INFO] vault-unseal: vault is unsealed`
   - Note: unseal keys can be read from `./vault-init.json` on first run;
     TUI can offer to read keys automatically on first-run path only

8. `[AUTO]` Authenticate with root token from `./vault-init.json` or
   environment variable `VAULT_TOKEN`:
   - `POST /v1/auth/token/lookup-self` to verify token validity
   - Log: `[INFO] vault-auth: root token valid`

9. `[AUTO]` Enable secrets engines:
   - `kv-v2` at `secret/` — for service credentials
   - `pki` at `pki/` — for internal cert management (future use)
   - Check each mount before enabling (idempotency)
   - Log per mount: `[INFO] vault-mount: enabled path=<path> type=<type>`

10. `[AUTO]` Write wildcard TLS cert and key to Vault:
    - Path: `secret/bootstrap/tls/wildcard`
    - Fields: `cert`, `key`, `expiry`, `domain`
    - Log: `[INFO] vault-secret: cert written path=secret/bootstrap/tls/wildcard`

11. `[AUTO]` Enable and configure Vault audit log:
    - File audit device at `/var/log/vault/audit.log` on target
    - Log: `[INFO] vault-audit: audit log enabled`

12. `[AUTO]` Write initial Vault policies:
    - `policy/bootstrap` — allows reading bootstrap secrets during subsequent stages
    - Check before writing (idempotency)
    - Log: `[INFO] vault-policy: bootstrap policy written`

13. `[AUTO]` Configure Authentik OIDC auth method in Vault (prep — Authentik not
    yet running):
    - Enable `oidc` auth method at `auth/oidc/`
    - Write placeholder config; Authentik OIDC client details will be filled in
      during Stage 5
    - Log: `[INFO] vault-auth-method: oidc auth method enabled (pending Stage 5 authentik config)`

14. `[AUTO]` Write root token to Vault's own KV for emergency access:
    - Path: `secret/bootstrap/vault/root-token`
    - Log: `[INFO] vault-secret: root token stored (also in vault-init.json)`

15. `[AUTO]` Generate a bootstrap-scoped Vault token for use by subsequent stages:
    - Policy: `bootstrap`; TTL: `24h`; no renewal
    - Write token to in-memory run context (never to disk or TUI)
    - Log: `[INFO] vault-token: bootstrap token generated ttl=24h`

### Branch Conditions

- Vault already running and initialized → skip install and init; jump to seal
  check
- Vault running, initialized, and unsealed → skip to secrets engine check
  (idempotency path)
- `vault-init.json` already exists → warn operator it will be overwritten only
  if re-initializing; on idempotency path, read existing token

### Failure Modes

| Step | Failure | Trigger |
|------|---------|---------|
| 2 | Ansible role fails | Arch detection error, package download failure |
| 4 | Vault fails to start | TLS cert path wrong, port conflict |
| 4 | Health poll timeout | Vault misconfigured; systemd unit failed |
| 5a | Init fails | Vault already initialized (race); API error |
| 5a | `vault-init.json` write fails | Disk full; permissions |
| 7 | Wrong unseal key | Operator enters incorrect key |
| 8 | Root token invalid | `vault-init.json` corrupted |
| 10 | Secret write fails | Policy or auth error |

### Recovery Paths

- **4a** Vault fails to start: dump `journalctl -u vault --no-pager -n 50` to
  log file; emit path to TUI; `[VERIFY]` retry or abort
- **5a-write** `vault-init.json` write failure: CRITICAL — halt immediately;
  emit to TUI in red: `CRITICAL: vault-init.json could not be written —
  unseal keys are lost if you restart. Resolve disk/permission issue and
  run: bootstrap --stage vault` Vault will need to be re-initialized
- **7a** Wrong unseal key: Vault API returns `400`; display error inline;
  allow operator to re-enter the key; Vault tracks progress correctly so
  only the wrong key needs re-entry
- **General retry**: `bootstrap --stage vault` re-runs idempotently; checks
  each resource before creating

### Handoff Contract

Produces:
- Vault running, initialized, and unsealed at `https://vault.home.<domain>:8200`
- `./vault-init.json` — unseal keys and root token (operator responsibility to secure)
- Secrets engine `kv-v2` at `secret/` with wildcard TLS cert loaded
- OIDC auth method enabled (pending Authentik config)
- Bootstrap-scoped token in run context for use by Stage 4+
- Audit log active

### Observable States

| State | TUI | Log |
|-------|-----|-----|
| Checking | `Vault: checking for existing instance...` | `[INFO] stage=vault action=pre-check` |
| Installing | `Vault: installing Vault binary...` | `[INFO] stage=vault action=install` |
| Starting | `Vault: starting Vault service...` | `[INFO] stage=vault action=start` |
| Initializing | `Vault: initializing (first run)...` | `[INFO] stage=vault action=init` |
| Keys written | Warning box shown in TUI | `[WARN] stage=vault vault-init.json written` |
| Unsealing | `Vault: enter unseal key N of threshold...` | `[INFO] stage=vault action=unseal progress=N/T` |
| Unsealed | `Vault: unsealed` | `[INFO] stage=vault status=unsealed` |
| Configuring | `Vault: configuring secrets engines and policies...` | `[INFO] stage=vault action=configure` |
| Complete | `Stage 3: Vault — complete` | `[INFO] stage=vault status=complete` |
| Error | Red error panel; `see: <log path>:<line>` | `[ERROR] stage=vault error=<msg>` |

---

## Stage 4: PostgreSQL

### Prerequisites

- Stage 3 (Vault): unsealed; bootstrap token valid
- Port 5432 available on the target
- Target has sufficient disk space

### Happy Path

1. `[AUTO]` Verify Vault is unsealed.
2. `[AUTO]` Check for the expected PostgreSQL instance and version.
3. `[AUTO]` Generate or retrieve the PostgreSQL admin credential in Vault.
4. `[AUTO]` Run `roles/postgresql_install` and restrict its listener and
   `pg_hba.conf` to configured service hosts/networks.
5. `[AUTO]` Create dedicated `authentik`, `gitlab`, and `maas` roles and
   databases. Store each credential at `secret/bootstrap/postgres/<service>`.
6. `[AUTO]` Verify each role can connect to its own database.
7. `[AUTO]` Verify each role is rejected by the other service databases.
8. `[AUTO]` Record version, health, database, and role evidence without secrets.

### Branch Conditions

- Expected PostgreSQL already healthy → reconcile databases and roles.
- Existing non-empty database or conflicting role → stop and request a repair
  decision; do not silently adopt, empty, or recreate it.

### Recovery Paths

- Install/start failure: preserve the data directory, emit service logs, retry.
- Database/role conflict: make no destructive change and show the conflicting
  object.
- Vault unavailable: halt before credential or database mutation.

### Handoff Contract

Produces PostgreSQL plus isolated databases, roles, and Vault credentials for
Authentik, GitLab, and MAAS. No application has yet populated them.

### Observable States

| State | TUI | Log |
|-------|-----|-----|
| Checking | `PostgreSQL: checking for existing instance...` | `[INFO] stage=postgres action=pre-check` |
| Installing | `PostgreSQL: installing...` | `[INFO] stage=postgres action=install` |
| Creating databases | `PostgreSQL: creating service databases...` | `[INFO] stage=postgres action=create-db name=<db>` |
| Verifying isolation | `PostgreSQL: verifying service role isolation...` | `[INFO] stage=postgres action=verify-isolation` |
| Complete | `Stage 4: PostgreSQL — complete` | `[INFO] stage=postgres status=complete` |
| Error | Red error panel; `see: <log path>:<line>` | `[ERROR] stage=postgres error=<msg>` |

---

## Stage 5: Authentik

### Prerequisites

- Stage 1 (DNS): `authentik.home.<domain>` resolves
- Stage 2 (TLS): wildcard certificate available
- Stage 3 (Vault): unsealed; bootstrap token valid
- Stage 4 (PostgreSQL): final Authentik database and credential verified
- Docker and ports 9000/9443 available

### Happy Path

1. `[AUTO]` Verify Vault and the final Authentik database are healthy.
2. `[AUTO]` Generate or retrieve the Authentik admin credential in Vault.
3. `[AUTO]` Render Authentik server and worker configuration with the Stage 4
   PostgreSQL DSN. The compose model contains no PostgreSQL service.
4. `[AUTO]` Run `roles/authentik_install`.
5. `[AUTO]` Wait for schema initialization and API health for up to 300 seconds.
6. `[AUTO]` Initialize or verify the bootstrap administrator.
7. `[AUTO][TERRAFORM bootstrap-authentik]` Plan and reconcile `adults` and
   `kids`, Vault/GitLab OIDC providers, applications, and policy bindings.
8. `[AUTO]` Store OIDC client credentials in Vault and complete Vault OIDC config.
9. `[AUTO]` Verify Authentik discovery and the Vault callback contract.

### Branch Conditions

- Authentik already healthy on the expected DSN → skip deployment and reconcile
  durable API objects.
- Authentik database contains an unexpected installation → stop; do not infer a
  brownfield import.
- Interrupted first startup → retain database and configuration, then retry.

### Recovery Paths

- Database unavailable: make no container change; repair or retry Stage 4.
- Startup failure: retain database/runtime configuration, dump container logs,
  and retry without dropping or restoring the database.
- OIDC failure: leave healthy Authentik running and retry the Terraform/API
  reconciliation idempotently.

### Handoff Contract

Produces healthy Authentik on its final database, bootstrap groups, Vault and
GitLab OIDC providers/applications, client credentials in Vault, and a verified
Vault OIDC discovery configuration.

### Observable States

| State | TUI | Log |
|-------|-----|-----|
| Dependencies | `Authentik: verifying Vault and PostgreSQL...` | `[INFO] stage=authentik action=pre-check` |
| Deploying | `Authentik: deploying on final database...` | `[INFO] stage=authentik action=deploy` |
| Initializing | `Authentik: initializing schema (Xs)...` | `[INFO] stage=authentik action=schema-wait elapsed=Xs` |
| Configuring OIDC | `Authentik: registering OIDC clients...` | `[INFO] stage=authentik action=create-oidc` |
| Complete | `Stage 5: Authentik — complete` | `[INFO] stage=authentik status=complete` |
| Error | Red error panel; `see: <log path>:<line>` | `[ERROR] stage=authentik error=<msg>` |

---

## Stage 6: GitLab + Runner

### Prerequisites

- Stage 1 (DNS): `gitlab.home.<domain>` resolves
- Stage 2 (TLS): wildcard cert in Vault
- Stage 3 (Vault): unsealed; bootstrap token valid; Authentik OIDC creds for
  GitLab at `secret/bootstrap/authentik/oidc/gitlab`
- Stage 4 (PostgreSQL): `gitlab` database and user exist with credentials in
  Vault at `secret/bootstrap/postgres/gitlab`
- Ports 80, 443 available (or configured alternate) on target
- Target has sufficient disk/memory (GitLab minimum: 4 GB RAM, 20 GB disk)

### Happy Path

1. `[AUTO]` Check Vault seal state:
   - If sealed: halt; emit error
   - If unsealed: log `[INFO] gitlab-preflight: vault unsealed`

2. `[AUTO]` Check if GitLab is already running:
   - TCP check: `<target>:443`
   - HTTP check: `GET https://gitlab.home.<domain>/-/health`
   - If responding: jump to step 9 (Runner idempotency check)
   - If not: continue

3. `[AUTO]` Read prerequisites from Vault:
   - PostgreSQL DSN for `gitlab` db: `secret/bootstrap/postgres/gitlab`
   - Authentik OIDC client: `secret/bootstrap/authentik/oidc/gitlab`
   - Log: `[INFO] gitlab-creds: prerequisites read from vault`

4. `[AUTO]` Generate GitLab admin password and secrets:
   - Check `secret/bootstrap/gitlab/admin` in Vault; generate if missing
   - Generate `gitlab_secret`, `gitlab_db_key_base` (random 128-char hex)
   - Write all to `secret/bootstrap/gitlab/`
   - Log: `[INFO] gitlab-creds: secrets generated and stored`

5. `[AUTO]` Run Ansible role `gitlab_install`:
   - Install GitLab CE (Omnibus or Docker — use Omnibus for arch compatibility)
   - Write `/etc/gitlab/gitlab.rb` from template with:
     - `external_url` = `https://gitlab.home.<domain>`
     - `gitlab_rails['db_adapter']` = `postgresql`
     - PostgreSQL DSN (from Vault, injected via env; not written to disk in
       plaintext — written as variable reference in gitlab.rb with value
       in environment)
     - TLS cert paths
     - Authentik OIDC provider config
     - GitLab Runner registration disabled at install (registered separately)
   - Run `gitlab-ctl reconfigure`
   - Log: `[INFO] gitlab-install: ansible role started; this may take several minutes`
   - TUI: `GitLab: installing (this takes ~5–10 minutes)...`

6. `[AUTO]` Wait for GitLab to become healthy:
   - Poll `GET https://gitlab.home.<domain>/-/health` every 15s; timeout 600s
   - Log every 60s: `[INFO] gitlab-wait: elapsed=Xs`

7. `[AUTO][TERRAFORM bootstrap-gitlab]` Configure durable GitLab API resources:
   - Display the saved Terraform plan and require approval in interactive mode
     (`--approve-terraform` is required for headless mutation)
   - Disable public signup
   - Verify OIDC sign-in configured by the Ansible-owned `gitlab.rb`
   - Create `infrastructure` and `services` top-level groups
   - Create `infrastructure/iac` project (initial Terraform state repository)
   - Commit initial CI and backend example files
   - Log per action: `[INFO] gitlab-terraform: action=<action> status=ok`

8. `[AUTO]` Prepare GitLab Terraform HTTP backend:
   - Generate a narrowly scoped backend credential; write it to Vault at
     `secret/bootstrap/gitlab/terraform-token`
   - Log: `[INFO] gitlab-tf-backend: terraform state project configured`

9. `[AUTO]` Install and register GitLab Runner:
   - Check if Runner already registered: `GET /api/v4/runners` and check for
     existing `bootstrap-runner`
   - If not registered:
     - Install Runner binary for target arch
     - Generate registration token from GitLab API
     - Register runner with tags: `bootstrap`, `terraform`, `ansible`
     - Write runner config to target
     - Start `gitlab-runner` service
   - Log: `[INFO] gitlab-runner: registered name=bootstrap-runner`

10. `[AUTO][TERRAFORM bootstrap-vault]` Configure Vault integration with GitLab:
    - Enable Vault JWT auth method for GitLab CI token injection
    - Write Vault role: `gitlab-ci` — allows reading `secret/services/*` and
      `secret/infrastructure/*` with TTL `1h`
    - Log: `[INFO] vault-gitlab: jwt auth configured for gitlab ci`

11. `[AUTO]` Smoke test GitLab OIDC login flow:
    - Fetch OIDC discovery document from Authentik for GitLab provider
    - Verify Authentik application redirect URIs match GitLab callback URL
    - Log: `[INFO] gitlab-oidc: callback uri verified`

12. `[AUTO]` Verify the Terraform-created project and repository files are ready
    for state migration (see Terraform State Migration flow below):
    - Verify `terraform/backend.tf.example` exists in `infrastructure/iac`
    - Log: `[INFO] gitlab-tf: initial states ready`

### Branch Conditions

- GitLab already running → skip install; check Runner registration and Vault
  integration (idempotency path)
- Runner already registered → skip Runner install; verify health
- Vault JWT auth already configured → skip; verify role exists

### Failure Modes

| Step | Failure | Trigger |
|------|---------|---------|
| 5 | GitLab reconfigure fails | Config syntax error, port conflict |
| 6 | Health timeout | GitLab OOM killed, slow migration |
| 7 | API config fails | Wrong admin token, API rate limit |
| 9 | Runner registration fails | Wrong registration token |
| 10 | Vault JWT config fails | Bootstrap token expired |

### Recovery Paths

- **6a** Health timeout: dump `gitlab-ctl tail` to log file; emit path to TUI;
  `[VERIFY]` retry (GitLab may still be migrating DB) or abort
- **5a** Reconfigure fails: emit `gitlab-ctl reconfigure` output to log file;
  common causes (port 80 in use, PostgreSQL unreachable) noted in TUI
- **General retry**: `bootstrap --stage gitlab` re-runs idempotently

### Handoff Contract

Produces:
- GitLab running at `https://gitlab.home.<domain>`
- GitLab authenticated via Authentik OIDC
- `infrastructure/iac` project with Terraform HTTP backend configured
- GitLab Runner registered with tags `bootstrap`, `terraform`, `ansible`
- Vault JWT auth configured for GitLab CI; role `gitlab-ci` created
- Terraform token in Vault at `secret/bootstrap/gitlab/terraform-token`

### Observable States

| State | TUI | Log |
|-------|-----|-----|
| Checking | `GitLab: checking for existing instance...` | `[INFO] stage=gitlab action=pre-check` |
| Installing | `GitLab: installing (5–10 min)...` | `[INFO] stage=gitlab action=install` |
| Waiting | `GitLab: waiting for startup (Xs)...` | `[INFO] stage=gitlab startup-wait elapsed=Xs` |
| Configuring | `GitLab: configuring admin and groups...` | `[INFO] stage=gitlab action=configure` |
| Registering Runner | `GitLab: registering Runner...` | `[INFO] stage=gitlab action=runner-register` |
| Configuring Vault | `GitLab: configuring Vault JWT auth...` | `[INFO] stage=gitlab action=vault-jwt` |
| Complete | `Stage 6: GitLab + Runner — complete` | `[INFO] stage=gitlab status=complete` |
| Error | Red error panel; `see: <log path>:<line>` | `[ERROR] stage=gitlab error=<msg>` |

---

## Stage 7: MAAS

### Prerequisites

- Stage 1 (DNS): `maas.home.<domain>` resolves; internal DNS running
- Stage 3 (Vault): unsealed; bootstrap token valid
- Stage 4 (PostgreSQL): `maas` database and user in Vault at
  `secret/bootstrap/postgres/maas`
- Stage 6 (GitLab): running (MAAS Terraform provider will target GitLab state
  backend)
- Port 5240 (MAAS HTTP), 5241 (MAAS HTTPS) available on target
- `lab_vlan_subnet` and `lab_gateway` validate
- When `maas_dhcp_enabled: true`: VLAN ID, target interface, and DHCP range are
  configured; the VLAN is routed and trunked to the target

### Happy Path

1. `[AUTO]` Check Vault seal state; halt if sealed.

2. `[AUTO]` Check if MAAS is already running:
   - TCP check: `<target>:5240`
   - HTTP check: `GET http://maas.home.<domain>:5240/MAAS/api/2.0/`
   - If responding: jump to step 6 (network config idempotency)
   - If not: continue

3. `[AUTO]` Read MAAS prerequisites from Vault:
   - PostgreSQL DSN: `secret/bootstrap/postgres/maas`
   - Generate MAAS admin password if missing; write to
     `secret/bootstrap/maas/admin`
   - Log: `[INFO] maas-creds: credentials ready`

4. `[AUTO]` Run Ansible role `maas_install`:
   - Install MAAS (snap or deb for target arch)
   - Run `maas init region+rack --database-uri <dsn>` (DSN injected via env;
     not logged)
   - Enable and start MAAS services
   - Log: `[INFO] maas-install: ansible role started`
   - TUI: `MAAS: installing...`

5. `[AUTO]` Wait for MAAS API to become healthy:
   - Poll `GET http://<target>:5240/MAAS/api/2.0/` every 10s; timeout 300s
   - Log every 60s: `[INFO] maas-wait: elapsed=Xs`

6. `[AUTO]` Configure MAAS base settings:
   - Create admin API key; log in via `maas login bootstrap-admin ...`
   - Set MAAS URL: `http://maas.home.<domain>:5240/MAAS`
   - Set upstream DNS to Technitium DNS IP
   - Set MAAS domain to `maas.home.<domain>`
   - Configure NTP server
   - Log: `[INFO] maas-config: base configuration applied`

7. `[AUTO][TERRAFORM bootstrap-maas]` Configure the durable lab network objects:
   - Display the saved Terraform plan and require approval in interactive mode
   - Define the fabric, VLAN, subnet, gateway, and optional dynamic range
   - Do not enable DHCP as a side effect of object creation
   - Log: `[INFO] maas-network: lab vlan configured; dhcp=disabled`
   - TUI: `MAAS: configuring lab network...`

8. `[AUTO]` Import Ubuntu OS images for PXE boot:
   - Import Ubuntu 22.04 and 24.04 images (amd64 and arm64)
   - This is async — MAAS downloads images in background
   - Log: `[INFO] maas-images: image import started (background)`
   - TUI: `MAAS: importing OS images (background — may take several minutes)...`

9. `[AUTO]` Generate MAAS API key for Terraform MAAS provider:
   - Create API key scoped to MAAS admin
   - Write to Vault at `secret/bootstrap/maas/terraform-api-key`
   - Log: `[INFO] maas-terraform: api key written to vault`

10. `[AUTO]` Verify the Terraform-owned
    `maas.home.<domain>` record created by Stage 1.

11. `[BRANCH]` Handle DHCP:
    - If `maas_dhcp_enabled: false`, log `dhcp=disabled` and complete.
    - If true, validate interface/VLAN presence, subnet containment, gateway,
      excluded infrastructure addresses, range, rack-controller health, and
      Technitium resolution from the VLAN.
    - Run a bounded DHCP-discovery probe and display the current observation.

12. `[VERIFY]` Display the cutover plan: interface, VLAN, subnet, gateway,
    range, observed authority, proposed MAAS authority, and rollback steps.
    Instruct the operator to disable UniFi DHCP on only the dedicated lab
    network, then require confirmation that the change was made.

13. `[AUTO]` Repeat the bounded DHCP probe. If any other offer is observed,
    refuse to continue. Otherwise enable MAAS DHCP and verify the MAAS API state
    plus a disposable client's lease, gateway, DNS server, and PXE discovery.

### Branch Conditions

- MAAS already running → skip install; verify base and Terraform network config
- OS images already imported → skip image import
- `maas_dhcp_enabled: false` → never make a DHCP-authority change
- MAAS DHCP already active with the exact approved configuration → verify it
  without toggling
- Another DHCP offer is observed → refuse activation

### Failure Modes

| Step | Failure | Trigger |
|------|---------|---------|
| 4 | MAAS snap install fails | Network, snap store unreachable |
| 4 | Database init fails | Wrong DSN, PostgreSQL not accepting connections |
| 5 | Health timeout | MAAS rack controller not registering |
| 7 | Network plan fails | VLAN/subnet/range conflict |
| 8 | Image import fails | No internet, wrong arch |
| 11 | DHCP preflight fails | VLAN/interface absent, Technitium unhealthy, invalid range |
| 13 | Conflicting DHCP detected | Another DHCP offer remains after confirmation |
| 13 | MAAS DHCP verification fails | API state or disposable-client lease is incorrect |

### Recovery Paths

- **4a** Database init failure: check PostgreSQL connectivity from target;
  verify `maas` db user has correct permissions; retry `bootstrap --stage maas`
- **7a/11a** Network validation failure: make no DHCP change; show the exact
  interface, VLAN, subnet, or range error and link the UniFi guide
- **13a** Activation failure: disable any partially enabled MAAS DHCP state and
  instruct the operator to restore the recorded UniFi DHCP setting for the lab
  VLAN; bootstrap does not call the UniFi API
- **8a** Image import fails: non-blocking — MAAS still functional; images can
  be re-imported after bootstrap; emit warning to TUI, not error

### Handoff Contract

Produces:
- MAAS running at `http://maas.home.<domain>:5240/MAAS`
- Lab network objects managed by Terraform
- DHCP disabled by default, or MAAS as the sole verified authority on the
  dedicated lab VLAN after an explicitly approved cutover
- Ubuntu 22.04 and 24.04 images importing (background)
- MAAS Terraform API key in Vault at `secret/bootstrap/maas/terraform-api-key`

### Observable States

| State | TUI | Log |
|-------|-----|-----|
| Checking | `MAAS: checking for existing instance...` | `[INFO] stage=maas action=pre-check` |
| Installing | `MAAS: installing...` | `[INFO] stage=maas action=install` |
| Configuring | `MAAS: configuring lab network...` | `[INFO] stage=maas action=configure-network` |
| Default DHCP | `MAAS: DHCP disabled (default)` | `[INFO] stage=maas dhcp=disabled` |
| Cutover approval | `MAAS: review DHCP cutover` | `[VERIFY] stage=maas action=approve-dhcp` |
| DHCP enabled | `MAAS: DHCP verified on lab VLAN` | `[INFO] stage=maas dhcp=enabled` |
| DHCP rollback | `MAAS: DHCP activation failed — restore UniFi` | `[ERROR] stage=maas dhcp=rollback` |
| Importing images | `MAAS: importing OS images (background)...` | `[INFO] stage=maas action=image-import status=background` |
| Complete | `Stage 7: MAAS — complete` | `[INFO] stage=maas status=complete` |
| Error | Red error panel; `see: <log path>:<line>` | `[ERROR] stage=maas error=<msg>` |

---

## Stage 8: k3s Clusters

### Prerequisites

- Stage 7 (MAAS): running; OS images imported; lab VLAN DHCP active only when
  MAAS-provisioned nodes require PXE
- Stage 6 (GitLab): running with Terraform HTTP backend configured
- Stage 3 (Vault): unsealed; JWT auth enabled for GitLab CI
- At least one node definition exists in `infrastructure/nodes/` in the
  `infrastructure/iac` GitLab project
- At least one cluster definition exists in `infrastructure/clusters/`
- Nodes referenced in cluster definitions are provisioned (via MAAS, Proxmox,
  or other provisioner) and SSH-reachable

NOTE: Unlike stages 1–7, k3s cluster setup is IaC-driven (Terraform +
Ansible). Bootstrap's role here is to trigger the initial IaC run that
establishes the first cluster(s). Subsequent cluster changes go through
GitOps (MR → pipeline → apply).

### Happy Path

1. `[AUTO]` Check Vault seal state; halt if sealed.

2. `[AUTO]` Check that at least one cluster definition file exists:
   - Clone `infrastructure/iac` from GitLab (using bootstrap token)
   - Check for `infrastructure/clusters/*.yaml`
   - If none found: emit warning; `[VERIFY]` prompt to continue (no clusters
     defined — bootstrap is complete but no clusters will be deployed);
     if operator confirms: mark stage complete and proceed to Terraform
     state migration

3. `[AUTO]` For each cluster definition:
   3a. `[AUTO]` Resolve node names to IPs (via MAAS API or `infrastructure/nodes/`
       provisioner definitions)
   3b. `[AUTO]` Verify each node is SSH-reachable
   3c. `[AUTO]` Run Ansible role `k3s_install` against the node(s):
       - Install k3s on control-plane nodes first
       - Join worker nodes to the cluster
       - Write kubeconfig to Vault at `secret/bootstrap/k3s/<cluster-name>/kubeconfig`
       - Log: `[INFO] k3s-install: cluster=<name> node=<node> role=<role>`

4. `[AUTO]` Verify each cluster is healthy:
   - Use kubeconfig from Vault: `kubectl get nodes`
   - All nodes should be `Ready`
   - Log: `[INFO] k3s-verify: cluster=<name> nodes=<count> status=all-ready`

5. `[AUTO]` Register each cluster as a named Terraform target:
   - Write cluster endpoint and kubeconfig reference to Vault at
     `secret/infrastructure/clusters/<name>`
   - Log: `[INFO] k3s-register: cluster=<name> endpoint=<url>`

6. `[AUTO]` Run initial Terraform apply for each cluster:
   - Uses GitLab Terraform HTTP backend
   - Applies Terraform modules: `k3s/cluster`, `k3s/namespaces`, base Helm
     releases (cert-manager placeholder, etc.)
   - Uses GitLab CI pipeline (triggers a pipeline run on `infrastructure/iac`)
   - Poll pipeline status until complete; timeout 600s
   - Log: `[INFO] k3s-terraform: pipeline triggered; pipeline_id=<id>`

### Branch Conditions

- No cluster definitions → warn and skip; bootstrap still completes
- Node not SSH-reachable → skip that node; emit warning; cluster deploy
  continues with available nodes
- Cluster already exists (kubeconfig in Vault) → skip install; verify health

### Failure Modes

| Step | Failure | Trigger |
|------|---------|---------|
| 3b | Node SSH unreachable | Node not provisioned, wrong IP |
| 3c | k3s install fails | Incompatible arch, resource limits |
| 4 | Cluster not healthy | Node not joining, network issue |
| 6 | Terraform pipeline fails | GitLab CI config error, Vault auth failure |

### Recovery Paths

- **3b** Node unreachable: skip node with warning; do not fail entire stage;
  node can be added later via GitOps
- **4a** Cluster unhealthy after install: dump `kubectl describe node` to log
  file; `[VERIFY]` retry or skip cluster
- **6a** Terraform pipeline fails: emit pipeline URL to TUI for operator to
  inspect; `[VERIFY]` retry or skip

### Handoff Contract

Produces:
- Each defined cluster deployed and healthy
- Kubeconfigs in Vault at `secret/bootstrap/k3s/<cluster-name>/kubeconfig`
- Cluster endpoints registered in Vault at `secret/infrastructure/clusters/<name>`
- Initial Terraform apply complete; cluster state in GitLab HTTP backend

### Observable States

| State | TUI | Log |
|-------|-----|-----|
| No clusters | Warning: `No cluster definitions found — skipping` | `[WARN] stage=k3s no-cluster-definitions` |
| Installing | `k3s: installing cluster <name>...` | `[INFO] stage=k3s action=install cluster=<name>` |
| Joining node | `k3s: joining node <node> to <cluster>...` | `[INFO] stage=k3s action=join-node node=<node>` |
| Verifying | `k3s: verifying cluster <name>...` | `[INFO] stage=k3s action=verify cluster=<name>` |
| Terraform | `k3s: running Terraform apply via GitLab CI...` | `[INFO] stage=k3s action=terraform-apply pipeline=<id>` |
| Complete | `Stage 8: k3s — complete` | `[INFO] stage=k3s status=complete` |
| Error | Red error panel; `see: <log path>:<line>` | `[ERROR] stage=k3s error=<msg>` |

---

## Cross-Cutting Flow: Terraform State Migration

> SC-14 completion condition — this is the event that marks bootstrap done.

### Trigger

All 9 stages (0–8) have completed successfully.

### Prerequisites

- GitLab running with `infrastructure/iac` project
- Terraform HTTP backend project access token in Vault at
  `secret/bootstrap/gitlab/terraform-token`
- Local Terraform state files exist for any resources created during bootstrap
  via Terraform
- All k3s cluster states are in local files (if bootstrap ran Terraform locally
  for any stage)

### Migration Steps

1. `[AUTO]` Enumerate local Terraform state files created during bootstrap:
   - Scan `./terraform/` for `*.tfstate` files
   - Log: `[INFO] tf-migration: found N state files`

2. `[AUTO]` For each state file:
   2a. `[AUTO]` Read Terraform HTTP backend config from Vault
   2b. `[AUTO]` Update `backend.tf` to point to GitLab HTTP backend; supply the
       credential through `TF_HTTP_PASSWORD`, never in the file:
       ```
       terraform {
         backend "http" {
           address = "https://gitlab.home.<domain>/api/v4/projects/<id>/terraform/state/<workspace>"
           username = "bootstrap"
         }
       }
       ```
   2c. `[AUTO]` Run `terraform init -migrate-state` in each directory
   2d. `[AUTO]` Verify GitLab returns the state with matching lineage and serial
   2e. `[AUTO]` Keep the local `*.tfstate` file until every state verifies and
       the operator confirms the handoff
   2f. `[AUTO]` Log: `[INFO] tf-migration: state=<name> verified in gitlab backend`

3. `[VERIFY]` Confirm with operator:
   `All local Terraform state has been migrated to GitLab.
    State lineage and serial values have been verified.
    GitLab HTTP backend is now the source of truth.
    Proceed? [y/N]`

4. `[AUTO]` After confirmation, remove redundant local state files. If removal
   fails, warn but do not invalidate the verified GitLab state.

5. `[AUTO]` Write completion marker to `./logs/run-<timestamp>/bootstrap-complete.json`:
   - Fields: timestamp, all stage completion times, GitLab URL, Vault URL
   - Log: `[INFO] bootstrap: COMPLETE; all stages done; state migrated to gitlab`

6. `[AUTO]` TUI displays bootstrap completion summary:
   - All services and their URLs
   - `vault-init.json` location reminder (if not yet secured)
   - GitLab URL with first-login instructions
   - Reminder: all future changes go through GitOps (`infrastructure/iac` repo)

### Failure Modes

| Step | Failure | Trigger |
|------|---------|---------|
| 2c | `terraform init -migrate-state` fails | Wrong backend URL, auth failure |
| 2d | State verification fails | GitLab HTTP backend not responding |

### Recovery Paths

- **2c** Migration failure: do NOT delete local state file; emit error to TUI
  with specific `terraform` error output; `[VERIFY]` retry individual state or
  skip (local state remains valid as fallback)
- Any failure here is non-destructive — local state is authoritative until
  confirmed migrated and deleted

---

## Cross-Cutting Flow: Service Deploy (Post-Bootstrap)

> This flow is entirely GitOps-driven. No bootstrap tool involvement.

### Trigger

Developer writes a service definition file and opens a merge request against
`infrastructure/iac`.

### Happy Path

1. Developer writes `services/<name>.yaml` using the service definition schema
2. Developer opens MR against `infrastructure/iac` default branch
3. GitLab CI pipeline runs on MR:
   - `[AUTO]` Validate YAML schema
   - `[AUTO]` `terraform plan` — shows DNS record, cert, Helm chart changes
   - `[AUTO]` Plan output posted as MR comment
4. Developer reviews plan; approves and merges MR
5. GitLab CI pipeline runs on merge to default branch:
   - `[AUTO]` `terraform apply` runs:
     - Technitium DNS record created for `hostname`
     - If `authentik: true`: Authentik OIDC provider + application created
     - Helm chart deployed to named cluster
   - `[AUTO]` Pipeline reports success/failure as GitLab commit status
6. Service is reachable at `hostname` within target SLA (< 10 min from merge)

### Branch Conditions

- `authentik: true` → OIDC provider and Authentik application created;
  outbound proxy configured for web service
- `authentik: false` → No SSO proxy; service exposed directly; access enforced
  by `allowed_groups` at application level
- `target.provisioner: maas` (bare metal) → Terraform MAAS provider provisions
  machine; Ansible role applied post-provision

### Failure Modes

- `terraform plan` fails → MR blocked; developer fixes definition
- Helm chart deploy fails → Pipeline fails; GitLab CI shows error; service not
  reachable; developer inspects logs in GitLab UI

---

## Cross-Cutting Flow: Reboot and Manual Unseal

### Automatic boot

1. `[AUTO]` Start network, firewall, and required storage mounts.
2. `[AUTO]` Start Technitium and verify private plus external DNS.
3. `[AUTO]` Start Vault; record its expected sealed state.
4. `[AUTO]` Start PostgreSQL, then Authentik, GitLab/Runner, and MAAS using
   protected runtime credentials.
5. `[AUTO]` Start k3s; mark workloads requiring fresh Vault delivery pending.
6. `[AUTO]` Allow eligible backup components while marking the Vault component incomplete.
7. `[AUTO]` Publish `degraded: Vault sealed` with available and paused capabilities.

### Operator unseal

1. `[AUTO]` `bootstrap vault unseal` verifies SSH host and Vault cluster identity.
2. `[PROMPT]` Request one masked Shamir share at a time.
3. `[AUTO]` Submit through the API from memory and display `N of threshold`.
4. `[BRANCH]` Wrong share re-prompts; progress reset or identity change stops.
5. `[AUTO]` On threshold, wait for unsealed/active health.

### Reconciliation

1. `[AUTO]` Verify Vault health, Raft peers, and audit device.
2. `[AUTO]` Verify Authentik discovery/Vault OIDC and GitLab JWT authentication.
3. `[AUTO]` Compare authoritative secret versions with runtime-copy metadata.
4. `[VERIFY]` Review any stale-copy updates and affected service reloads.
5. `[AUTO]` Apply approved updates atomically and run deferred health checks.
6. `[AUTO]` Create a missed Vault Raft snapshot when backups are enabled.
7. `[AUTO]` Set healthy status only after required checks pass.

---

## Cross-Cutting Flow: Backup and Recovery

### Configuration flow

1. `[PROMPT]` Offer **Configure automated backups**, **One-time controller
   export**, or **Skip for now — trial mode**, with persistent protection
   recommended.
2. `[PROMPT]` Collect a TrueNAS NFS path, SFTP/custom Restic URI, or mounted path.
3. `[AUTO]` Verify mount source, write access, and repository identity; initialize
   only after the operator approves a new empty repository.
4. `[PROMPT]` Show schedule and retention defaults:
   daily 02:30; 7 daily, 5 weekly, 12 monthly.
5. `[PROMPT]` Allow preset or custom backup, metadata-check, full-check,
   USB-copy, and restore-drill intervals.
6. `[BRANCH]` If trial mode is selected, persist `backup_enabled: false`, require
   no repository/password/mount, show unprotected status, and continue bootstrap.
7. `[AUTO]` Install the Restic binary, protected repository-password file,
   application backup hooks, systemd service, mount dependency, and timer.
8. `[VERIFY]` Show the complete effective policy before enabling the timer.
9. `[BRANCH]` A later configuration edit plus lifecycle-stage rerun follows this
   same flow without reinstalling services.

### Scheduled NAS backup

1. `[AUTO]` Lock the backup run and validate the expected mount and repository ID.
2. `[AUTO]` Produce native Vault, PostgreSQL, GitLab, Authentik, Technitium,
   MAAS, k3s, and recovery-metadata artifacts.
3. `[AUTO]` Run Restic and verify the new snapshot appears with the component manifest.
4. `[AUTO]` Apply configured retention; initial setup is dry-run-only.
5. `[AUTO]` Run metadata or full-data integrity checks when due.
6. `[AUTO]` Remove staging data only after snapshot verification.
7. `[AUTO]` Record snapshot IDs, component results, integrity state, and USB-copy age.

### Offline USB copy

1. `[PROMPT]` Ask the operator to connect the recorded removable device.
2. `[AUTO]` Validate filesystem UUID, mount source, and destination repository ID.
3. `[AUTO]` Copy eligible verified snapshots from NAS to the USB repository.
4. `[AUTO]` Verify destination snapshots, safely unmount, and instruct disconnect.

### Restore

1. `[VERIFY]` Select repository, snapshot, components, isolated target, and
   acceptable recovery point.
2. `[AUTO]` Restore into an isolated directory or disposable host by default.
3. `[AUTO]` Recover in order: networking/Technitium, Vault and manual unseal,
   PostgreSQL, Authentik, GitLab/state, MAAS/k3s, then GitOps reconciliation.
4. `[VERIFY]` Run component health, data, login, and end-to-end service checks.
5. `[VERIFY]` Require separate approval before replacing any running service.

---

## Cross-Cutting Flow: Upgrades and Maintenance

### Proposal and review

1. `[AUTO]` Renovate opens or refreshes a pinned-version MR and Dependency
   Dashboard entry with update class, release notes, deadline, compatibility,
   downtime, backup, rollback, and health-check metadata.
2. `[PROMPT]` Before the deadline, the administrator may approve, reject that
   version, or choose another supported target.
3. `[AUTO]` At 3/7/21/30 days for critical security, other security,
   patch-minor, and major updates respectively, mark the proposal
   `approved-by-policy`.
4. `[BRANCH]` If scheduled application maintenance is disabled, keep the update
   overdue and display a persistent manual-maintenance warning.

### Maintenance window

1. `[AUTO]` At Sunday 03:00 local by default, lock the maintenance queue and
   order eligible components one at a time.
2. `[AUTO]` Recalculate the supported version path and check platform health,
   capacity, artifacts, backup freshness, migration state, and rollback plan.
3. `[BRANCH]` A failed gate makes no change, records the blocker, and retains
   overdue state for the next window.
4. `[AUTO]` Pause affected jobs or writes and launch a host-side oneshot
   executor that survives GitLab being restarted.
5. `[AUTO]` Apply one safe version step, wait for readiness and migrations, and
   run component plus platform health checks.
6. `[BRANCH]` On safe rollback failure, restore the prior immutable artifact;
   after an irreversible migration, stop and require the documented restore
   decision.
7. `[AUTO]` Continue only while the three-hour admission window allows; finish
   an in-progress safe step rather than abandoning it.
8. `[AUTO]` Report installed, blocked, rolled-back, overdue, and reboot-required
   states without automatically rebooting.

### Trial mode

`backup_enabled: false` permits deadline-driven patch/minor upgrades only for a
stateless workload with a proven immutable rollback. Data-bearing and major
updates remain blocked until a verified backup repository exists. A same-host
checkpoint never satisfies this gate.

The complete policy and component-specific GitLab, K3s, Ubuntu, and stateful
service constraints are in [upgrade-policy.md](upgrade-policy.md).

---

## Cross-Cutting Flow: Constrained Observability

### Configuration

1. `[PROMPT]` Present target-local Uptime Kuma + Beszel with the 512 MiB,
   half-core, 5 GiB budget and accepted retention tiers.
2. `[PROMPT]` Offer **Configure paging** (recommended) or **Dashboard only**.
3. `[BRANCH]` For paging, select Discord, Telegram, ntfy, or generic webhook and
   collect the credential using masked input.
4. `[PROMPT]` Show the 3-failure service debounce, 15-minute resource sustain,
   85/95% disk thresholds, six-hour reminder, recovery messages, and warning
   quiet-hour settings; allow edits.
5. `[AUTO]` Send a test notification with no secret content.
6. `[BRANCH]` A failed test allows retry/provider change or explicit
   `paging_enabled: false`; it can never mark paging configured.
7. `[AUTO]` Install pinned Kuma, Beszel hub/agent over a local Unix socket, and
   the health adapter with local state, cgroup limits, and seven-day Kuma/log
   retention.
8. `[AUTO]` Materialize critical platform checks and every declared service
   health check, then verify dashboard and Authentik access.
9. `[VERIFY]` Run idle/synthetic resource-budget checks and display the
   local-only whole-host outage limitation.

### Runtime

1. `[AUTO]` Kuma performs critical 60-second and ordinary five-minute functional
   checks; Beszel records its accepted native aggregation tiers.
2. `[AUTO]` Health-adapter heartbeats report backup, Vault, certificate,
   upgrade, reboot, and monitoring self-state.
3. `[BRANCH]` Dependency and maintenance rules suppress cascaded or expected
   alerts without stopping collection.
4. `[AUTO]` Sustained failures route to configured destinations with reminders
   and one recovery notification.
5. `[BRANCH]` Resource pressure lengthens noncritical Kuma intervals and
   disables optional Beszel collectors before marking telemetry degraded.
6. `[AUTO]` Status distinguishes healthy, paging disabled, paging failed,
   telemetry resource-degraded, and monitoring unavailable.

TrueNAS is never an execution target for this flow. It may store Restic
artifacts containing monitoring configuration only. The complete policy is in
[observability.md](observability.md).

---

## Cross-Cutting Flow: GitLab Loss and GitOps Recovery

### Normal protection

1. `[AUTO]` During the nightly backup, create the native GitLab archive,
   isolated GitLab database dump, `/etc/gitlab` configuration/secrets,
   TLS/SSH identity, Runner metadata, exact version/edition/package artifact,
   hashes, and component manifest.
2. `[AUTO]` After every successful infrastructure apply, record the applied
   commit, pipeline, workspace, lineage, and serial.
3. `[AUTO]` In root-only tmpfs, create and validate critical Git bundles and
   download/parse each changed Terraform state.
4. `[AUTO]` Snapshot the checkpoint into encrypted Restic, reopen it, verify
   hashes/commits/lineage/serial, then clear tmpfs.
5. `[BRANCH]` A checkpoint failure leaves the apply in
   `succeeded; recovery checkpoint failed`, pages when configured, and retries
   without rolling live infrastructure back.

### Incident and restore

1. `[AUTO]` `bootstrap recover gitlab` verifies pinned SSH and writes a named
   freeze marker consumed by Runner, Terraform, GitOps, upgrades, and
   maintenance.
2. `[AUTO]` Preserve diagnostics and damaged-state evidence when space permits.
3. `[VERIFY]` Show full backups/checkpoints, completeness, version/edition,
   package hash, commits, state lineage/serial, integrity result, space, and
   estimated loss window.
4. `[AUTO]` Install the exact GitLab package and restore configuration, secrets,
   TLS/SSH identity, only GitLab's isolated database, and native application
   data.
5. `[AUTO]` Run GitLab health, Gitaly, database, secrets-decryption, repository,
   and migration checks.
6. `[BRANCH]` If a newer critical checkpoint exists, reconcile only validated
   commit graphs and matching workspace/lineage/serial, upload the state through
   GitLab, and verify a re-download.
7. `[AUTO]` Run login, project, clone/push, Terraform read-only plan, Runner test
   job, backup/checkpoint, and monitoring checks.
8. `[VERIFY]` Display evidence and require the incident ID plus typed unfreeze;
   web readiness alone never resumes applies.

### Break-glass state

Local Terraform from an exported state requires a named incident, typed
confirmation, a reviewed non-destructive saved plan, protected before/after
states, and reconciliation back into GitLab before unfreeze. It is not
automatic failover.

The full contract is in
[gitlab-disaster-recovery.md](gitlab-disaster-recovery.md).

---

## Cross-Cutting Flow: Controller Laptop Recovery

### Normal protection

1. `[AUTO]` Create a random controller ID at initial bootstrap and record its
   target-side adoption marker.
2. `[PROMPT]` Collect a distinct controller-recovery password using masked
   input and require the operator to preserve it in the password manager.
3. `[AUTO]` Build a portable, schema-versioned manifest containing sanitized
   configuration, target fingerprint and ID, backend/ownership metadata, run
   manifests, protection status, and verified bootstrap artifact.
4. `[AUTO]` Encrypt the controller SSH private key in a separate envelope using
   the controller-recovery password, then place that envelope in the Restic-
   protected bundle.
5. `[VERIFY]` Assert that Vault init/Shamir material, repository passwords,
   plaintext `.env`, and the envelope password are absent.
6. `[AUTO]` Refresh the artifact after material controller/configuration
   changes, include it in post-apply checkpoints, and include the newest sealed
   copy in nightly backups.

### Blank-laptop recovery

1. `[VERIFY]` Acquire a downloaded or preserved bootstrap release and verify
   its checksum and source/build identity.
2. `[PROMPT]` Open the expected Restic repository using password-manager input;
   stop if its repository ID differs.
3. `[VERIFY]` Select a bundle by creation time, target/controller IDs, source
   commit, schema compatibility, hashes, and protection age.
4. `[PROMPT]` Open the SSH-key envelope using masked password-manager input, or
   branch to physical-console enrollment of a new key.
5. `[AUTO]` Rebind portable paths and atomically reconstruct `config.yaml`,
   `.env`, the bootstrap known-hosts file, and `~/.bootstrap` with protected
   files at mode `0600`.
6. `[VERIFY]` Compare the target ID and saved SSH fingerprint before
   authentication; an intentional replacement uses the changed-key flow.
7. `[AUTO]` Perform a mutation-free audit of target health, Vault seal state,
   GitLab, applied Git/state metadata, backups, DNS, certificates, ownership,
   controller activity, and freeze/maintenance state.
8. `[VERIFY]` Display every difference and proposed action. Require the typed
   target plus **old controller unavailable** or **lost/compromised** before
   adoption.
9. `[AUTO]` Write the new controller marker, run non-mutating platform plans and
   health checks, create a fresh bundle, and restore-open it for verification.

### Recovery mode branch

- `[BRANCH]` **Old controller unavailable** preserves existing SSH and
  controller-specific tokens, and records a warning that the old laptop may
  retain access.
- `[BRANCH]` **Lost or compromised** installs and verifies replacement SSH and
  service credentials before revoking old access. It inventories successful,
  failed, deferred, and manual rotations.
- `[BRANCH]` Suspected Shamir-share exposure presents the separate Vault rekey
  process; shares are never silently rotated or sourced from the bundle.

### Trial profile

`bootstrap controller export --output <external-path>` creates and restore-
verifies the portable bundle on removable storage or another device. A copy
kept only on the administrator laptop does not satisfy controller-loss
protection and leaves a persistent warning.

The complete contract is in
[controller-recovery.md](controller-recovery.md).

---

## Cross-Cutting Flow: Server Adoption

> Triggered by: `just adopt-server <ip>`

### Prerequisites

- Bootstrap is complete (GitLab running)
- Target server is reachable at `<ip>` via SSH
- Target server has BMC/IPMI accessible (or known to not have one)
- Operator's machine has `just` installed and `config.yaml` present

### Happy Path

1. `[AUTO]` `just adopt-server <ip>` invokes the bootstrap binary in adopt mode
2. `[AUTO]` SSH connectivity check to `<ip>`; detect OS and arch
3. `[AUTO]` Attempt BMC/IPMI detection:
   - Check `<ip>` for IPMI port 623/UDP
   - If detected: attempt IPMI info read; record BMC IP, supported features
   - If not detected: log `[INFO] adopt: no bmc detected; continuing`
4. `[AUTO]` Collect server metadata:
   - CPU model, core count, RAM, disk layout
   - Network interfaces and MACs
   - Architecture (x86_64, aarch64)
5. `[AUTO]` Generate MAAS machine definition from metadata
6. `[AUTO]` Clone `infrastructure/iac` from GitLab (short-lived token from
   Vault)
7. `[AUTO]` Write `infrastructure/nodes/<hostname>.yaml` with collected metadata
   and provisioner
8. `[AUTO]` Push branch to GitLab; open MR:
   - MR title: `adopt: add node <hostname> (<ip>)`
   - MR body: hardware summary table; BMC status; suggested cluster assignment
   - MR auto-assigned to admin user
9. `[AUTO]` Emit MR URL to operator's terminal:
   `MR opened: https://gitlab.home.<domain>/infrastructure/iac/-/merge_requests/<id>`

### Server Adoption Post-MR

After operator reviews and merges MR:

10. `[AUTO]` GitLab CI pipeline runs `terraform apply`
11. `[AUTO]` Terraform MAAS provider: creates machine record in MAAS with MAC
    address, architecture, BMC details (if present)
12. `[AUTO]` MAAS commissions machine: PXE boots; hardware discovery; marks
    as `Ready`
13. `[AUTO]` If node definition references a cluster: Ansible role `k3s_install`
    runs to join node to the cluster
14. `[AUTO]` Node appears in cluster: `kubectl get nodes` shows new node `Ready`

### Failure Modes

| Step | Failure | Trigger |
|------|---------|---------|
| 2 | SSH unreachable | Wrong IP, firewall |
| 3 | IPMI auth failure | Wrong credentials; IPMI not enabled |
| 6 | GitLab clone fails | Token expired, GitLab unreachable |
| 12 | MAAS commission fails | PXE boot failure, wrong MAC, BIOS settings |

### Recovery Paths

- **2a** SSH failure: emit specific error; operator corrects IP; retry
- **12a** MAAS commission failure: operator inspects MAAS UI; common causes
  (BIOS boot order, PXE not enabled) documented in TUI error; MAAS allows
  retry from UI

---

## Global Invariants

These constraints apply to every stage and every flow:

1. **Vault seal check**: any stage that reads from or writes to Vault must
   verify Vault is unsealed before proceeding. Never assume unsealed state
   persists between stages.

2. **Credential injection**: Cloudflare, paging, PostgreSQL, GitLab, Authentik,
   and backup credentials are injected at runtime from protected mode `0600`
   files or tightly scoped environment blocks. Secret values are never process
   arguments, log entries, TUI plaintext, structural configuration, Git
   content, or generated unit text.

3. **Idempotency gate**: every stage begins by checking whether its target
   state already exists. If it does, the stage emits a status line and
   skips creation. Stages never fail because a resource already exists.

4. **Log file path reference**: every `[AUTO]` failure emits to both the TUI
   error panel and the log file. The TUI error panel always includes the
   absolute path and line number of the log entry:
   `see: /Users/brian/src/bootstrap-home-infra/logs/run-<timestamp>/bootstrap.log:<line>`

5. **Network checks are explicit**: DNS resolution and port reachability checks
   are numbered steps in the happy path. They are never assumed.

6. **Rollback or skip for every failure**: no failure mode is terminal without
   an operator decision. Every failure leads to either: retry with corrected
   input, skip with warning, rollback to prior state, or abort with clear
   instructions.

7. **`vault-init.json` is sacred**: if this file cannot be written during Vault
   init, bootstrap halts immediately with a CRITICAL error. There is no
   recovery path that involves discarding unseal keys.

8. **Architecture neutrality**: every Ansible role and binary selection step
   must branch on detected arch (x86_64 or aarch64) and select the correct
   artifact. Arch detection is performed in Stage 0 and available to all
   subsequent stages via the run manifest.
