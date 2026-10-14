# Bootstrap Workflow Map

> Produced by: workflow-architect
> Source of truth: `docs/prd.md`
> Last updated: 2026-03-19
> Status: Design workflow; not a record of verified runtime behavior
> Implementation notes updated: 2026-10-14 — see [notes.md](notes.md)

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
   - **Lab VLAN subnet** — CIDR for lab segment (MAAS will manage DHCP here)
   - **Admin email** — used for Vault, GitLab, Authentik admin accounts
   - **Initial admin password** — master password for all bootstrap admin accounts
     (masked input; must meet minimum strength requirements)
   - **Vault unseal shares count** — default `5`; minimum `3`
   - **Vault unseal threshold** — default `3`

4. `[VERIFY]` Display collected config summary (credentials masked); prompt:
   `Proceed with these values? [y/N]`

5. `[AUTO]` Write `config.yaml` to working directory (credentials stored as
   references, not plaintext — Cloudflare token and admin password stored in
   local OS keychain or as environment variable references)
   - Log: `[INFO] config written; path=./config.yaml`

6. `[AUTO]` Validate SSH connectivity to target host:
   - Attempt SSH connection with provided key; timeout 10s
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
| 7 | `uname` command fails | Minimal OS image without standard utils |
| 8 | OS version check fails | Non-Ubuntu or Ubuntu < 22.04 |

### Recovery Paths

- **6a** SSH failure: display error with specific reason; allow operator to
  correct host/user/key values and retry; `[PROMPT]` for corrected values;
  re-run step 6
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

6. `[AUTO]` Create initial DNS zone for `config.internal_domain` via API
   - Check if zone exists first (idempotency)
   - If zone exists: log `[INFO] dns-zone: already exists; skipping`
   - If not: create zone; log `[INFO] dns-zone: created domain=<domain>`

7. `[AUTO]` Create A records for bootstrap services that will be needed by
   downstream stages:
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

9. `[AUTO]` Configure target host's `/etc/resolv.conf` (or systemd-resolved)
   to use `127.0.0.1` as primary DNS, with original upstream as fallback
   - Log: `[INFO] dns-resolver: target host resolver configured`

10. `[AUTO]` Verify DNS resolution from target host itself:
    - SSH command: `dig vault.home.<domain> @127.0.0.1`
    - Expect: resolves correctly
    - Log: `[INFO] dns-verify: target host resolution confirmed`

### Branch Conditions

- Technitium already running on port 5380 → skip install; jump to zone/record
  creation (idempotency path)
- Docker already installed on target → skip Docker install step within Ansible role
- DNS zone already exists → skip zone creation; proceed to record upserts

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
- **General retry**: `bootstrap --stage dns` re-runs entire stage idempotently

### Handoff Contract

Produces:
- Technitium DNS running on `<bootstrap_server_ip>:53` and `:5380`
- Zone `home.<domain>` with A records for all bootstrap service hostnames
- Target host `/etc/resolv.conf` points to local Technitium instance
- Technitium API session details written to run manifest

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
      during Stage 4
    - Log: `[INFO] vault-auth-method: oidc auth method enabled (pending authentik config)`

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

## Stage 4: Authentik

### Prerequisites

- Stage 1 (DNS): `authentik.home.<domain>` resolves
- Stage 2 (TLS): wildcard cert in `./certs/home.<domain>/`
- Stage 3 (Vault): unsealed; bootstrap token in run context; `kv-v2` mounted
- Target host has Docker available (deployed in Stage 1 DNS install path)
- Port 9000 (HTTP) and 9443 (HTTPS) available on target

### Happy Path

1. `[AUTO]` Check Vault seal state before proceeding:
   - `GET /v1/sys/seal-status` using bootstrap token
   - If sealed: emit error to TUI and log; halt with message:
     `Vault is sealed — unseal Vault before continuing Stage 4`
   - If unsealed: log `[INFO] authentik-preflight: vault unsealed`

