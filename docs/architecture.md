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
│   │   ├── stage4_authentik.go      # Stage 4: Authentik deploy and OIDC wiring
│   │   ├── stage5_postgres.go       # Stage 5: PostgreSQL + Authentik migration
│   │   ├── stage6_gitlab.go         # Stage 6: GitLab CE + Runner + Vault JWT
│   │   ├── stage7_maas.go           # Stage 7: MAAS install and lab VLAN DHCP
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
│       │   │   └── network.yml      # Fabric, subnet, DHCP config via maas CLI
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
│       ├── maas-config/             # MAAS machines, subnets, DHCP config
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

**Purpose:** Transfer lego binary for target arch; run DNS-01 challenge; collect cert and key to working directory.

**Idempotency gate:** Check cert file existence and expiry (via `openssl x509 -noout -dates`) before running lego. Skip if valid cert present with > 30 days until expiry.

**Secret injection:** `CF_DNS_API_TOKEN` passed as extra-var and used only in the lego invocation environment block. `ACME_SERVER_URL` and `CLOUDFLARE_API_BASE_URL` read from extra-vars, defaulting to production endpoints.

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

**Bootstrap vs shared PostgreSQL:** The compose template has a conditional block: if `authentik_use_bundled_postgres` is true (default), bundled PostgreSQL is included in the compose file. The Go runner flips this to false during the Stage 5 migration and re-runs the role.

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

**Purpose:** Install MAAS via snap (preferred) or deb for target arch, run `maas init region+rack`, start MAAS services. Network configuration (fabric, subnet, DHCP) is done by the Go runner via the MAAS CLI/API after the service is healthy.

**Idempotency gate:** Check `maas status` before install. Check `snap list maas` for existing installation.

**Secret injection:** PostgreSQL DSN for MAAS passed as extra-var `maas_db_uri`; injected into `maas init` command environment block with `no_log: true` on the task.

---

## 4. Terraform Module Layout

### 4.1 Module Breakdown

| Module | Provider(s) | Purpose |
|--------|-------------|---------|
| `dns-records` | `technitium` (community) | Creates and manages A records in Technitium for a given service hostname. Input: `hostname`, `ip`. Called by the service deploy pipeline. |
| `vault-config` | `hashicorp/vault` | Manages Vault policies, KV paths, auth method configuration, and PKI setup. Used during bootstrap and for post-bootstrap policy updates. |
| `authentik-config` | `goauthentik/authentik` | Manages groups, OIDC providers, and applications. Called during bootstrap Stage 4 and by the service deploy pipeline when `authentik: true`. |
| `gitlab-config` | `gitlabhq/gitlab` | Manages GitLab groups, projects, Runner registration, and project-level CI/CD variables. Used during Stage 6. |
| `maas-config` | `maas/maas` | Manages MAAS machine records, subnet definitions, and DHCP configuration. Used during Stage 7 and by the server adoption MR pipeline. |
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

**Consequences:** The Go binary must validate `.env` presence and completeness at startup. The `.gitignore` must exclude `.env` and `config.yaml` (both are excluded; only `config.yaml.example` and `.env.example` are committed). Every Ansible role and Terraform module invocation must receive secrets via environment variable injection, not through files written to disk.

---

### ADR-003: Ansible for initial deployment + Terraform for GitOps

**Decision:** Service installation during bootstrap uses Ansible roles (imperative, SSH-based). Post-bootstrap service management uses Terraform (declarative, GitOps-driven via GitLab CI).

**Rationale:** Ansible excels at one-time system configuration tasks: installing packages, writing config files, managing systemd units. These tasks are appropriate during bootstrap where the target is a bare OS and Terraform's state management would be premature overhead. Terraform excels at declarative lifecycle management of services and infrastructure resources, which matches the post-bootstrap GitOps model. Trying to use Terraform from the start hits the chicken-and-egg problem: Terraform needs a state backend; the state backend (GitLab) is not yet running.

**Trade-offs:** Two tool paradigms to maintain. Ansible roles and Terraform modules can drift in their understanding of what constitutes the deployed state of a service. Operators need familiarity with both.

**Consequences:** The handoff boundary is explicit: Ansible owns installation; Terraform owns post-bootstrap lifecycle. After bootstrap completes and state is migrated, Ansible is only invoked by GitLab CI pipelines (for bare metal provisioning post-MAAS commissioning), never directly by the operator.

---

### ADR-004: Raft integrated storage for Vault

**Decision:** Vault uses the integrated Raft storage backend (`storage "raft"`), not an external storage backend (Consul, etcd, or a database).

