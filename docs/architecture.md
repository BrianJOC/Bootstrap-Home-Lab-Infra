# System Architecture: bootstrap-home-infra

> Produced by: software-architect
> Sources: `docs/prd.md`, `docs/workflow.md`, `docs/specs.md`
> Last updated: 2026-03-19
> Status: Ready for implementation

---

## 1. Repository Layout

```
bootstrap-home-infra/
├── cmd/
│   └── bootstrap/
│       └── main.go                  # Binary entry point; wires config, TUI/CLI mode, stage runner
│
├── internal/
│   ├── config/
│   │   ├── config.go                # Config struct definitions; YAML + env loading; validation
│   │   ├── loader.go                # config.yaml reader; .env loader; merge precedence
│   │   └── validate.go              # Field validators (IP, CIDR, domain, password strength)
│   ├── runner/
│   │   ├── runner.go                # StageRunner interface; orchestrator; stage sequencing
│   │   ├── stage0_config.go         # Stage 0: config collection and SSH validation
│   │   ├── stage1_dns.go            # Stage 1: Technitium DNS
│   │   ├── stage2_tls.go            # Stage 2: Cloudflare DNS-01 cert issuance
│   │   ├── stage3_vault.go          # Stage 3: Vault install, init, unseal, configure
│   │   ├── stage4_postgres.go       # Stage 4: PostgreSQL and service databases
│   │   ├── stage5_authentik.go      # Stage 5: Authentik deploy and OIDC wiring
│   │   ├── stage6_gitlab.go         # Stage 6: GitLab CE + Runner + Vault JWT
│   │   ├── stage7_maas.go           # Stage 7: MAAS install; optional DHCP cutover
│   │   ├── stage8_k3s.go            # Stage 8: k3s cluster install and Terraform trigger
│   │   └── migrate_state.go         # Terraform state migration to GitLab HTTP backend
│   ├── ssh/
│   │   ├── client.go                # SSH connection management; exec; file transfer
│   │   └── keyscan.go               # Host key verification; arch/OS detection helpers
│   ├── ansible/
│   │   └── runner.go                # Ansible playbook invocation via SSH; output streaming
│   ├── terraform/
│   │   └── runner.go                # terraform init/plan/apply invocation; state migration
│   ├── vault/
│   │   └── client.go                # Vault HTTP client; seal check; KV read/write
│   ├── tui/
│   │   ├── model.go                 # Root Bubbletea model; layout composition
│   │   ├── progress.go              # Stage progress pane component
│   │   ├── logpane.go               # Live log tail pane component
│   │   ├── errorpanel.go            # Error panel with log file path reference
│   │   ├── form.go                  # Input form component (masked fields, validation)
│   │   ├── confirm.go               # [VERIFY] prompt component
│   │   └── styles.go                # Lipgloss style definitions (colors, borders, widths)
│   ├── logging/
│   │   ├── logger.go                # Structured logger; writes to file + channel for TUI pane
│   │   └── rotate.go                # Log file rotation by run timestamp
│   └── runcontext/
│       └── context.go               # In-memory run context: bootstrap token, arch, run manifest
│
├── ansible/
│   ├── site.yml                     # Top-level playbook; imports stage playbooks
│   ├── inventory/
│   │   ├── hosts.yml                # Dynamic inventory; target host set at runtime
│   │   └── group_vars/
│   │       └── all.yml              # Non-secret group vars (paths, service names, ports)
│   └── roles/
│       ├── technitium_dns/
│       │   ├── tasks/
│       │   │   ├── main.yml         # Entry point; includes subtask files
│       │   │   ├── docker.yml       # Install Docker (arch-aware)
│       │   │   ├── deploy.yml       # Deploy Technitium container
│       │   │   └── configure.yml    # Zone and record creation via API
│       │   ├── handlers/
│       │   │   └── main.yml         # restart-technitium, reload-resolver
│       │   ├── defaults/
│       │   │   └── main.yml         # technitium_port, technitium_api_port, bind_ip
│       │   ├── vars/
│       │   │   └── main.yml         # Internal role vars; no secrets
│       │   └── templates/
│       │       └── resolv.conf.j2   # systemd-resolved or resolv.conf template
│       ├── tls_lego/
│       │   ├── tasks/
│       │   │   ├── main.yml
│       │   │   ├── deploy.yml       # Transfer lego binary for target arch
│       │   │   └── issue.yml        # Run DNS-01 challenge; collect cert
│       │   ├── defaults/
│       │   │   └── main.yml         # acme_server_url, cloudflare_api_base_url, cert_dir
│       │   └── templates/
│       │       └── lego-env.j2      # Environment file for lego invocation (vars only, no values)
│       ├── vault_install/
│       │   ├── tasks/
│       │   │   ├── main.yml
│       │   │   ├── install.yml      # Binary install for target arch
│       │   │   ├── configure.yml    # Write vault.hcl; deploy TLS certs
│       │   │   └── service.yml      # systemd unit; start; health check
│       │   ├── handlers/
│       │   │   └── main.yml         # restart-vault
│       │   ├── defaults/
│       │   │   └── main.yml         # vault_port, vault_config_dir, vault_tls_dir
│       │   └── templates/
│       │       └── vault.hcl.j2     # Vault config: listener, storage raft, api_addr
│       ├── authentik_install/
│       │   ├── tasks/
│       │   │   ├── main.yml
│       │   │   ├── deploy.yml       # Docker Compose up; inject env vars
│       │   │   └── configure.yml    # Groups, OIDC providers via API (called from runner)
│       │   ├── handlers/
│       │   │   └── main.yml         # restart-authentik
│       │   ├── defaults/
│       │   │   └── main.yml         # authentik_http_port, authentik_https_port
│       │   └── templates/
│       │       └── docker-compose.yml.j2  # Authentik compose file; env var references only
│       ├── postgresql_install/
│       │   ├── tasks/
│       │   │   ├── main.yml
│       │   │   ├── install.yml      # Install PostgreSQL 16 (arch-aware)
│       │   │   ├── configure.yml    # postgresql.conf, pg_hba.conf
│       │   │   └── databases.yml    # Create DBs and users (called from runner)
│       │   ├── handlers/
│       │   │   └── main.yml         # restart-postgresql
│       │   ├── defaults/
│       │   │   └── main.yml         # pg_port, pg_version, pg_data_dir
│       │   └── templates/
│       │       ├── postgresql.conf.j2
│       │       └── pg_hba.conf.j2
│       ├── gitlab_install/
│       │   ├── tasks/
│       │   │   ├── main.yml
│       │   │   ├── install.yml      # Omnibus package for target arch
│       │   │   ├── configure.yml    # Render gitlab.rb; gitlab-ctl reconfigure
│       │   │   └── runner.yml       # Install and register gitlab-runner
│       │   ├── handlers/
│       │   │   └── main.yml         # gitlab-ctl-reconfigure, restart-runner
│       │   ├── defaults/
│       │   │   └── main.yml         # gitlab_external_url, gitlab_edition
│       │   └── templates/
│       │       └── gitlab.rb.j2     # GitLab Omnibus config; all secrets as env var refs
│       ├── maas_install/
│       │   ├── tasks/
│       │   │   ├── main.yml
│       │   │   ├── install.yml      # MAAS snap or deb for target arch
│       │   │   ├── init.yml         # maas init region+rack; DSN from env
│       │   │   └── network.yml      # Host/interface readiness; no durable MAAS API objects
│       │   ├── handlers/
│       │   │   └── main.yml         # restart-maas
│       │   └── defaults/
│       │       └── main.yml         # maas_http_port, maas_url
│       └── k3s_install/
│           ├── tasks/
│           │   ├── main.yml
│           │   ├── control_plane.yml  # Install k3s server on control-plane nodes
│           │   └── worker.yml         # Install k3s agent; join token from Vault
│           ├── handlers/
│           │   └── main.yml           # restart-k3s
│           └── defaults/
│               └── main.yml           # k3s_version, k3s_config_dir
│
├── terraform/
│   ├── environments/
│   │   ├── bootstrap/               # Local state during bootstrap; migrated post-bootstrap
│   │   │   ├── backend.tf           # Initially: local backend; replaced during migration
│   │   │   ├── main.tf              # Invokes modules needed during bootstrap
│   │   │   └── variables.tf
│   │   └── post-bootstrap/          # Template for GitLab HTTP backend; not active until migration
│   │       └── backend.tf.example   # Committed to infrastructure/iac; shows HTTP backend form
│   └── modules/
│       ├── dns-records/             # Technitium DNS record management
│       │   ├── main.tf
│       │   ├── variables.tf
│       │   └── outputs.tf
│       ├── vault-config/            # Vault policies, auth methods, secrets engines
│       │   ├── main.tf
│       │   ├── variables.tf
│       │   └── outputs.tf
│       ├── authentik-config/        # Authentik groups, OIDC providers, applications
│       │   ├── main.tf
│       │   ├── variables.tf
│       │   └── outputs.tf
│       ├── gitlab-config/           # GitLab groups, projects, Runner, Terraform backend
│       │   ├── main.tf
│       │   ├── variables.tf
│       │   └── outputs.tf
│       ├── maas-config/             # MAAS machines, networks, optional approved DHCP
│       │   ├── main.tf
│       │   ├── variables.tf
│       │   └── outputs.tf
│       ├── k3s-cluster/             # k3s cluster registration as named Terraform target
│       │   ├── main.tf
│       │   ├── variables.tf
│       │   └── outputs.tf
│       └── helm-release/            # Generic Helm chart deployment to named k3s cluster
│           ├── main.tf
│           ├── variables.tf
│           └── outputs.tf
│
├── test/
│   ├── images/
│   │   └── base.pkr.hcl             # Packer HCL; builds qcow2 for x86 and ARM
│   ├── keys/
│   │   ├── test_id_ed25519          # Test private key (test-only; never used outside QEMU VMs)
│   │   └── test_id_ed25519.pub      # Corresponding public key; embedded in Packer image
│   ├── mocks/
│   │   ├── cloudflare/
│   │   │   ├── server.go            # HTTP stub: list-zones, create-TXT-record, delete-TXT-record
│   │   │   └── Dockerfile
│   │   └── pebble/
│   │       ├── pebble-config.json   # Pebble ACME server config; points at challtestsrv
│   │       └── docker-compose.yml   # Pebble + challtestsrv side-car compose file
│   ├── molecule/                    # Molecule scenarios for each Ansible role
│   │   └── technitium_dns/
│   │       ├── molecule.yml
│   │       └── verify.yml
│   └── harness/
│       └── stage_test.go            # Go test harness for just test-stage <name>
│
├── docs/
│   ├── prd.md
│   ├── workflow.md
│   ├── specs.md
│   └── architecture.md              # This document
│
├── config.yaml.example              # Template showing all required structural fields; safe to commit
├── .env.example                     # Template showing all required secret env vars; safe to commit
├── justfile                         # Task runner targets
└── .gitignore                       # Excludes: config.yaml, .env, vault-init.json, certs/, logs/, *.tfstate
```