2. `[AUTO]` Check if Authentik is already running:
   - TCP port check: `<bootstrap_server_ip>:9443`
   - HTTP check: `GET https://authentik.home.<domain>/api/v3/root/config/`
   - If responding: jump to step 7 (OIDC client idempotency check)
   - If not: continue

3. `[AUTO]` Retrieve Authentik admin password from Vault:
   - Path: `secret/bootstrap/authentik/admin`
   - If not found: generate random password; write to Vault at that path
   - Token used: bootstrap token from run context
   - Log: `[INFO] authentik-creds: admin credentials ready (from vault)`

4. `[AUTO]` Run Ansible role `authentik_install`:
   - Deploy Authentik via Docker Compose (server + worker + Redis)
   - Inject: admin password (from Vault, via env var), PostgreSQL DSN (from
     Vault — wait, PostgreSQL not yet running; use SQLite for bootstrap
     Authentik, migrate to PostgreSQL in Stage 5 post-deploy)

   NOTE: Authentik requires a database. For the bootstrap stage, Authentik
   will use its bundled PostgreSQL (single-container path). After Stage 5
   (dedicated PostgreSQL) is up, Authentik is migrated to the shared instance.
   This migration is handled at the end of Stage 5.

   - Log: `[INFO] authentik-install: ansible role started`
   - TUI: `Authentik: deploying...`

5. `[AUTO]` Wait for Authentik to become healthy:
   - Poll `https://authentik.home.<domain>/api/v3/root/config/` every 10s;
     timeout 300s (Authentik startup is slow)
   - Log every 30s: `[INFO] authentik-wait: elapsed=Xs`

6. `[AUTO]` Set Authentik admin password via API (idempotent — uses a known
   admin username):
   - `PATCH /api/v3/core/users/<admin-pk>/set_password/`
   - Log: `[INFO] authentik-admin: admin password set`

7. `[AUTO]` Create Authentik groups (idempotent — check before create):
   - `adults` group
   - `kids` group
   - Log per group: `[INFO] authentik-group: created name=<group>`

8. `[AUTO]` Create Authentik OIDC provider for Vault:
   - Name: `vault`; redirect URIs: `https://vault.home.<domain>/ui/vault/auth/oidc/oidc/callback`
   - Note client ID and client secret
   - Check if provider exists by name before creating (idempotency)
   - Log: `[INFO] authentik-oidc: vault provider created`

9. `[AUTO]` Create Authentik OIDC provider for GitLab:
   - Name: `gitlab`; redirect URIs: `https://gitlab.home.<domain>/users/auth/openid_connect/callback`
   - Note client ID and client secret
   - Log: `[INFO] authentik-oidc: gitlab provider created`

10. `[AUTO]` Write all OIDC client credentials to Vault:
    - Path: `secret/bootstrap/authentik/oidc/vault` — `{client_id, client_secret}`
    - Path: `secret/bootstrap/authentik/oidc/gitlab` — `{client_id, client_secret}`
    - Log: `[INFO] vault-secret: oidc credentials written for vault and gitlab`

11. `[AUTO]` Complete Vault OIDC auth method configuration:
    - Read OIDC client creds from `secret/bootstrap/authentik/oidc/vault`
    - Write to Vault OIDC auth method: `oidc_discovery_url`, `client_id`,
      `client_secret`, `oidc_scopes`
    - Log: `[INFO] vault-oidc: authentik oidc config applied`

12. `[AUTO]` Create Authentik application entries (one per service needing SSO):
    - Vault application pointing to Vault OIDC provider
    - GitLab application pointing to GitLab OIDC provider
    - Check before creating (idempotency)
    - Log per app: `[INFO] authentik-app: created name=<name>`

13. `[AUTO]` Verify Authentik SSO flow for Vault (smoke test):
    - Fetch OIDC discovery document:
      `GET https://authentik.home.<domain>/application/o/vault/.well-known/openid-configuration`
    - Verify issuer, authorization endpoint present
    - Log: `[INFO] authentik-verify: vault oidc discovery ok`

### Branch Conditions

- Authentik already running → skip install; check groups, OIDC providers, apps
  for idempotency