**Rationale:** Raft integrated storage eliminates a dependency on any external service for Vault. During bootstrap, no external coordination service is running. Raft is production-supported by HashiCorp as of Vault 1.4, handles the single-node homelab topology cleanly, and requires no additional infrastructure. Consul would require its own install and clustering before Vault could start — circular to the bootstrap problem.

**Trade-offs:** Raft integrated storage means Vault cannot be easily clustered beyond three nodes without careful migration. Snapshots for backup require explicit `vault operator raft snapshot save` commands. For a single-server homelab this is acceptable.

**Consequences:** `vault.hcl.j2` configures `storage "raft" { path = "/opt/vault/data" node_id = "<hostname>" }`. Backup procedures must use `vault operator raft snapshot save`. High availability clustering is out of scope for v1.

---

### ADR-005: Authentik bundled PostgreSQL during bootstrap, migrated in Stage 5

**Decision:** Authentik starts in Stage 4 using its bundled PostgreSQL container. At the end of Stage 5, after the shared PostgreSQL instance is running, Authentik is migrated (stop → pg_dump → restore → restart) to the shared instance.

**Rationale:** Stage 4 (Authentik) must precede Stage 5 (PostgreSQL) because the strict dependency order is: DNS → TLS → Vault → Authentik → PostgreSQL → GitLab. Authentik must be running before GitLab because GitLab needs Authentik OIDC credentials. PostgreSQL must come after Authentik because MAAS and GitLab both depend on it, and managing the shared instance as a service requires Authentik SSO to already work. Breaking this circular dependency requires Authentik to bootstrap on a temporary storage (its bundled PostgreSQL) and migrate later.

**Trade-offs:** The migration adds complexity: a stop-export-import-restart sequence with rollback handling. If the migration fails mid-import, the bootstrap runner must revert Authentik to the bundled DSN. This is the most operationally delicate step in the bootstrap sequence.

**Consequences:** The `authentik_install` Ansible role must support both `authentik_use_bundled_postgres: true` (Stage 4 default) and `authentik_use_bundled_postgres: false` (Stage 5 migration). The Go Stage 5 runner must implement the pg_dump → pg_restore sequence with rollback on failure. The migration is idempotent: if Authentik's config already points to the shared DSN, the step is skipped.

---

### ADR-006: `ACME_SERVER_URL` + `CLOUDFLARE_API_BASE_URL` as first-class env overrides