---

## 2. Go Binary Architecture

### 2.1 Package Breakdown

| Package | Responsibility |
|---------|----------------|
| `cmd/bootstrap` | Entry point only. Parses top-level flags (`--target`, `--stage`, `--headless`, `--test`, `--migrate-state`). Initializes config, logging, TUI or CLI mode, then hands off to `runner`. |
| `internal/config` | Defines `Config` and `Secrets` structs. Loads `config.yaml` into `Config`; loads `.env` into process environment and reflects values into `Secrets`. Validates all fields. Never logs secret values. |
| `internal/runner` | Defines the `Stage` interface. Implements the orchestrator that sequences stages 0–8 and the state migration flow. Each stage file implements `Stage` and holds all logic for that stage. The orchestrator injects the `RunContext` (in-memory state) into each stage. |
| `internal/ssh` | Manages a persistent SSH connection to the target host. Exposes `Exec(cmd string) (stdout, stderr string, err error)` and `Copy(local, remote string) error`. Handles arch/OS detection. |
| `internal/ansible` | Wraps SSH to invoke `ansible-playbook`. Streams stdout to the logging channel so the TUI log pane updates in real time. Returns structured result (changed count, failed tasks). |
| `internal/terraform` | Wraps local `terraform` binary invocation. Handles `init`, `plan`, `apply`, and the `init -migrate-state` migration path. Captures output for TUI display. |
| `internal/vault` | Thin HTTP client for the Vault API. All methods accept a token parameter. Used by stage runners for seal checks, KV reads/writes, auth method configuration. |
| `internal/tui` | Bubbletea root model and pane components. Receives updates via a channel from the logging system. Never blocks the runner goroutine. |
| `internal/logging` | Structured JSON logger writing to `logs/run-<timestamp>/bootstrap.log`. Also writes log entries to a buffered channel that the TUI log pane reads. Every log entry at ERROR or higher includes the absolute file path and line number. |
| `internal/runcontext` | Holds per-run in-memory state: bootstrap Vault token (never persisted), target arch, run start time, stage completion markers. Passed by pointer through the runner into each stage. |

### 2.2 TUI Component Design

The TUI is a three-pane Bubbletea layout using Lipgloss for styling. It is active only during bootstrap; post-bootstrap interaction is pure CLI.

```
┌─────────────────────────────────────────────────────────────┐
│  bootstrap-home-infra v<version>   target: <ip>             │
├──────────────────────────┬──────────────────────────────────┤
│  STAGE PROGRESS          │  LIVE LOG                        │
│                          │                                  │
│  [✓] 0: Config           │  [INFO] stage=dns action=...     │
│  [✓] 1: DNS              │  [INFO] dns-record: created ...  │
│  [ ] 2: TLS  ← active    │  [INFO] tls-challenge: started  │
│  [ ] 3: Vault            │  [INFO] tls-challenge: waiting  │
│  [ ] 4: Authentik        │                                  │
│  [ ] 5: PostgreSQL       │  (auto-scrolling; last N lines)  │
│  [ ] 6: GitLab           │                                  │
│  [ ] 7: MAAS             │                                  │
│  [ ] 8: k3s              │                                  │
├──────────────────────────┴──────────────────────────────────┤
│  ERROR PANEL (hidden when no error)                         │
│  [ERROR] tls-challenge: auth failure                        │
│  see: /…/logs/run-20260319T140000/bootstrap.log:142         │
└─────────────────────────────────────────────────────────────┘
```

**Bubbletea model structure:**

The root model in `internal/tui/model.go` holds:
- `stages []StageStatus` — name, state (pending/running/done/error) for each stage
- `logLines []string` — rolling window of the last N log lines
- `errorMsg string` — current error panel content (empty = panel hidden)
- `logFilePath string` — absolute path of the active log file
- `activeForm *FormModel` — non-nil when a `[PROMPT]` is active
- `confirmModel *ConfirmModel` — non-nil when a `[VERIFY]` is active

Updates arrive as Bubbletea messages sent from the runner goroutine over a channel. The TUI never calls into stage logic; it only reacts to messages.

**Lipgloss styling approach** (`internal/tui/styles.go`):
- Progress pane border: `lipgloss.RoundedBorder()`; fixed width `28`
- Log pane: no border; takes remaining width; monospace font implied by terminal
- Error panel: `lipgloss.NormalBorder()` with `lipgloss.Color("196")` (red) foreground; hidden via zero height when empty
- Active stage row: bold + `lipgloss.Color("33")` (blue)
- Complete stage row: `lipgloss.Color("40")` (green) checkmark
- Warning box (vault-init.json): `lipgloss.Color("220")` (yellow) border; full width; prominent

### 2.3 CLI Headless Mode

Headless mode is triggered by `--headless`. In this mode:
- The Bubbletea TUI is not initialized
- All output goes to stdout as structured log lines (same format as the log file)
- Any `[PROMPT]` or `[VERIFY]` step that would pause in TUI mode instead reads from stdin (for piped use) or fails hard with a descriptive error if stdin is not a pipe
- `--headless` combined with `--stage <name>` re-runs a single named stage without prompts; requires `config.yaml` and `.env` to be fully populated

The runner interface is identical in both modes. The difference is the sink: TUI messages go to the Bubbletea model in interactive mode; they go to structured log output in headless mode. This is achieved by passing a `Sink` interface into the runner:

```go
// internal/runner/runner.go
type Sink interface {
    StageStarted(name string)
    StageComplete(name string)
    StageFailed(name string, err error, logPath string, logLine int)
    LogLine(level, msg string, fields map[string]string)
    Prompt(label string, masked bool) (string, error)
    Confirm(msg string) (bool, error)
}
```

The TUI sink sends Bubbletea messages. The CLI sink writes to stdout and reads from stdin.

### 2.4 Config Loading

Config loading is a two-source merge:

1. `config.yaml` is read from the working directory and unmarshalled into `Config`. Missing required fields are collected (not immediately fatal — the interactive TUI prompts for them).
2. `.env` is read and each variable is loaded into the process environment via `os.Setenv`. Values are then reflected into `Secrets` struct fields. The `.env` file is read once; its contents are not retained in memory after reflection.

On `--headless`, both files must fully satisfy all required fields or the binary exits with a list of missing fields. No prompts are issued.

`ACME_SERVER_URL` and `CLOUDFLARE_API_BASE_URL` are read from the environment at Stage 2 startup. If unset, the production defaults are used (`https://acme-v02.api.letsencrypt.org/directory` and `https://api.cloudflare.com`). Setting them in the test harness redirects without any code path divergence.

### 2.5 Stage Runner Interface

Every stage implements the following interface:

```go
// internal/runner/runner.go

// Stage is implemented by every bootstrap stage.
type Stage interface {
    // Name returns the canonical stage identifier used in --stage flags and log entries.
    Name() string

    // Run executes the stage. It receives the full config, the in-memory run context,
    // and a Sink for TUI/CLI communication. Run must be idempotent: calling it on a
    // target where the stage has already completed must produce no changes and return nil.
    Run(ctx context.Context, cfg *config.Config, sec *config.Secrets, rc *runcontext.RunContext, sink Sink) error

    // Check returns true if the stage's postconditions are already satisfied.
    // The orchestrator calls Check before Run; if Check returns true, Run is skipped
    // and the stage is marked complete in the progress pane.
    Check(ctx context.Context, cfg *config.Config, rc *runcontext.RunContext) (bool, error)
}
```