- Bootstrap PostgreSQL (bundled) vs. shared PostgreSQL: shared PostgreSQL is
  not available until Stage 5; Authentik uses bundled until Stage 5 migration
- OIDC provider already exists by name → skip creation; read existing
  `client_id` and update Vault if needed

### Failure Modes

| Step | Failure | Trigger |
|------|---------|---------|
| 1 | Vault sealed | Vault restarted between stages |
| 4 | Docker Compose deploy fails | Resource limits, image pull failure |
| 5 | Authentik health timeout | Database migration slow, out of memory |
| 8/9 | OIDC provider creation fails | Authentik API error, name conflict |
| 10 | Vault write fails | Bootstrap token expired (TTL 24h) |
| 11 | Vault OIDC config fails | Authentik not yet fully initialized |

### Recovery Paths

- **1a** Vault sealed: halt; instruct operator to unseal Vault manually (`vault
  operator unseal` using keys from `vault-init.json`); then retry
  `bootstrap --stage authentik`
- **5a** Health timeout: dump Authentik container logs to log file; emit path;
  `[VERIFY]` retry or abort
- **10a** Bootstrap token expired: operator must run `bootstrap --stage vault`
  to refresh the bootstrap token, then retry Stage 4
- **General retry**: `bootstrap --stage authentik` re-runs idempotently

### Handoff Contract

Produces:
- Authentik running at `https://authentik.home.<domain>`
- Groups `adults` and `kids` created
- OIDC providers for Vault and GitLab created; credentials in Vault at
  `secret/bootstrap/authentik/oidc/*`
- Vault OIDC auth method fully configured with Authentik as provider
- Authentik applications created for Vault and GitLab

### Observable States

| State | TUI | Log |
|-------|-----|-----|
| Vault check | `Authentik: verifying Vault is unsealed...` | `[INFO] stage=authentik action=vault-seal-check` |
| Deploying | `Authentik: deploying...` | `[INFO] stage=authentik action=deploy` |
| Waiting | `Authentik: waiting for startup (Xs)...` | `[INFO] stage=authentik startup-wait elapsed=Xs` |
| Configuring groups | `Authentik: creating groups...` | `[INFO] stage=authentik action=create-groups` |
| Configuring OIDC | `Authentik: registering OIDC clients...` | `[INFO] stage=authentik action=create-oidc` |
| Writing to Vault | `Authentik: storing credentials in Vault...` | `[INFO] stage=authentik action=vault-write` |
| Complete | `Stage 4: Authentik — complete` | `[INFO] stage=authentik status=complete` |
| Error | Red error panel; `see: <log path>:<line>` | `[ERROR] stage=authentik error=<msg>` |

---

## Stage 5: PostgreSQL

### Prerequisites

- Stage 3 (Vault): unsealed; bootstrap token valid
- Stage 4 (Authentik): Authentik running (will be migrated to shared PostgreSQL
  at end of this stage)
- Port 5432 available on target (or on a dedicated DB host if configured)
- Target has sufficient disk space (estimate: 20 GB minimum)

### Happy Path

1. `[AUTO]` Check Vault seal state:
   - `GET /v1/sys/seal-status`
   - If sealed: halt with TUI error; instruct operator to unseal
   - If unsealed: log `[INFO] postgres-preflight: vault unsealed`

2. `[AUTO]` Check if PostgreSQL is already running:
   - TCP port check: `<target>:5432`
   - If open: attempt `psql` connection with admin credentials from Vault
   - If responding: jump to step 5 (database idempotency check)
   - If not: continue

3. `[AUTO]` Generate PostgreSQL admin password if not in Vault:
   - Check `secret/bootstrap/postgres/admin`
   - If missing: generate random password; write to Vault
   - Log: `[INFO] postgres-creds: admin credentials ready`

4. `[AUTO]` Run Ansible role `postgresql_install`:
   - Install PostgreSQL 16 (or latest stable for target arch)
   - Configure `postgresql.conf`: `listen_addresses = '*'`
   - Configure `pg_hba.conf`: allow connections from bootstrap server IP; reject
     all others
   - Set admin password (from Vault, injected via env var)
   - Start and enable `postgresql` systemd service
   - Log: `[INFO] postgres-install: ansible role started`