**Decision:** The ACME server URL (Let's Encrypt endpoint) and Cloudflare API base URL are read from environment variables at the point of use, with production defaults. Setting them in the test harness redirects all certificate issuance and DNS API calls to local test doubles (Pebble and a stub HTTP server) without any conditional code paths.

**Rationale:** Retrofitting test overrides after the fact is consistently painful and leads to test-mode code paths that diverge from production paths. Making these overrides first-class means the production code path and test code path are identical — only the URL changes. This satisfies the global invariant that tests never call real external APIs.

**Trade-offs:** Two extra env vars that operators might accidentally set in their real `.env`. Mitigated by documenting them as test-only in `.env.example` with a clear comment.

**Consequences:** The `tls_lego` Ansible role reads `acme_server_url` and `cloudflare_api_base_url` from extra-vars passed by the Go runner, which sources them from the environment. The Go runner's cert validation logic (parsing the issuer) must accept any CA when `ACME_SERVER_URL` is overridden, not just Let's Encrypt. The Pebble container config in `test/mocks/pebble/` must be kept in sync with the domain used in test runs.

---

### ADR-007: Local Terraform state during bootstrap → GitLab HTTP backend post-bootstrap

**Decision:** Terraform state is managed locally (local backend in `terraform/environments/bootstrap/`) during bootstrap. After all stages complete, `terraform init -migrate-state` migrates each workspace to the GitLab HTTP backend. Local `.tfstate` files are deleted only after the GitLab backend confirms the state is readable. This migration is the SC-14 completion condition.

**Rationale:** The GitLab HTTP backend cannot exist before GitLab is running. Running Terraform with a local backend during bootstrap avoids the circular dependency. The migration procedure is a well-defined `terraform init -migrate-state` operation with explicit verification and non-destructive rollback (local file not deleted until confirmed).

**Trade-offs:** Local `.tfstate` files on the operator's machine are a temporary source of truth. If the operator's machine is lost between bootstrap completion and state migration, those state files are lost. Mitigated by: (1) bootstrap completes quickly (single session), (2) migration runs immediately after final stage, (3) local files are in the working directory that the operator chose.

**Consequences:** `terraform/environments/bootstrap/` must be in `.gitignore` for the `*.tfstate` and `*.tfstate.backup` entries, but the `*.tf` files are committed. The migration runner must handle multiple workspaces independently: a failure in one must not prevent migration of others. The Terraform project access token used for the HTTP backend is stored in Vault and injected as `TF_VAR_gitlab_terraform_token` at migration time.

---

### ADR-008: k3s cluster as named IaC target; node provisioner decoupled from cluster definition

**Decision:** A k3s cluster is a named first-class IaC object defined in `infrastructure/clusters/<name>.yaml`. The cluster definition references nodes by name. How those nodes are provisioned (MAAS bare metal, Proxmox VM, Proxmox LXC, or any other method) is defined separately in `infrastructure/nodes/<name>.yaml` and is completely opaque to the cluster definition and to Stage 8.

**Rationale:** Coupling cluster definition to provisioner type would require a new cluster schema variant for every provisioner. Decoupling means the cluster topology (which nodes, which roles) is independent of the infrastructure procurement path. Stage 8 only requires SSH reachability of each node; it does not care how the node was provisioned. This matches the PRD's explicit statement that "the provisioning method is an implementation detail of the node — not of the cluster or the service deployed to it."

**Trade-offs:** Two files per node to maintain: the node definition and whatever the provisioner needs. The indirection through node names requires that node names are stable identifiers used consistently across the cluster definition, the node definition, and the Ansible dynamic inventory.

**Consequences:** Stage 8 resolves node names to IPs by reading node definitions and querying the appropriate provisioner API (MAAS API for MAAS nodes, Proxmox API for Proxmox nodes) at runtime. If a node is not SSH-reachable regardless of provisioner state, Stage 8 warns and skips that node without failing the stage. The Terraform `k3s-cluster` module does not import provisioner-specific resources; it only registers the cluster endpoint and kubeconfig.

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
    InternalDomain      string `yaml:"internal_domain"`       // e.g. "home.example.com"
    CloudflareZoneID    string `yaml:"cloudflare_zone_id"`    // Cloudflare zone ID for internal domain
    BootstrapServerIP   string `yaml:"bootstrap_server_ip"`   // Static IP of the bootstrap server
    LabVLANSubnet       string `yaml:"lab_vlan_subnet"`       // CIDR, e.g. "10.10.10.0/24"
    LabVLANID           int    `yaml:"lab_vlan_id"`           // VLAN ID on Unifi
    AdminEmail          string `yaml:"admin_email"`           // Admin email for all services
    VaultShares         int    `yaml:"vault_shares"`          // Shamir share count; default 5
    VaultThreshold      int    `yaml:"vault_threshold"`       // Unseal threshold; default 3
}

// Secrets holds all sensitive values sourced from the process environment (loaded from .env).
// Fields are populated by reading env vars; values are never written to disk or logs.
// The zero value of any field means "not set"; all fields are required unless noted.
type Secrets struct {
    CFDNSAPIToken       string // CF_DNS_API_TOKEN — Cloudflare API token for DNS-01
    AdminPassword       string // BOOTSTRAP_ADMIN_PASSWORD — master admin password
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
health_check: string      # required; format: "http", "https", "tcp:<port>"; hook for future monitoring
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
  → process env only
  → Go runner reads env var
  → passes as extra-var to Ansible
  → Ansible uses in task env: block     (never written to disk on target)

Generated secrets (passwords, tokens):
  → Go runner generates in memory
  → writes to Vault KV via HTTPS
  → Ansible reads from Vault via env var injection (Go runner fetches, passes as extra-var)

vault-init.json:
  → written to operator's working dir on first Vault init
  → contains all Shamir unseal keys and root token
  → operator's responsibility to secure offline
  → never committed to git (in .gitignore)

Bootstrap Vault token:
  → generated in Stage 3; held in RunContext struct in Go process memory
  → never written to disk, log file, or TUI
  → TTL: 24h; warn if > 23h elapsed since Stage 3 start
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
                            ├── Stage 4 (Authentik)  ←── uses bundled postgres
                            │       └── Stage 5 (PostgreSQL)  ←── migrates Authentik here
                            │               └── Stage 6 (GitLab + Runner)
                            │                       └── Stage 7 (MAAS)
                            │                               └── Stage 8 (k3s)
                            │                                       └── Terraform State Migration
                            │                                               └── BOOTSTRAP COMPLETE (SC-14)
                            └── [bootstrap token in-memory; 24h TTL; used by Stages 4–8]
```

All stages from 3 onward check Vault seal state before executing. Any stage may be re-run idempotently using `bootstrap --stage <name>`. The `--headless` flag enables non-interactive re-runs for CI and automation.