The orchestrator in `runner.go` iterates through stages in dependency order (0–8), calls `Check`, then `Run` if needed, then marks the stage complete. The `--stage <name>` flag causes the orchestrator to jump directly to the named stage (config is still loaded first).

### 2.6 SSH Client

`internal/ssh/client.go` wraps `golang.org/x/crypto/ssh`. It:
- Accepts a private key path (from `config.SSHKeyPath`) — key is read once into memory; path is not logged
- Maintains a persistent multiplexed connection to the target host
- Exposes `Exec` (runs a command, returns stdout/stderr) and `Copy` (streams a local file to a remote path)
- `DetectArch()` calls `uname -m` and parses the result into a `TargetArch` enum (`AMD64`, `ARM64`)
- `DetectOS()` calls `lsb_release -is` and `lsb_release -rs` and returns OS name and version

### 2.7 Override Support

`ACME_SERVER_URL` and `CLOUDFLARE_API_BASE_URL` are not special-cased in tests. They are read from the environment at the point of use in Stage 2 — specifically when constructing the lego invocation and when the Ansible `tls_lego` role resolves its `acme_server_url` and `cloudflare_api_base_url` defaults. The Ansible role `defaults/main.yml` for `tls_lego` reads these from extra vars passed by the Go runner, which sources them from the environment. This means the override is active for both the Go-level cert validation logic and the Ansible role level without any conditional compilation or test-mode flag.

### 2.8 Logging

Log entries are written as JSON lines to `logs/run-<timestamp>/bootstrap.log`. Each entry has:
```json
{"ts":"2026-03-19T14:00:00Z","level":"INFO","stage":"dns","msg":"dns-record: created","name":"vault.home.example.com","ip":"192.168.1.10"}
```

For ERROR and higher, entries include `"log_path"` and `"log_line"` fields. The TUI error panel reads these to display `see: <path>:<line>`.

The logger in `internal/logging/logger.go` writes to two sinks: the log file and a buffered `chan LogEntry`. The TUI log pane receives entries from the channel via a Bubbletea `WaitForActivity` command. In headless mode, entries are written to stdout only.

---

## 3. Ansible Role Structure

### Global Conventions

- All roles are invoked by the Go runner via `internal/ansible/runner.go`, which constructs a dynamic inventory from `config.TargetHost` and passes `extra-vars` for all per-run values.
- Secrets are never in `vars/` or `defaults/` files. They arrive as extra-vars from the Go runner, which reads them from the process environment. The Ansible process inherits only the vars it needs; the runner does not export all env vars.
- Every task file begins with an idempotency gate: a `stat`, `uri`, or `command` check that returns a registered variable used in `when:` conditions on subsequent tasks.
- Arch detection uses the `ansible_architecture` fact (`x86_64` or `aarch64`); no custom detection needed.

### 3.1 `technitium_dns`

**Purpose:** Install Docker (if absent), deploy Technitium DNS container, configure DNS zone and A records, configure target resolver.

**Idempotency gate:** TCP port check on `5380` before any install step. Zone existence check before zone creation. Per-record check before each record creation.

**Secret injection:** `BOOTSTRAP_ADMIN_PASSWORD` passed as extra-var `technitium_admin_password` by the Go runner. Never written to any file on disk.

**Key templates:**
- `resolv.conf.j2` — generates either a `systemd-resolved` drop-in or direct `/etc/resolv.conf` depending on `ansible_service_mgr`.

### 3.2 `tls_lego`

**Purpose:** Transfer lego binary for target arch; run DNS-01 challenge; collect cert and key to working directory; install the host-managed renewal timer and atomic deployment service.

**Idempotency gate:** Check cert file existence and expiry (via `openssl x509 -noout -dates`) before running lego. Skip if valid cert present with > 30 days until expiry.

**Secret injection:** During bootstrap, `CF_DNS_API_TOKEN` is passed as an
extra-var and used only in the lego invocation environment block. For day-2
renewal, the zone-scoped token is copied to a root-owned mode `0600` runtime
file and read through lego's `_FILE` support. `ACME_SERVER_URL` and
`CLOUDFLARE_API_BASE_URL` read from extra-vars, defaulting to production
endpoints.

**Renewal lifecycle:** A daily systemd timer checks the configurable renewal
window. The oneshot service locks against concurrent runs, renews into immutable
staging, validates the candidate, atomically changes the active symlink, reloads
consumers sequentially, and rolls back if a health check fails. GitLab and Vault
are not prerequisites; Vault synchronization may remain pending until unseal.
See [certificate renewal and deployment](certificate-renewal.md).

**Arch handling:** `lego_arch` var set from `ansible_architecture`; maps `x86_64` → `amd64`, `aarch64` → `arm64`. Binary downloaded from GitHub releases.

### 3.3 `vault_install`

**Purpose:** Install Vault binary for target arch, write Vault configuration, create systemd unit, copy TLS certs to Vault config path with correct permissions.

**Idempotency gate:** Check Vault binary presence and version. Check systemd unit status. Role does NOT initialize or unseal — that is done by the Go runner via the Vault HTTP API.

**Secret injection:** No secrets in the Ansible role itself. TLS cert and key are files transferred by the Go runner via `ssh.Copy` before the role runs, placed at the paths the Vault config template references.

**Key templates:**
- `vault.hcl.j2`: configures `listener "tcp"` with TLS cert paths, `storage "raft"` with `node_id` and `path`, and `api_addr` / `cluster_addr`.

### 3.4 `authentik_install`

**Purpose:** Deploy Authentik server, worker, and Redis via Docker Compose. Does not configure OIDC or groups — that is done by the Go runner via the Authentik REST API.

**Idempotency gate:** Check container running state via `docker compose ps`. Skip `compose up` if all services are `running`.

**Secret injection:** Admin password passed as extra-var `authentik_admin_password`; injected into the Docker Compose env block via the template. PostgreSQL DSN passed similarly. The compose template uses env var references, not inline values.

**Database contract:** The compose template always receives the final PostgreSQL
DSN created by Stage 4. It does not contain a bundled PostgreSQL service or a
temporary-database conditional.

### 3.5 `postgresql_install`

**Purpose:** Install PostgreSQL 16, configure `postgresql.conf` and `pg_hba.conf`, start and enable the service. Database and user creation is done by the Go runner (not Ansible) to keep credential management centralized.

**Idempotency gate:** Check PostgreSQL service running state. Check PostgreSQL version matches expected. `pg_hba.conf` and `postgresql.conf` are managed with `blockinfile` so re-runs are safe.

**Secret injection:** PostgreSQL admin password passed as extra-var `pg_admin_password`. Set via `ALTER USER postgres PASSWORD '...'` in an Ansible task that uses `no_log: true`.

### 3.6 `gitlab_install`

**Purpose:** Install GitLab CE Omnibus for target arch, render `gitlab.rb`, run `gitlab-ctl reconfigure`. GitLab Runner install and registration is handled in a separate task file within the same role.

**Idempotency gate:** Check `gitlab-ctl status` before reconfigure. Check Runner registration before registration step. Omnibus package version pinned via `gitlab_version` var.

**Secret injection:** PostgreSQL DSN, Authentik OIDC client secret, GitLab secret key base — all passed as extra-vars, used in `gitlab.rb.j2` as env var references. GitLab Omnibus supports reading secrets from environment variables in `gitlab.rb` via the `ENV['VAR']` syntax. The Ansible task sets the environment block on the `command` module for `gitlab-ctl reconfigure`.

**Key templates:**
- `gitlab.rb.j2`: `external_url`, `db_adapter` = `postgresql`, PostgreSQL DSN via `ENV`, TLS cert paths, Authentik OmniAuth provider config, Runner registration disabled at install.

### 3.7 `maas_install`

**Purpose:** Install MAAS via snap (preferred) or deb for target arch, run
`maas init region+rack`, start MAAS services, and validate host/interface
readiness. Terraform owns durable fabric, VLAN, subnet, and range objects. Go
orchestrates the optional, explicitly approved DHCP cutover after MAAS is healthy.

**Idempotency gate:** Check `maas status` before install. Check `snap list maas` for existing installation.

**Secret injection:** PostgreSQL DSN for MAAS passed as extra-var `maas_db_uri`; injected into `maas init` command environment block with `no_log: true` on the task.

### 3.8 `observability_install`

**Purpose:** Install target-local Uptime Kuma, Beszel hub/agent, and the
bootstrap health adapter; generate functional monitors, bounded retention,
resource limits, and optional paging configuration. TrueNAS is never a runtime
target for this role.

**Idempotency gate:** Verify pinned container versions, local state volumes,
Beszel agent registration through a local Unix socket, generated monitor IDs,
effective retention, paging test state, and cgroup limits before changing
anything.

**Secret injection:** Discord, Telegram, ntfy, or generic-webhook credentials
are read from protected mode `0600` files. They never enter structural
configuration, command arguments, logs, dashboards, or generated unit text.

**Resource behavior:** Kuma and Beszel share the configured memory, CPU, and
storage budget. Optional collectors and noncritical Kuma cadence shed first.
Core services are never throttled to preserve telemetry.