5. `[AUTO]` Create per-service databases and users (idempotent — check before
   create):
   - `authentik` database + `authentik` user; credentials generated and written
     to Vault at `secret/bootstrap/postgres/authentik`
   - `gitlab` database + `gitlab` user; credentials at
     `secret/bootstrap/postgres/gitlab`
   - `maas` database + `maas` user; credentials at
     `secret/bootstrap/postgres/maas`
   - Log per db: `[INFO] postgres-db: created name=<db>`

6. `[AUTO]` Verify connectivity to each database from the target host:
   - `psql -U <user> -d <db> -c '\q'` for each service user
   - Log per: `[INFO] postgres-verify: db=<db> status=ok`

7. `[AUTO]` Migrate Authentik to shared PostgreSQL:
   - Stop Authentik service
   - Export data from bundled PostgreSQL (pg_dump)
   - Update Authentik config with new PostgreSQL DSN (DSN pulled from Vault)
   - Import dump into shared PostgreSQL `authentik` database
   - Restart Authentik
   - Poll Authentik health endpoint until healthy (timeout 300s)
   - Log: `[INFO] authentik-migration: migrated to shared postgres`
   - TUI: `PostgreSQL: migrating Authentik to shared database...`

8. `[AUTO]` Verify Authentik health after migration:
   - `GET https://authentik.home.<domain>/api/v3/root/config/`
   - Confirm response 200
   - Log: `[INFO] authentik-migration: health check passed`

### Branch Conditions

- PostgreSQL already running → skip install; check databases (idempotency path)
- Authentik already using shared PostgreSQL (detected by DSN in Authentik
  config matching shared instance) → skip migration step
- PostgreSQL on separate host → Ansible role targets different inventory host;
  connection checks adjusted accordingly

### Failure Modes

| Step | Failure | Trigger |
|------|---------|---------|
| 4 | PostgreSQL install fails | Package repo unreachable, disk space |
| 4 | Service fails to start | Config error in `postgresql.conf` |
| 5 | User/DB creation fails | Permission error, existing conflicting objects |
| 7 | Authentik migration fails | pg_dump error, import conflict |
| 8 | Authentik unhealthy after migration | Migration data corruption |

### Recovery Paths

- **7a** Authentik migration fails mid-import: Authentik is stopped; original
  bundled database is still intact; rollback: revert Authentik config to
  bundled DSN; restart Authentik; resolve issue; retry `bootstrap --stage postgres`
- **8a** Authentik unhealthy post-migration: emit error to TUI; dump Authentik
  logs; `[VERIFY]` prompt: rollback to bundled DB or retry migration
- **General retry**: `bootstrap --stage postgres` re-runs idempotently; checks
  all resources before creating

### Handoff Contract

Produces:
- PostgreSQL running on `<target>:5432`
- Databases: `authentik`, `gitlab`, `maas` — each with dedicated user
- All credentials in Vault under `secret/bootstrap/postgres/*`
- Authentik migrated to shared PostgreSQL and healthy

### Observable States

| State | TUI | Log |
|-------|-----|-----|
| Checking | `PostgreSQL: checking for existing instance...` | `[INFO] stage=postgres action=pre-check` |
| Installing | `PostgreSQL: installing...` | `[INFO] stage=postgres action=install` |
| Creating databases | `PostgreSQL: creating service databases...` | `[INFO] stage=postgres action=create-db name=<db>` |
| Migrating Authentik | `PostgreSQL: migrating Authentik to shared DB...` | `[INFO] stage=postgres action=authentik-migration` |
| Verifying | `PostgreSQL: verifying connectivity...` | `[INFO] stage=postgres action=verify` |
| Complete | `Stage 5: PostgreSQL — complete` | `[INFO] stage=postgres status=complete` |
| Error | Red error panel; `see: <log path>:<line>` | `[ERROR] stage=postgres error=<msg>` |