---

## 4. Terraform Module Layout

### 4.1 Module Breakdown

| Module | Provider(s) | Purpose |
|--------|-------------|---------|
| `dns-records` | `technitium` (community) | Creates and manages A records in Technitium for a given service hostname. Input: `hostname`, `ip`. Called by the service deploy pipeline. |
| `vault-config` | `hashicorp/vault` | Manages Vault policies, KV paths, auth method configuration, and PKI setup. Used during bootstrap and for post-bootstrap policy updates. |
| `authentik-config` | `goauthentik/authentik` | Manages groups, OIDC providers, and applications. Called during bootstrap Stage 5 and by the service deploy pipeline when `authentik: true`. |
| `gitlab-config` | `gitlabhq/gitlab` | Manages GitLab groups, projects, Runner registration, and project-level CI/CD variables. Used during Stage 6. |
| `maas-config` | `canonical/maas` | Manages MAAS machine records, fabrics, VLANs, subnets, ranges, and the approved DHCP state. DHCP defaults disabled. Used during Stage 7 and by the server adoption MR pipeline. |
| `k3s-cluster` | `hashicorp/kubernetes` | Registers a k3s cluster as a named Terraform target by writing kubeconfig and endpoint to Vault. Used during Stage 8. |
| `helm-release` | `hashicorp/helm` | Deploys a Helm chart to a named k3s cluster using a kubeconfig sourced from Vault. Called by the service deploy pipeline for `target.cluster` services. |

### 4.2 State Strategy: Local During Bootstrap

During bootstrap (Stages 0–8), Terraform runs locally on the operator's machine via `internal/terraform/runner.go`. State is stored in `terraform/environments/bootstrap/*.tfstate` files. The backend in `terraform/environments/bootstrap/backend.tf` uses the `local` backend:

```hcl
terraform {
  backend "local" {
    path = "terraform.tfstate"
  }
}
```

Each logical grouping of resources that will eventually become a separate GitLab HTTP state workspace is a separate directory under `terraform/environments/bootstrap/`. This clean separation makes migration atomic per workspace.

### 4.3 Migration Path to GitLab HTTP Backend

After all stages complete, the Terraform state migration flow in `internal/runner/migrate_state.go` executes. For each `*.tfstate` file under `terraform/environments/bootstrap/`:

1. Read the GitLab project ID and Terraform access token from Vault at `secret/data/bootstrap/gitlab/terraform-token`.
2. Generate a workspace name from the directory name (e.g. `bootstrap-dns`, `bootstrap-vault`).
3. Write a new `backend.tf` pointing to the GitLab HTTP backend:

```hcl
terraform {
  backend "http" {
    address        = "https://gitlab.home.<domain>/api/v4/projects/<id>/terraform/state/<workspace>"
    lock_address   = "https://gitlab.home.<domain>/api/v4/projects/<id>/terraform/state/<workspace>/lock"
    unlock_address = "https://gitlab.home.<domain>/api/v4/projects/<id>/terraform/state/<workspace>/lock"
    username       = "bootstrap"
    password       = var.gitlab_terraform_token
    lock_method    = "POST"
    unlock_method  = "DELETE"
  }
}
```

4. Run `terraform init -migrate-state -input=false` in the directory.
5. Verify: `GET /api/v4/projects/<id>/terraform/state/<workspace>` returns the state JSON.
6. Only if verification succeeds: delete the local `.tfstate` file.
7. If verification fails: do not delete; emit error; `[VERIFY]` prompt to retry or skip.

The token is passed as an environment variable (`TF_VAR_gitlab_terraform_token`) to the `terraform` process; it never appears in `backend.tf` on disk.

### 4.4 `infrastructure/iac` GitLab Project Structure

After bootstrap, all IaC lives in `infrastructure/iac` on the GitLab instance. Its structure mirrors the Terraform module layout:

```
infrastructure/iac/
├── .gitlab-ci.yml            # Pipeline: validate → plan → apply on merge
├── services/                 # Service definition YAMLs (one per service)
├── infrastructure/
│   ├── clusters/             # Cluster definition YAMLs
│   └── nodes/                # Node definition YAMLs
├── terraform/
│   ├── services/             # Terraform root for service deployments
│   │   ├── backend.tf        # GitLab HTTP backend
│   │   ├── main.tf           # Reads service YAMLs; invokes dns-records, helm-release, authentik-config
│   │   └── variables.tf
│   └── infrastructure/       # Terraform root for infrastructure management
│       ├── backend.tf
│       ├── main.tf
│       └── variables.tf
└── backend.tf.example        # Written by bootstrap; shows HTTP backend config format
```

### 4.5 Sensitive Output Handling

Terraform module outputs that contain secrets (tokens, passwords, kubeconfigs) are marked `sensitive = true`. They are written to Vault via the `vault-config` module rather than appearing in state as plaintext. Modules that need a secret from another module read it from Vault at apply time using a `vault_generic_secret` data source, not via `terraform_remote_state` output.

---

## 5. Architecture Decision Records

### ADR-001: Go + Bubbletea + Lipgloss for TUI

**Decision:** The bootstrap tool is a single Go binary using Bubbletea for the interactive TUI and Lipgloss for styling.

**Rationale:** A single compiled binary with no runtime dependencies satisfies the cross-architecture requirement (x86_64 and aarch64) without requiring the operator to install Python, Node, or any language runtime. Go's `GOARCH` cross-compilation produces native binaries for both targets from a single codebase. Bubbletea is a mature Elm-architecture TUI framework with good multi-pane layout support. Lipgloss provides composable, testable styling. Python/Textual would require a Python interpreter on the operator's machine and produces no binary artifact.

**Trade-offs:** Go's TUI ecosystem is narrower than Python's for some widget types (e.g. rich input forms). Building masked-input form fields in Bubbletea requires more code than Textual's built-in widgets. Go compile times add a minor development loop cost compared to Python's interpreted execution.

**Consequences:** All stage logic, SSH operations, and Ansible/Terraform invocations are implemented in Go. Developers contributing to the bootstrap tool need Go familiarity. The binary is built for the operator's machine (not the target host); the target host never runs the Go binary.

---

### ADR-002: `config.yaml` (structural) + `.env` (secrets) split

**Decision:** Non-sensitive structural configuration lives in `config.yaml`. All secrets live in `.env` as environment variable definitions, never as YAML fields.

**Rationale:** Structural config (domain names, IP ranges, VLAN IDs, Vault share counts) benefits from YAML's hierarchical format and is safe to version-control as a template. Secrets should never appear in any file that might be committed, logged, or displayed. The env var model maps directly to how tools like Ansible, lego, and GitLab CI already expect secrets to be injected. A single `.env` file with mode `0600` is a well-understood secret container for operator machines.

**Trade-offs:** Two files to manage instead of one. The operator must ensure `.env` is populated before headless runs. `.env` files have no schema validation unless the binary enforces it explicitly.

**Consequences:** The Go binary must validate `.env` presence and completeness at startup. The `.gitignore` must exclude `.env` and runtime config according to the documented repository policy. Bootstrap inputs are passed to Ansible without appearing in process arguments or logs. ADR-009 permits minimum root-owned runtime copies for core host services; this ADR does not require Vault-exclusive runtime delivery.

---

### ADR-003: Ansible for initial deployment + Terraform for GitOps

**Status:** Amended 2026-07-26. The complete ownership matrix is in [`docs/resource-ownership.md`](resource-ownership.md).

**Decision:** Ownership is based on resource type, not bootstrap phase. Ansible owns host/runtime lifecycle both before and after handoff. Terraform owns durable service API objects from the moment each API is available. Go orchestrates both tools and the state handoff; it does not duplicate their lifecycle logic.

**Rationale:** Ansible excels at packages, files, systemd, Docker, migrations, and imperative operations. Terraform excels at declarative API resources with stable identity. Giving an object to one owner from creation avoids import-heavy handoffs, 409 conflicts, and configuration drift.

**Trade-offs:** Both tools remain part of the day-2 platform. Pipelines that require both must have explicit ordering and postcondition checks. Existing Ansible API-configuration tasks and Terraform `remote-exec` installation logic must be refactored to match the exclusive ownership matrix.

**Consequences:** After bootstrap, GitLab pipelines invoke both Terraform and Ansible according to ownership. Operators do not routinely run either tool directly. A clean bootstrap has no Terraform import step because Terraform creates and tracks all Terraform-owned objects from their first creation.

---

### ADR-004: Raft integrated storage for Vault

**Decision:** Vault uses the integrated Raft storage backend (`storage "raft"`), not an external storage backend (Consul, etcd, or a database).

**Rationale:** Raft integrated storage eliminates a dependency on any external service for Vault. During bootstrap, no external coordination service is running. Raft is production-supported by HashiCorp as of Vault 1.4, handles the single-node homelab topology cleanly, and requires no additional infrastructure. Consul would require its own install and clustering before Vault could start — circular to the bootstrap problem.

**Trade-offs:** Raft integrated storage means Vault cannot be easily clustered beyond three nodes without careful migration. Snapshots for backup require explicit `vault operator raft snapshot save` commands. For a single-server homelab this is acceptable.