---

## Stage 6: GitLab + Runner

### Prerequisites

- Stage 1 (DNS): `gitlab.home.<domain>` resolves
- Stage 2 (TLS): wildcard cert in Vault
- Stage 3 (Vault): unsealed; bootstrap token valid; Authentik OIDC creds for
  GitLab at `secret/bootstrap/authentik/oidc/gitlab`
- Stage 5 (PostgreSQL): `gitlab` database and user exist with credentials in
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

7. `[AUTO]` Configure GitLab initial settings via API:
   - Set admin password (from Vault)
   - Disable public signup
   - Enable OIDC sign-in via Authentik provider (config already in gitlab.rb;
     verify via API)
   - Create `infrastructure` and `services` top-level groups
   - Create `infrastructure/iac` project (initial Terraform state repository)
   - Log per action: `[INFO] gitlab-config: action=<action> status=ok`

8. `[AUTO]` Configure GitLab Terraform HTTP backend:
   - Create project-level Terraform state: `infrastructure/iac`
   - Generate project access token scoped to `api`; write to Vault at
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

10. `[AUTO]` Configure Vault integration with GitLab:
    - Enable Vault JWT auth method for GitLab CI token injection
    - Write Vault role: `gitlab-ci` — allows reading `secret/services/*` and
      `secret/infrastructure/*` with TTL `1h`
    - Log: `[INFO] vault-gitlab: jwt auth configured for gitlab ci`

11. `[AUTO]` Smoke test GitLab OIDC login flow:
    - Fetch OIDC discovery document from Authentik for GitLab provider
    - Verify Authentik application redirect URIs match GitLab callback URL
    - Log: `[INFO] gitlab-oidc: callback uri verified`

12. `[AUTO]` Create initial Terraform workspace for bootstrap state migration
    (see Terraform State Migration flow below):
    - Write `terraform/backend.tf.example` to `infrastructure/iac` repo
    - Log: `[INFO] gitlab-tf: initial workspace ready`

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
- Stage 5 (PostgreSQL): `maas` database and user in Vault at
  `secret/bootstrap/postgres/maas`
- Stage 6 (GitLab): running (MAAS Terraform provider will target GitLab state
  backend)
- Operator has configured lab VLAN on Unifi switch; VLAN is routed to bootstrap
  server
- Port 5240 (MAAS HTTP), 5241 (MAAS HTTPS) available on target
- Lab VLAN DHCP is NOT yet active (MAAS will own DHCP on that segment)

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

6. `[AUTO]` Configure MAAS via CLI (`maas login` + API):
   - Create admin API key; log in via `maas login bootstrap-admin ...`
   - Set MAAS URL: `http://maas.home.<domain>:5240/MAAS`
   - Set upstream DNS to Technitium DNS IP
   - Set MAAS domain to `maas.home.<domain>`
   - Configure NTP server
   - Log: `[INFO] maas-config: base configuration applied`

7. `[AUTO]` Configure lab VLAN fabric and subnet in MAAS:
   - Define fabric for lab VLAN (VLAN ID from `config.yaml`)
   - Define subnet: `config.lab_vlan_subnet` (e.g. `10.10.10.0/24`)
   - Enable DHCP on the lab VLAN with MAAS as DHCP server
   - Reserve IP range for static assignments
   - Log: `[INFO] maas-network: lab vlan configured; dhcp enabled`
   - TUI: `MAAS: configuring lab VLAN DHCP...`

8. `[AUTO]` Import Ubuntu OS images for PXE boot:
   - Import Ubuntu 22.04 and 24.04 images (amd64 and arm64)
   - This is async — MAAS downloads images in background
   - Log: `[INFO] maas-images: image import started (background)`
   - TUI: `MAAS: importing OS images (background — may take several minutes)...`

9. `[AUTO]` Generate MAAS API key for Terraform MAAS provider:
   - Create API key scoped to MAAS admin
   - Write to Vault at `secret/bootstrap/maas/terraform-api-key`
   - Log: `[INFO] maas-terraform: api key written to vault`