**Consequences:** `vault.hcl.j2` configures `storage "raft" { path = "/opt/vault/data" node_id = "<hostname>" }`. Backup procedures must use `vault operator raft snapshot save`. High availability clustering is out of scope for v1.

---

### ADR-005: PostgreSQL precedes Authentik; no bootstrap database migration

**Status:** Amended 2026-07-26. This replaces the earlier bundled-PostgreSQL
migration design. The complete contract is in
[`docs/database-architecture.md`](database-architecture.md).

**Decision:** PostgreSQL is Stage 4. It creates isolated databases and roles for
Authentik, GitLab, and MAAS and records their credentials in Vault. Authentik is
Stage 5 and connects to its final PostgreSQL database on first startup. The
bootstrap path does not create a bundled Authentik database or migrate data.

**Rationale:** PostgreSQL has no dependency on Authentik, while Authentik requires
PostgreSQL. Vault is already available to hold generated database credentials.
Putting the database first expresses the real dependency and removes a
stop-export-import-reconfigure-restart operation from a fresh installation.

**Trade-offs:** Shared PostgreSQL becomes an explicit Authentik prerequisite and
a common host-level dependency. Each service still has an isolated database,
role, credential, and restore target. A permanently separate Authentik database
server would improve fault isolation but is not justified for the initial
single-host homelab.

**Consequences:** The `authentik_install` compose template always requires the
final DSN and contains no bundled PostgreSQL branch. The Stage 4 runner owns
database and role readiness; the Stage 5 runner owns Authentik initialization
and application health. Brownfield Authentik imports are a separate, explicit
future workflow and are never inferred during clean bootstrap.

---

### ADR-006: `ACME_SERVER_URL` + `CLOUDFLARE_API_BASE_URL` as first-class env overrides