10. `[AUTO]` Add initial Technitium DNS A record for MAAS (idempotent — may
    already exist from Stage 1):
    - `maas.home.<domain>` → `<bootstrap_server_ip>`
    - Log: `[INFO] dns-record: maas.home.<domain> verified`

### Branch Conditions

- MAAS already running → skip install; verify network config and DHCP
- OS images already imported → skip image import
- Lab VLAN DHCP already active on MAAS → skip DHCP configuration

### Failure Modes

| Step | Failure | Trigger |
|------|---------|---------|
| 4 | MAAS snap install fails | Network, snap store unreachable |
| 4 | Database init fails | Wrong DSN, PostgreSQL not accepting connections |
| 5 | Health timeout | MAAS rack controller not registering |
| 7 | DHCP config fails | VLAN not present, subnet conflict |
| 8 | Image import fails | No internet, wrong arch |

### Recovery Paths

- **4a** Database init failure: check PostgreSQL connectivity from target;
  verify `maas` db user has correct permissions; retry `bootstrap --stage maas`
- **7a** VLAN/DHCP failure: check Unifi VLAN configuration; verify lab VLAN
  is trunked to bootstrap server interface; `[VERIFY]` prompt to retry
- **8a** Image import fails: non-blocking — MAAS still functional; images can
  be re-imported after bootstrap; emit warning to TUI, not error

### Handoff Contract

Produces:
- MAAS running at `http://maas.home.<domain>:5240/MAAS`
- Lab VLAN DHCP active (MAAS-managed)
- Ubuntu 22.04 and 24.04 images importing (background)
- MAAS Terraform API key in Vault at `secret/bootstrap/maas/terraform-api-key`

### Observable States

| State | TUI | Log |
|-------|-----|-----|
| Checking | `MAAS: checking for existing instance...` | `[INFO] stage=maas action=pre-check` |
| Installing | `MAAS: installing...` | `[INFO] stage=maas action=install` |
| Configuring | `MAAS: configuring network and DHCP...` | `[INFO] stage=maas action=configure-network` |
| Importing images | `MAAS: importing OS images (background)...` | `[INFO] stage=maas action=image-import status=background` |
| Complete | `Stage 7: MAAS — complete` | `[INFO] stage=maas status=complete` |
| Error | Red error panel; `see: <log path>:<line>` | `[ERROR] stage=maas error=<msg>` |

---

## Stage 8: k3s Clusters

### Prerequisites

- Stage 7 (MAAS): running; lab VLAN DHCP active; OS images imported
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
   2b. `[AUTO]` Update `backend.tf` to point to GitLab HTTP backend:
       ```
       terraform {
         backend "http" {
           address = "https://gitlab.home.<domain>/api/v4/projects/<id>/terraform/state/<workspace>"
           username = "bootstrap"
           password = "<token>"
         }
       }
       ```
   2c. `[AUTO]` Run `terraform init -migrate-state` in each directory
   2d. `[AUTO]` Verify state file is now readable from GitLab HTTP backend
   2e. `[AUTO]` Delete local `*.tfstate` file after verified migration
   2f. `[AUTO]` Log: `[INFO] tf-migration: state=<name> migrated to gitlab backend`

3. `[VERIFY]` Confirm with operator:
   `All local Terraform state has been migrated to GitLab.
    Local .tfstate files have been removed.
    GitLab HTTP backend is now the source of truth.
    Proceed? [y/N]`

4. `[AUTO]` Write completion marker to `./logs/run-<timestamp>/bootstrap-complete.json`:
   - Fields: timestamp, all stage completion times, GitLab URL, Vault URL
   - Log: `[INFO] bootstrap: COMPLETE; all stages done; state migrated to gitlab`

5. `[AUTO]` TUI displays bootstrap completion summary:
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

2. **Credential injection**: Cloudflare token, PostgreSQL passwords, GitLab
   tokens, and Authentik secrets are always injected as environment variables
   at runtime. They are never written to log files, never displayed in TUI
   plaintext, and never written to `config.yaml`.

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