**Decision:** The ACME server URL (Let's Encrypt endpoint) and Cloudflare API base URL are read from environment variables at the point of use, with production defaults. Setting them in the test harness redirects all certificate issuance and DNS API calls to local test doubles (Pebble and a stub HTTP server) without any conditional code paths.

**Rationale:** Retrofitting test overrides after the fact is consistently painful and leads to test-mode code paths that diverge from production paths. Making these overrides first-class means the production code path and test code path are identical — only the URL changes. This satisfies the global invariant that tests never call real external APIs.

**Trade-offs:** Two extra env vars that operators might accidentally set in their real `.env`. Mitigated by documenting them as test-only in `.env.example` with a clear comment.

**Consequences:** The `tls_lego` Ansible role reads `acme_server_url` and `cloudflare_api_base_url` from extra-vars passed by the Go runner, which sources them from the environment. The Go runner's cert validation logic (parsing the issuer) must accept any CA when `ACME_SERVER_URL` is overridden, not just Let's Encrypt. The Pebble container config in `test/mocks/pebble/` must be kept in sync with the domain used in test runs.

---

### ADR-007: Local Terraform state during bootstrap → GitLab HTTP backend post-bootstrap

**Status:** Amended 2026-07-26. Approval and migration behavior is defined in [`docs/resource-ownership.md`](resource-ownership.md).

**Decision:** Stage-aligned Terraform roots use local backends during bootstrap. After all stages complete, `terraform init -migrate-state` migrates each state to a distinct GitLab HTTP state name. Local `.tfstate` files are deleted only after GitLab returns a state with matching lineage and serial and the operator confirms the handoff. This migration is the SC-14 completion condition.

**Rationale:** The GitLab HTTP backend cannot exist before GitLab is running. Running Terraform with a local backend during bootstrap avoids the circular dependency. The migration procedure is a well-defined `terraform init -migrate-state` operation with explicit verification and non-destructive rollback (local file not deleted until confirmed).

**Trade-offs:** Local `.tfstate` files on the operator's machine are a temporary source of truth. If the operator's machine is lost between bootstrap completion and state migration, those state files are lost. Mitigated by: (1) bootstrap completes quickly (single session), (2) migration runs immediately after final stage, (3) local files are in the working directory that the operator chose.

**Consequences:** `terraform/environments/bootstrap/` contains separate roots for DNS, Vault, Authentik, GitLab, and MAAS. The migration runner handles each independently: a failure in one must not delete or invalidate any local fallback. Interactive runs display and require approval of saved plans. Headless mutating applies require the explicit `--approve-terraform` flag. The backend credential is stored in Vault and supplied through the HTTP backend environment variables, never written into `backend.tf`.

---

### ADR-008: k3s cluster as named IaC target; node provisioner decoupled from cluster definition

**Decision:** A k3s cluster is a named first-class IaC object defined in `infrastructure/clusters/<name>.yaml`. The cluster definition references nodes by name. How those nodes are provisioned (MAAS bare metal, Proxmox VM, Proxmox LXC, or any other method) is defined separately in `infrastructure/nodes/<name>.yaml` and is completely opaque to the cluster definition and to Stage 8.

**Rationale:** Coupling cluster definition to provisioner type would require a new cluster schema variant for every provisioner. Decoupling means the cluster topology (which nodes, which roles) is independent of the infrastructure procurement path. Stage 8 only requires SSH reachability of each node; it does not care how the node was provisioned. This matches the PRD's explicit statement that "the provisioning method is an implementation detail of the node — not of the cluster or the service deployed to it."

**Trade-offs:** Two files per node to maintain: the node definition and whatever the provisioner needs. The indirection through node names requires that node names are stable identifiers used consistently across the cluster definition, the node definition, and the Ansible dynamic inventory.

**Consequences:** Stage 8 resolves node names to IPs by reading node definitions and querying the appropriate provisioner API (MAAS API for MAAS nodes, Proxmox API for Proxmox nodes) at runtime. If a node is not SSH-reachable regardless of provisioner state, Stage 8 warns and skips that node without failing the stage. The Terraform `k3s-cluster` module does not import provisioner-specific resources; it only registers the cluster endpoint and kubeconfig.

---

### ADR-009: Pragmatic v1 secret delivery with manual Vault recovery

**Status:** Accepted 2026-07-26.

**Context:** The product targets one administrator using one trusted laptop to bootstrap an initial homelab. Requiring every core service to fetch its secrets from Vault at startup would make a sealed Vault part of the critical boot path. Adding SPIFFE/SPIRE would introduce a second trust plane before the platform has a multi-node workload-identity requirement.

**Decision:** Vault is the authoritative record for platform secrets, but v1 core host services may retain the minimum local runtime copies required to start and recover. Those copies must be root-owned, mode `0600`, excluded from logs and version control, and reconciled from the authoritative value. Manual Shamir unseal remains the v1 default. The operator selects a `vault-init.json` path outside the repository and is instructed to back it up, typically in a password manager. `.env` and `~/.bootstrap` are preserved by default for safe re-runs; removal is only through an explicit cleanup flow that never deletes `vault-init.json`.

**Delivery tiers:**

1. Bootstrap and core host services use protected local runtime copies where necessary.
2. Vault Agent is an optional post-v1 enhancement for selected host services. If used, templates should render into `/run` or another ephemeral location and service ordering must account for a sealed Vault.
3. Future k3s workloads should prefer Kubernetes authentication with Vault Agent Injector and ephemeral pod-local secret rendering.
4. SPIFFE/SPIRE is deferred until multiple nodes/clusters, workload mTLS, or cross-platform workload identity makes another identity plane worthwhile.

**Consequences:** Documentation must not claim that secret bytes exist only in Vault or never appear in protected runtime files. Root compromise of the host can expose service runtime credentials; this is accepted for the single-host v1 threat model. Rotation must update Vault first, reconcile required local copies atomically, and restart or reload affected services. Automatic unseal remains a future optional profile, not a v1 dependency.

---

### ADR-010: Trust on first use with pinned SSH host identity

**Status:** Accepted 2026-07-26. The complete interaction contract is in [`docs/ssh-host-trust.md`](ssh-host-trust.md).

**Decision:** Interactive bootstrap displays and confirms an unknown SSH host fingerprint, then pins it in a bootstrap-owned known-hosts file shared by Go and Ansible. A changed key stops before authentication, displays old and new fingerprints, and offers an in-workflow replacement only after the operator confirms an intentional reinstall/replacement by typing the target. Headless mode cannot silently replace a pin; replacement requires an explicit flag plus the expected new fingerprint.

**Rationale:** A new homelab server often has no existing trust entry, so strict pre-provisioning alone is poor onboarding. Permanently disabling verification exposes bootstrap credentials to impersonation. Trust on first use with visible fingerprints, durable pinning, and a deliberate changed-key path balances usability and identity continuity.

**Consequences:** Go and Ansible must use the same strict trust store. Production configuration may not set `host_key_checking = False`. Reinstall recovery is handled inside the TUI and is auditable. First-use confirmation without an independently verified fingerprint remains susceptible to an active attacker on that first connection; advanced and headless users can supply `ssh_host_fingerprint` to remove that ambiguity.

---

### ADR-011: Homelab-first DNS and explicit MAAS DHCP cutover

**Status:** Accepted 2026-07-26. The complete contract is in
[`docs/network-cutover.md`](network-cutover.md), with the operator procedure in
[`docs/unifi-network-guide.md`](unifi-network-guide.md).

**Decision:** Stage 1 installs and validates Technitium without modifying router
or non-lab client settings. The desired post-bootstrap state is Technitium as the
DNS resolver for selected networks across the home, rolled out manually through
UniFi one network at a time. Stage 7 installs MAAS with DHCP disabled by default.
MAAS DHCP may be enabled only for a dedicated lab VLAN after explicit approval,
network validation, a bounded conflicting-DHCP probe, and confirmation that
UniFi DHCP was disabled on that VLAN.

**Rationale:** DNS and DHCP changes enlarge the failure domain beyond the
bootstrap server. Keeping the default installation lab-scoped lets a new
operator prove Technitium and preserve a working household network. A separate
UniFi procedure still leads to the requested whole-home DNS end state. Making
MAAS DHCP opt-in prevents two DHCP authorities from silently coexisting.

**Consequences:** Configuration must collect the interface, VLAN, subnet,
gateway, and optional MAAS range. Bootstrap never calls the UniFi API in v1.
Failed target-resolver changes restore the captured prior resolver
configuration. A failed MAAS activation disables partial MAAS DHCP and directs
the operator to restore UniFi DHCP. One Technitium server is an accepted v1
availability risk; a resilient deployment advertises two internal resolvers
rather than mixing Technitium with a public client-side secondary.

---

### ADR-012: Restic backup repositories on TrueNAS and removable USB

**Status:** Accepted 2026-07-27. The policy is defined in
[`docs/backup-recovery.md`](backup-recovery.md), with storage preparation in
[`docs/truenas-backup-guide.md`](truenas-backup-guide.md).

**Decision:** Restic is the default backup engine. The primary repository is a
dedicated TrueNAS dataset, mounted over NFS by default. A separate removable USB
repository is optional and normally disconnected. The wizard recommends nightly
backups at 02:30 with 7 daily, 5 weekly, and 12 monthly snapshots, weekly
metadata checks, monthly full-data checks, and quarterly restore drills. It
displays these defaults and permits backup disablement or custom schedules,
retention, verification, and USB-copy intervals.

**Rationale:** The selected topology needs backups that remain usable when
GitLab or the bootstrap controller is unavailable. Restic provides encrypted,
deduplicated snapshots and repository verification without another server-side
control plane. TrueNAS supplies reliable local storage and optional dataset
snapshots. The disconnected USB repository reduces the shared failure domain of
the host and online NAS.

**Trade-offs:** Restic is CLI-oriented and does not create application-consistent
database or service snapshots itself. Native Vault, PostgreSQL, GitLab,
Authentik, Technitium, MAAS, and k3s artifacts must be produced before Restic
runs. An online NFS repository remains writable by the backup host, so TrueNAS
snapshots and offline USB are defense layers rather than immutability guarantees.

**Consequences:** Backup jobs are Ansible-owned host lifecycle with Go-owned
validation, status, and operator prompts. Repository passwords follow ADR-009:
password-manager backup plus the minimum root-owned runtime copy required for
unattended timers. A missing NAS mount or mismatched repository ID stops backup,
retention, and restore operations. USB absence is reported as copy age, not as a
failure of the primary NAS backup.

---

### ADR-013: Supported trial mode and pre-unseal service recovery

**Status:** Accepted 2026-07-28. Backup trial behavior is defined in
[`docs/backup-recovery.md`](backup-recovery.md); boot ordering and reconciliation
are defined in [`docs/reboot-recovery.md`](reboot-recovery.md).

**Decision:** `backup_enabled: false` is a supported trial profile requiring no
repository, password, mount, timer, or USB device. It does not block bootstrap,
but status and destructive actions remain visibly unprotected until backups are
configured. On host reboot, core services start from minimum protected local
runtime credentials while Vault starts sealed. Manual unseal triggers
Vault/OIDC/GitLab/runtime-secret/backup reconciliation.

**Rationale:** A first-time evaluator should not need to own a NAS or removable
device merely to understand the platform. Silently placing copies on the same
host would create false confidence. Separately, making household DNS and every
web service wait for manual unseal would turn ordinary reboots into broad
outages despite the accepted protected-runtime-copy model.

**Trade-offs:** Trial mode has no protection from host or disk loss. Before
unseal, Vault-dependent CI, new secret delivery, rotation, and fresh Raft
snapshots are unavailable. Protected runtime copies expand the impact of root
host compromise, already accepted in ADR-009.

**Consequences:** Completion and status views distinguish trial/unprotected,
degraded/sealed, partial backup, and healthy states. The operator may enable
backups later without reinstalling services. Unseal shares are entered through
masked prompts and never supplied as CLI arguments or retained. Post-unseal
reconciliation refreshes stale runtime copies only through an explicit,
auditable change path.

---

### ADR-014: Host-managed automatic certificate renewal

**Status:** Accepted 2026-07-28. The complete lifecycle contract is defined in
[`docs/certificate-renewal.md`](certificate-renewal.md).

**Decision:** A systemd timer on the bootstrap host checks certificate
eligibility daily and renews through lego and Cloudflare DNS-01 within a
configurable window, defaulting to 30 days. A Cloudflare API token scoped to the
managed zone with Zone Read and DNS Edit is retained as a root-owned mode `0600`
runtime secret. Candidates are validated in staging, activated atomically, and
rolled back if a sequential consumer reload or health check fails. Renewal
continues while GitLab is unavailable or Vault is sealed; Vault synchronization
may be completed during post-unseal reconciliation.

**Rationale:** Certificate expiry is a host availability concern and should not
depend on the availability of the GitOps or secret-control planes it protects.
The accepted local-runtime-secret model already permits a narrowly scoped token,
and Cloudflare token scoping limits the credential to the required zone
operations.

**Trade-offs:** Root compromise can expose a token capable of editing records in
the certificate zone. Automatic issuance can encounter ACME rate limits or
activate material that individual consumers reject, so locking, validation,
staged activation, per-consumer health checks, and rollback are mandatory.

**Consequences:** Ansible owns timer, service, token file, immutable certificate
versions, and consumer reload integration. Go owns configuration, status,
manual interaction, and reconciliation orchestration. Vault remains
authoritative for certificate inventory and distribution but is not in the
renewal critical path. Disabling automation preserves expiry monitoring and a
persistent warning.

---

### ADR-015: Deadline-driven application maintenance

**Status:** Accepted 2026-07-28. The complete policy is defined in
[`docs/upgrade-policy.md`](upgrade-policy.md).

**Decision:** Ubuntu security packages install automatically without automatic
reboot. Renovate proposes pinned application versions for GitLab review.
Unreviewed proposals become `approved-by-policy` after configurable defaults of
3 days for critical security, 7 days for other security, 21 days for patch or
minor updates, and 30 days for major updates. Eligible proposals execute one
component at a time in a configurable maintenance window, defaulting to Sunday
at 03:00 local time with a three-hour admission window. Deadline expiry never
bypasses compatibility, health, capacity, backup, migration, or rollback gates.

**Rationale:** A single administrator should normally control disruptive
updates but should not accumulate indefinite security and compatibility drift
while unavailable. Separating policy approval from execution gates gives
ordinary changes a review period without turning absence into permanent
stagnation.

**Trade-offs:** An unattended but fully gated upgrade can still cause downtime,
and single-node GitLab cannot be upgraded without an outage. Conservative gates
can also leave an update overdue indefinitely when recovery protection is
missing. Major and data-bearing upgrades require stronger evidence than
stateless immutable rollouts.

**Consequences:** GitLab is the proposal and audit plane; a host-side oneshot
executor completes a started GitLab upgrade independently of GitLab remaining
available. GitLab required stops and migrations and K3s version sequencing are
component-specific hard gates. Trial mode permits only stateless patch/minor
upgrades with proven immutable rollback. Status distinguishes awaiting review,
policy-approved, blocked, overdue, running, rolled back, failed, and
reboot-required states.

---

### ADR-016: Target-local constrained observability

**Status:** Accepted 2026-07-28. The complete monitoring and paging contract is
defined in [`docs/observability.md`](observability.md).

**Decision:** Uptime Kuma, Beszel hub/agent, and the bootstrap health adapter
run on the target server. Kuma owns synthetic checks, incidents, and optional
paging; Beszel owns lightweight high-level system and container metrics with
aggressive aggregation. Kuma and journald retain seven days. The combined
default budget is 512 MiB memory, half of one CPU, and 5 GiB state. Paging via
Discord, Telegram, ntfy, or generic webhook is configurable and may be disabled
without disabling dashboards. TrueNAS performs storage only and runs no
telemetry compute.

**Rationale:** The homelab needs actionable failure signals and basic trend
context, but a Prometheus/Grafana/Loki stack is disproportionate for a
constrained single host. Kuma and Beszel cover functional availability and
high-level resources with a smaller operational and compute footprint.

**Trade-offs:** Target-local paging cannot report total host, power, or outbound
network loss. Beszel's built-in aggregation is more aggressive than retaining
one-minute samples for several days. Optional collectors and noncritical check
cadence must shed before telemetry can contend with core services.

**Consequences:** Ansible owns local containers, agent socket, state,
retention, cgroup limits, health adapter, and protected paging credentials. Go
owns wizard configuration, notification tests, monitor generation, status, and
resource-budget validation. Terraform/service definitions provide declarative
health-check intent. Status explicitly distinguishes monitoring disabled,
paging disabled, local-outage blind spot, resource-degraded telemetry, and
healthy monitoring.

---

### ADR-017: Layered GitLab and Terraform-state disaster recovery

**Status:** Accepted 2026-07-28. The complete contract is defined in
[`docs/gitlab-disaster-recovery.md`](gitlab-disaster-recovery.md).

**Decision:** A nightly full GitLab set protects ordinary instance data with a
less-than-24-hour target. Every successful infrastructure apply also produces
an encrypted Restic checkpoint of critical Git bundles and decrypted Terraform
state, bound to the applied commit, workspace, lineage, and serial. GitLab loss
freezes all IaC mutation. A host-side recovery operation restores the exact
GitLab version, configuration, secrets, identity, isolated database, and
application data; reconciles only a compatible newer checkpoint; verifies the
control plane; and requires explicit unfreeze. Local Terraform is a named,
audited break-glass path only.

**Rationale:** GitLab is both the GitOps source and Terraform backend. Its state
files require the GitLab database and encryption keys, so a GitLab-only backup
creates a recovery dependency precisely when GitLab is unavailable. A decrypted
post-apply export stored immediately inside encrypted Restic breaks that cycle
without establishing a second routine backend.

**Trade-offs:** Decrypted state briefly exists in protected tmpfs and contains
sensitive values. Checkpoint creation adds work after applies and can report a
recovery gap even when live infrastructure is healthy. Reconciling a newer
checkpoint than the full backup requires strict commit, lineage, and serial
matching rather than a simple newest-file restore.

**Consequences:** Ansible owns exact-version installation, runtime recovery,
freeze integration, and GitLab health. Go owns incident interaction, backup
selection, reconciliation validation, verification, and explicit unfreeze.
GitLab pipelines request post-apply checkpoints, but a host-side executor owns
their creation and GitLab's own restoration. Backups include the verified
package artifact, configuration, secrets, identity, isolated database, native
archive, and critical checkpoint metadata.

---

### ADR-018: Portable controller recovery with explicit adoption

**Status:** Accepted 2026-07-28. The complete contract is defined in
[`docs/controller-recovery.md`](controller-recovery.md).

**Decision:** A blank replacement administrator laptop reconstructs control
from the password manager, a verified TrueNAS or USB Restic repository, target
access, and a verified bootstrap release. A portable bundle preserves sanitized
configuration, pinned target identity, backend/ownership metadata, manifests,
and an exact verified bootstrap artifact. The current controller SSH private
key is included by default inside an encryption envelope separate from Restic;
its password remains only in the password manager. Vault init and Shamir
material are excluded. Recovery verifies identity and performs a read-only
audit before explicit adoption. Normal recovery preserves prior credentials;
an explicit lost-or-compromised mode replaces the SSH key and rotates
controller-specific tokens.

**Rationale:** The single administrator laptop is operationally important but
must not become an irreplaceable or authoritative control-plane component. A
portable recovery map removes laptop-specific paths and local-history
dependencies while retaining strict target identity. Separate SSH-key
encryption prevents a target compromise plus its unattended Restic credential
from yielding controller access.

**Trade-offs:** The password manager and Restic repository remain joint
recovery dependencies, and an operator who loses both needs physical console
re-enrollment. Preserving the old key favors ordinary hardware recovery over
automatic revocation. Compromise recovery therefore requires an explicit mode
and may include service-specific manual token rotation.

**Consequences:** Go owns bundle creation, integrity/schema validation, local
path rebinding, read-only audit, adoption, and the rotation checklist. The
target stores only an opaque encrypted artifact plus a non-secret controller
marker. Ansible may manage target-side marker directories and backup hooks but
does not own controller credentials. Every material change refreshes the
bundle; nightly backup includes the newest sealed artifact. Trial mode offers a
verified external export but remains visibly unprotected until one exists.

---

## 6. Key Interface Contracts

### 6.1 Stage Runner Interface

```go
// internal/runner/runner.go

package runner

import (
    "context"

    "github.com/brian/bootstrap-home-infra/internal/config"
    "github.com/brian/bootstrap-home-infra/internal/runcontext"
)

// Stage is implemented by each bootstrap stage (stage0 through stage8)
// and by the Terraform state migration flow.
type Stage interface {
    // Name returns the canonical lowercase hyphenated stage identifier.
    // This value is used in --stage flags, log entries, and stage markers.
    // Examples: "config-collection", "dns", "tls", "vault", "authentik",
    //           "postgres", "gitlab", "maas", "k3s", "migrate-state"
    Name() string

    // Check inspects the target system and returns true if this stage's
    // postconditions are already satisfied. The orchestrator calls Check
    // before Run; if Check returns true, Run is skipped.
    // Check must not make changes to the target system.
    Check(ctx context.Context, cfg *config.Config, rc *runcontext.RunContext) (bool, error)

    // Run executes the stage. It must be idempotent: calling Run on a target
    // where the stage is already complete must produce no changes and return nil.
    // Run communicates with the operator via the Sink interface only.
    // Run must never write secret values to any log or display surface.
    Run(
        ctx     context.Context,
        cfg     *config.Config,
        sec     *config.Secrets,
        rc      *runcontext.RunContext,
        sink    Sink,
    ) error
}

// Sink is the communication interface between stage runners and the operator.
// Implementations: TUISink (writes to Bubbletea model), CLISink (writes to stdout/stdin).
type Sink interface {
    StageStarted(name string)
    StageComplete(name string)
    StageFailed(name, errMsg, logPath string, logLine int)
    Log(level, stage, msg string, fields map[string]string)
    Prompt(label string, masked bool) (string, error)
    Confirm(msg string) (bool, error)
    ShowWarningBox(lines []string)
}
```

### 6.2 Config Struct

```go
// internal/config/config.go

package config

// Config holds all non-sensitive structural configuration from config.yaml.
// No secrets. Safe to log field names (not values).
type Config struct {
    TargetHost          string `yaml:"target_host"`           // IP or hostname of target Ubuntu server
    SSHUser             string `yaml:"ssh_user"`              // Default: "ubuntu"
    SSHKeyPath          string `yaml:"ssh_key_path"`          // Path to private key on operator machine
    SSHHostFingerprint  string `yaml:"ssh_host_fingerprint"`  // Optional expected SHA-256 host key fingerprint
    InternalDomain      string `yaml:"internal_domain"`       // e.g. "home.example.com"
    CloudflareZoneID    string `yaml:"cloudflare_zone_id"`    // Cloudflare zone ID for internal domain
    BootstrapServerIP   string `yaml:"bootstrap_server_ip"`   // Static IP of the bootstrap server
    LabVLANSubnet       string `yaml:"lab_vlan_subnet"`       // CIDR, e.g. "10.10.10.0/24"
    LabGateway          string `yaml:"lab_gateway"`           // Gateway address inside the lab subnet
    LabVLANID           int    `yaml:"lab_vlan_id"`           // VLAN ID on UniFi
    LabVLANInterface    string `yaml:"lab_vlan_interface"`    // Required for MAAS DHCP
    DHCPRangeLow        string `yaml:"dhcp_range_low"`        // Optional MAAS dynamic range start
    DHCPRangeHigh       string `yaml:"dhcp_range_high"`       // Optional MAAS dynamic range end
    MAASDHCPEnabled     bool   `yaml:"maas_dhcp_enabled"`     // Default false; deliberate cutover
    BackupEnabled       bool   `yaml:"backup_enabled"`        // Default false until repository supplied
    BackupRepository    string `yaml:"backup_repository"`     // Restic path or URI
    BackupOnCalendar    string `yaml:"backup_on_calendar"`    // Default daily 02:30
    BackupKeepDaily     int    `yaml:"backup_keep_daily"`     // Default 7
    BackupKeepWeekly    int    `yaml:"backup_keep_weekly"`    // Default 5
    BackupKeepMonthly   int    `yaml:"backup_keep_monthly"`   // Default 12
    BackupMetadataCheck string `yaml:"backup_metadata_check_interval"` // Default weekly
    BackupFullCheck     string `yaml:"backup_full_check_interval"`     // Default monthly
    BackupUSBEnabled    bool   `yaml:"backup_usb_enabled"`    // Optional offline copy
    BackupUSBRepository string `yaml:"backup_usb_repository"` // Removable Restic repo
    BackupUSBCopy       string `yaml:"backup_usb_copy_interval"` // Manual/weekly/monthly
    BackupRestoreDrill  string `yaml:"backup_restore_drill_interval"` // Default quarterly
    ControllerRecoveryEnabled bool `yaml:"controller_recovery_enabled"` // Default true
    ControllerRecoveryRefresh bool `yaml:"controller_recovery_refresh_after_change"` // Default true
    ControllerRecoveryIncludeSSHKey bool `yaml:"controller_recovery_include_ssh_private_key"` // Default true
    AdminEmail          string `yaml:"admin_email"`           // Admin email for all services
    VaultInitOutputPath string `yaml:"vault_init_output_path"`// Recovery material path outside repo
    VaultShares         int    `yaml:"vault_shares"`          // Shamir share count; default 5
    VaultThreshold      int    `yaml:"vault_threshold"`       // Unseal threshold; default 3
}

// Secrets holds all sensitive values sourced from the process environment (loaded from .env).
// Fields are populated by reading the protected .env input; values are never
// written to structural config, process arguments, or logs.
// The zero value of any field means "not set"; all fields are required unless noted.
type Secrets struct {
    CFDNSAPIToken       string // CF_DNS_API_TOKEN — Cloudflare API token for DNS-01
    AdminPassword       string // BOOTSTRAP_ADMIN_PASSWORD — master admin password
    ResticPrimaryPassword string // RESTIC_PRIMARY_PASSWORD — optional primary repository key
    ResticUSBPassword     string // RESTIC_USB_PASSWORD — optional removable repository key
    ControllerRecoveryPassword string // CONTROLLER_RECOVERY_PASSWORD — separate SSH-key envelope key
}
```

### 6.3 Service Definition Schema

```yaml
# services/<name>.yaml
# Deployed via GitLab CI terraform apply after bootstrap.

name: string              # required; unique service identifier; slug format
hostname: string          # required; FQDN, e.g. "ollama.home.example.com"; auto-creates DNS A record
target:
  # Exactly one of the following target types must be present:

  # Option A: k8s cluster target (Helm chart)
  cluster: string           # named k3s cluster from infrastructure/clusters/
  helm_chart: string        # Helm chart name (from a configured chart repository)
  helm_repo: string         # Helm repository URL (optional; uses default if omitted)
  values: string            # relative path to values file within infrastructure/iac repo

  # Option B: bare metal target (MAAS + Ansible)
  maas_machine: string      # named MAAS machine from MAAS inventory
  ansible_role: string      # Ansible role to apply post-MAAS provisioning

authentik: bool           # required; true = OIDC proxy; false = direct access
allowed_groups:           # required; list of Authentik group names
  - string
health_check: string      # required; format: "http", "https", "tcp:<port>"; materialized as a Kuma monitor in v1
```

### 6.4 Node Definition Schema

```yaml
# infrastructure/nodes/<name>.yaml

name: string              # required; stable identifier; must match cluster definition references
provisioner: string       # required; one of: "maas", "proxmox-vm", "proxmox-lxc"

# Fields required when provisioner: "maas"
maas_machine: string      # MAAS machine name/system ID

# Fields required when provisioner: "proxmox-vm"
proxmox_host: string      # Proxmox host FQDN or IP
vm_template: string       # Proxmox VM template name, e.g. "ubuntu-24.04"

# Fields required when provisioner: "proxmox-lxc"
proxmox_host: string      # Proxmox host FQDN or IP
template: string          # LXC template name, e.g. "ubuntu-24.04"
```

### 6.5 Cluster Definition Schema

```yaml
# infrastructure/clusters/<name>.yaml

name: string              # required; stable identifier used in service definitions and Vault paths
nodes:                    # required; list must have at least one control-plane role
  - name: string          # must match a node name in infrastructure/nodes/
    roles:                # list; valid values: "control-plane", "worker"
      - string
```

---

## 7. Key Data Flows

### 7.1 Secret Handling Flow

```
Operator machine                Target host            Vault
                                                       (after Stage 3)
.env (CF_DNS_API_TOKEN)
  → mode 0600; preserved by default for safe re-runs
  → Go runner reads env var
  → passes as extra-var to Ansible
  → never written to logs or process arguments

Generated secrets (passwords, tokens):
  → Go runner generates in memory
  → writes to Vault KV via HTTPS
  → Vault is the authoritative record
  → core host services may receive a minimum root-owned mode 0600 runtime copy
  → rotation reconciles local copies atomically, then reloads/restarts services

vault-init.json:
  → written to an operator-selected path outside the repository
  → contains all Shamir unseal keys and root token
  → TUI recommends backup in a password manager or other secure location
  → preserved permanently; cleanup must never delete it

Bootstrap Vault token:
  → generated in Stage 3; held in RunContext struct in Go process memory
  → never written to disk, log file, or TUI
  → TTL: 24h; warn if > 23h elapsed since Stage 3 start

Future delivery:
  → selected host services may use Vault Agent templates rendered under /run
  → k3s workloads prefer Kubernetes auth + Vault Agent Injector
  → SPIFFE/SPIRE deferred until multi-node workload identity or mTLS is required
```

### 7.2 Architecture Neutrality

Every place where an arch-specific artifact is selected follows this pattern:

```go
// internal/runcontext/context.go
type TargetArch string

const (
    AMD64 TargetArch = "x86_64"
    ARM64 TargetArch = "aarch64"
)

// AnsibleArch maps to Ansible's ansible_architecture fact value.
func (a TargetArch) AnsibleArch() string {
    switch a {
    case AMD64:
        return "x86_64"
    case ARM64:
        return "aarch64"
    default:
        return string(a)
    }
}

// GoArch maps to Go's GOARCH values (used for download URL construction).
func (a TargetArch) GoArch() string {
    switch a {
    case AMD64:
        return "amd64"
    case ARM64:
        return "arm64"
    default:
        return string(a)
    }
}
```

Ansible roles use `ansible_architecture` to branch on arch-specific package names and binary URLs. The mapping is in `vars/main.yml` of each role that downloads binaries:

```yaml
# roles/tls_lego/vars/main.yml
lego_arch_map:
  x86_64: amd64
  aarch64: arm64
lego_arch: "{{ lego_arch_map[ansible_architecture] }}"
lego_download_url: "https://github.com/go-acme/lego/releases/download/{{ lego_version }}/lego_{{ lego_version }}_linux_{{ lego_arch }}.tar.gz"
```

---

## 8. Test Infrastructure

### 8.1 QEMU Base Image

Built by `test/images/base.pkr.hcl` (Packer HCL):
- Ubuntu 22.04 and 24.04, x86_64 and aarch64 variants (four images total)
- SSH enabled; password auth disabled; key-based only
- `ansible` user with passwordless sudo
- Test keypair embedded at build time:
  - Public key: embedded in `~/.ssh/authorized_keys` during Packer provision step
  - Private key: `test/keys/test_id_ed25519` in the repo (test-only; not a secret; never used outside QEMU VMs)
- No other services; clean slate

Phase 1 (pre-GitLab): `just build-base-image` runs Packer locally.
Phase 2 (post-GitLab): GitLab CI builds and caches the image as a pipeline artifact.

### 8.2 Test Doubles

**Pebble (ACME CA):**
- Configuration: `test/mocks/pebble/pebble-config.json`
- DNS challenge verification: `challtestsrv` sidecar that accepts challenge responses from the mock Cloudflare stub
- The Go runner sets `ACME_SERVER_URL=https://localhost:14000/dir` in the test harness environment

**Cloudflare API mock:**
- Implementation: `test/mocks/cloudflare/server.go` — minimal HTTP server responding to:
  - `GET /client/v4/zones` → returns a fake zone record for the test domain
  - `POST /client/v4/zones/<id>/dns_records` → accepts and returns success
  - `DELETE /client/v4/zones/<id>/dns_records/<id>` → returns success
- The Go runner sets `CLOUDFLARE_API_BASE_URL=http://localhost:8080` in the test harness environment
- Neither mock ever calls a real external API

### 8.3 Justfile Targets

```justfile
# Build base QEMU test images (x86 and ARM) via Packer
build-base-image:
    packer build test/images/base.pkr.hcl

# Build the bootstrap binary for the current platform
build:
    go build -o bin/bootstrap ./cmd/bootstrap

# Run Molecule tests for a single Ansible role (idempotency: role applied twice)
test-role role:
    cd ansible/roles/{{ role }} && molecule test

# Run a single bootstrap stage against a fresh QEMU VM
# Starts Pebble and Cloudflare mock containers for Stage 2
test-stage stage:
    go run ./cmd/bootstrap --target qemu --stage {{ stage }} --test

# Run the full bootstrap end-to-end against a QEMU VM
test-bootstrap:
    go run ./cmd/bootstrap --target qemu --test

# Adopt a server: gathers hardware metadata and opens a GitLab MR
adopt-server ip:
    go run ./cmd/bootstrap adopt --target {{ ip }}
```

---

## 9. Dependency Map

```
Stage 0 (Config)
    └── Stage 1 (DNS)
            └── Stage 2 (TLS)
                    └── Stage 3 (Vault)  ←── vault-init.json written here
                            ├── Stage 4 (PostgreSQL)  ←── final service databases
                            │       └── Stage 5 (Authentik)  ←── starts on final DB
                            │               └── Stage 6 (GitLab + Runner)
                            │                       └── Stage 7 (MAAS)
                            │                               └── Stage 8 (k3s)
                            │                                       └── Terraform State Migration
                            │                                               └── BOOTSTRAP COMPLETE (SC-14)
                            └── [bootstrap token in-memory; 24h TTL; used by Stages 4–8]
```

All stages from 3 onward check Vault seal state before executing. Any stage may be re-run idempotently using `bootstrap --stage <name>`. The `--headless` flag enables non-interactive re-runs for CI and automation.
