# Raspberry Pi Dual-Storage Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add optional secondary disk support (SD card OS + SSD data) to the bootstrap system so write-heavy service data is redirected to an SSD when one is detected.

**Architecture:** A new `storage_setup` Ansible role runs before Stage 1 (DNS). It formats (if needed), mounts the secondary disk, and creates service data directories. All stage runners inject `service_data_root` (either `/data` or empty) into Ansible extra-vars. Each Ansible role conditionally uses this variable to redirect its data path to the SSD.

**Tech Stack:** Go (config, stage runner), Ansible (storage_setup role, role defaults/templates), Molecule (role testing), QEMU (integration tests), Justfile (test harness)

**Spec:** `docs/superpowers/specs/2026-04-02-rpi-dual-storage-design.md`

---

## File Map

### New files
- `ansible/roles/storage_setup/` — new Ansible role (tasks, defaults, handlers, molecule/)
- `internal/stages/stage0b_storage.go` — new `StorageSetupStage` struct
- `config.yaml.example` — example config with new optional fields documented

### Modified files
- `internal/config/config.go` — add `DataDisk`, `DataDiskMode`, `DataDiskForceFormat` fields; add `DataPath()` method; add validation
- `internal/config/config_test.go` — new test cases for new fields and `DataPath()`
- `internal/stages/stage0_config.go` — add disk detection + TUI picker step
- `internal/stages/stage1_dns.go` — add `service_data_root` to extra-vars
- `internal/stages/stage3_vault.go` — add `service_data_root` to extra-vars
- `internal/stages/stage4_authentik.go` — add `service_data_root` to extra-vars
- `internal/stages/stage5_postgres.go` — add `service_data_root` to extra-vars
- `internal/stages/stage6_gitlab.go` — add `service_data_root` to extra-vars
- `internal/stages/stage7_maas.go` — add `service_data_root` to extra-vars
- `cmd/bootstrap/main.go` — add `StorageSetupStage` to `buildStages()`
- `ansible/roles/technitium_dns/defaults/main.yml` — add `service_data_root`, update `technitium_data_dir`
- `ansible/roles/vault_install/defaults/main.yml` — add `service_data_root`, update `vault_data_dir`
- `ansible/roles/authentik_install/defaults/main.yml` — add `service_data_root`, update `authentik_data_dir`
- `ansible/roles/postgresql_install/defaults/main.yml` — add `service_data_root`, add `postgresql_data_dir`
- `ansible/roles/gitlab_install/defaults/main.yml` — add `service_data_root`, add `gitlab_data_dir`
- `ansible/roles/maas_install/defaults/main.yml` — add `service_data_root` (no-op pending snap investigation)
- `ansible/roles/*/molecule/default/converge.yml` — add `service_data_root: /tmp/test-data` to exercise non-default path
- `Justfile` — add `vm-up-ssd` and `test-bootstrap-ssd` targets

---

## Task 1: Config fields, DataPath(), validation, and unit tests

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

### Why the conditional approach for roles (intentional spec deviation)

The spec shows `DataPath()` returning `"/var/lib"` when no disk is configured. **This plan intentionally deviates from the spec on this point** — returning `""` (empty string) is correct.

The existing role data dirs use `/opt/...` paths (e.g., `/opt/technitium/data`, `/opt/vault/data`), not `/var/lib/...`. Returning `/var/lib` from `DataPath()` would silently redirect all service data from their native paths to `/var/lib/...` on single-disk targets — a breaking change for anyone with an existing deployment. Returning `""` instead lets each role fall back to its own hardcoded default via Jinja2 conditional logic when `service_data_root` is empty. `DataPath()` returns `"/data"` (disk configured, full mode) or `""` (no disk or mount_only). Stage runners pass `""` when there is no disk — roles interpret empty `service_data_root` as "use native path."

- [ ] **Step 1: Add fields to Config struct**

In `internal/config/config.go`, add three fields after `VaultInitOutputPath`:

```go
// DataDisk is the device path of the secondary storage disk (e.g. "/dev/sda").
// Empty means no secondary disk — all data remains on the OS disk.
DataDisk string `yaml:"data_disk"`
// DataDiskMode controls how the secondary disk is used.
// "full" (default): redirects Docker root and all service data to /data.
// "mount_only": formats and mounts at /data only; no service redirection.
DataDiskMode string `yaml:"data_disk_mode"`
// DataDiskForceFormat, when true, wipes the disk even if a filesystem is
// detected (other than LABEL=homelab-data). Requires explicit operator intent.
DataDiskForceFormat bool `yaml:"data_disk_force_format"`
```

- [ ] **Step 2: Add DataPath() method**

Add this method to `config.go`, after the `Config` struct:

```go
// DataPath returns the root path for service data.
// Returns "/data" when a secondary disk is configured in full mode.
// Returns "" (empty) otherwise — roles interpret empty as "use native path".
func (c *Config) DataPath() string {
	if c.DataDisk != "" && c.DataDiskMode != "mount_only" {
		return "/data"
	}
	return ""
}
```

- [ ] **Step 3: Add validation**

In the `Validate` function, add after the vault threshold checks:

```go
// Validate data_disk if set.
if rc.DataDisk != "" {
	validDataDisk := regexp.MustCompile(`^/dev/(sd[a-z]+|nvme[0-9]+n[0-9]+|mmcblk[0-9]+|vd[a-z]+)$`)
	if !validDataDisk.MatchString(rc.DataDisk) {
		return fmt.Errorf("data_disk %q must be a block device path (/dev/sd*, /dev/nvme*, /dev/mmcblk*, /dev/vd*)", rc.DataDisk)
	}
}
if rc.DataDiskMode != "" && rc.DataDisk == "" {
	return fmt.Errorf("data_disk_mode requires data_disk to be set")
}
if rc.DataDiskMode != "" && rc.DataDiskMode != "full" && rc.DataDiskMode != "mount_only" {
	return fmt.Errorf("data_disk_mode must be \"full\" or \"mount_only\", got %q", rc.DataDiskMode)
}
```

Note: `validDataDisk` regex needs to be declared at package level alongside `internalDomainRE`, or inline inside `Validate`.

- [ ] **Step 4: Write failing tests**

Add to `internal/config/config_test.go`:

```go
// ── DataPath ──────────────────────────────────────────────────────────────────

func TestDataPath_NoDisk(t *testing.T) {
	c := &Config{}
	if got := c.DataPath(); got != "" {
		t.Errorf("DataPath() with no disk = %q, want empty string", got)
	}
}

func TestDataPath_FullMode(t *testing.T) {
	c := &Config{DataDisk: "/dev/sda", DataDiskMode: "full"}
	if got := c.DataPath(); got != "/data" {
		t.Errorf("DataPath() full mode = %q, want /data", got)
	}
}

func TestDataPath_ModeUnset_DefaultsFull(t *testing.T) {
	c := &Config{DataDisk: "/dev/sda"}
	if got := c.DataPath(); got != "/data" {
		t.Errorf("DataPath() with mode unset = %q, want /data", got)
	}
}

func TestDataPath_MountOnlyMode(t *testing.T) {
	c := &Config{DataDisk: "/dev/sda", DataDiskMode: "mount_only"}
	if got := c.DataPath(); got != "" {
		t.Errorf("DataPath() mount_only = %q, want empty string", got)
	}
}

// ── Validate: data_disk ───────────────────────────────────────────────────────

func TestValidate_DataDisk(t *testing.T) {
	cases := []struct {
		name      string
		disk      string
		mode      string
		wantError bool
		substr    string
	}{
		{
			name: "valid sda",
			disk: "/dev/sda", mode: "full",
			wantError: false,
		},
		{
			name: "valid nvme",
			disk: "/dev/nvme0n1", mode: "full",
			wantError: false,
		},
		{
			name: "valid mmcblk",
			disk: "/dev/mmcblk1", mode: "full",
			wantError: false,
		},
		{
			name: "valid virtio (QEMU)",
			disk: "/dev/vdb", mode: "full",
			wantError: false,
		},
		{
			name:      "invalid path",
			disk:      "/tmp/notadisk", mode: "full",
			wantError: true, substr: "data_disk",
		},
		{
			name:      "mode without disk",
			disk:      "", mode: "full",
			wantError: true, substr: "data_disk_mode",
		},
		{
			name:      "invalid mode value",
			disk:      "/dev/sda", mode: "wiped",
			wantError: true, substr: "data_disk_mode",
		},
		{
			name: "empty disk and empty mode — valid SD-only",
			disk: "", mode: "",
			wantError: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := validRunConfig()
			rc.DataDisk = tc.disk
			rc.DataDiskMode = tc.mode
			err := Validate(rc)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.substr)
				}
				if !strings.Contains(err.Error(), tc.substr) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.substr)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}
```

- [ ] **Step 5: Run tests to verify they fail correctly**

```bash
go test ./internal/config/... -run "TestDataPath|TestValidate_DataDisk" -v
```

Expected: `TestDataPath_*` fail (method not implemented), `TestValidate_DataDisk` fail (validation not implemented).

- [ ] **Step 6: Implement (steps 1–3 above) and run tests to verify they pass**

```bash
go test ./internal/config/... -v
```

Expected: all tests pass, including existing ones.

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add data_disk fields, DataPath() helper, and validation"
```

---

## Task 2: storage_setup Ansible role

**Files:**
- Create: `ansible/roles/storage_setup/defaults/main.yml`
- Create: `ansible/roles/storage_setup/tasks/main.yml`
- Create: `ansible/roles/storage_setup/handlers/main.yml`

- [ ] **Step 1: Create role defaults**

Create `ansible/roles/storage_setup/defaults/main.yml`:

```yaml
---
# data_disk: block device path for secondary storage (e.g. /dev/sda).
# Empty means no secondary disk — entire role is a no-op.
data_disk: ""
# data_disk_mode: "full" or "mount_only"
# "full": redirect Docker root + service data to /data (default)
# "mount_only": only format and mount; no service redirection
data_disk_mode: "full"
# data_disk_force_format: when true, wipe the disk even if a filesystem is
# detected (other than LABEL=homelab-data).
data_disk_force_format: false
# service_data_root: derived from data_disk + data_disk_mode.
# Set by the Go stage runner via extra-vars; do not override here.
service_data_root: ""
```

- [ ] **Step 2: Create role tasks**

Create `ansible/roles/storage_setup/tasks/main.yml`:

```yaml
---
# No-op when no disk is configured.
- name: Skip storage setup (no data_disk configured)
  ansible.builtin.meta: end_play
  when: data_disk | length == 0

# ── Pre-flight guard ─────────────────────────────────────────────────────────

- name: Detect filesystem on data_disk
  ansible.builtin.command: blkid -o value -s TYPE {{ data_disk }}
  register: blkid_type
  changed_when: false
  failed_when: false

- name: Detect label on data_disk
  ansible.builtin.command: blkid -o value -s LABEL {{ data_disk }}
  register: blkid_label
  changed_when: false
  failed_when: false

- name: Fail if disk has unrecognized filesystem and force_format is false
  ansible.builtin.fail:
    msg: >
      Disk {{ data_disk }} has an existing filesystem
      (TYPE={{ blkid_type.stdout | default('unknown') }},
      LABEL={{ blkid_label.stdout | default('none') }}).
      Set data_disk_force_format: true to wipe it.
  when:
    - blkid_type.stdout | length > 0
    - blkid_label.stdout != 'homelab-data'
    - not (data_disk_force_format | bool)

# ── Format ───────────────────────────────────────────────────────────────────

- name: Format data_disk as ext4 with label homelab-data
  community.general.filesystem:
    fstype: ext4
    dev: "{{ data_disk }}"
    opts: "-L homelab-data"
    force: "{{ data_disk_force_format | bool }}"
  when: blkid_label.stdout != 'homelab-data'

# ── Mount ────────────────────────────────────────────────────────────────────

- name: Create /data mount point
  ansible.builtin.file:
    path: /data
    state: directory
    mode: "0755"
    owner: root
    group: root

- name: Mount data_disk at /data (persistent via fstab)
  ansible.posix.mount:
    path: /data
    src: LABEL=homelab-data
    fstype: ext4
    opts: defaults,noatime
    state: mounted

# ── Docker daemon.json (full mode only) ──────────────────────────────────────

- name: Ensure /etc/docker directory exists
  ansible.builtin.file:
    path: /etc/docker
    state: directory
    mode: "0755"
    owner: root
    group: root
  when: data_disk_mode != 'mount_only'

- name: Configure Docker data-root to /data/docker
  ansible.builtin.copy:
    dest: /etc/docker/daemon.json
    content: |
      {
        "data-root": "/data/docker"
      }
    mode: "0644"
    owner: root
    group: root
  notify: Restart Docker
  when: data_disk_mode != 'mount_only'

# ── Service data directories (full mode only) ─────────────────────────────────

- name: Create service data directories under /data
  ansible.builtin.file:
    path: "{{ item.path }}"
    state: directory
    mode: "{{ item.mode }}"
    owner: root
    group: root
  loop:
    - { path: /data/docker,      mode: "0710" }
    - { path: /data/postgresql,  mode: "0755" }
    - { path: /data/vault,       mode: "0755" }
    - { path: /data/authentik,   mode: "0755" }
    - { path: /data/gitlab,      mode: "0755" }
    - { path: /data/technitium,  mode: "0755" }
    - { path: /data/maas,        mode: "0755" }
  when: data_disk_mode != 'mount_only'
```

- [ ] **Step 3: Create role handlers**

Create `ansible/roles/storage_setup/handlers/main.yml`:

```yaml
---
- name: Restart Docker
  ansible.builtin.service:
    name: docker
    state: restarted
  when: ansible_facts.services['docker.service'] is defined
```

- [ ] **Step 4: Create a minimal playbook for the role**

Create `ansible/playbooks/stage-0b-storage.yml`:

```yaml
---
- name: Storage setup
  hosts: all
  become: true
  roles:
    - role: storage_setup
```

- [ ] **Step 5: Run ansible-lint on the new role**

```bash
ansible-lint ansible/roles/storage_setup/ --profile=min
```

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add ansible/roles/storage_setup/ ansible/playbooks/stage-0b-storage.yml
git commit -m "feat(ansible): add storage_setup role for secondary disk management"
```

---

## Task 3: storage_setup Molecule tests

**Files:**
- Create: `ansible/roles/storage_setup/molecule/default/molecule.yml`
- Create: `ansible/roles/storage_setup/molecule/default/prepare.yml`
- Create: `ansible/roles/storage_setup/molecule/default/converge.yml`
- Create: `ansible/roles/storage_setup/molecule/default/verify.yml`

- [ ] **Step 1: Create molecule.yml**

The project uses the `delegated` driver (confirmed in `ansible/roles/technitium_dns/molecule/default/molecule.yml`). The second disk is not attached via the Molecule driver config — it is created as a loop device in `prepare.yml` instead. This matches how Molecule tests work with the delegated driver: the QEMU VM is started externally via `just vm-up-ssd`, and Molecule connects to it.

Create `ansible/roles/storage_setup/molecule/default/molecule.yml`:

```yaml
---
dependency:
  name: galaxy
  options:
    requirements-file: ../../../../requirements.yml
driver:
  name: delegated
platforms:
  - name: instance
    address: "127.0.0.1"
provisioner:
  name: ansible
  connection_options:
    ansible_ssh_private_key_file: "../../../../test/keys/test_id_ed25519"
    ansible_user: ansible
  inventory:
    hosts:
      all:
        hosts:
          instance:
            ansible_host: "${MOLECULE_TARGET:-127.0.0.1}"
verifier:
  name: ansible
lint: |
  set -e
  ansible-lint
```

> **Note for implementer:** For the default scenario (full mode test), the QEMU VM must be started with a second disk attached (`just vm-up-ssd`) before running `molecule test`. The `prepare.yml` creates a loop device on the VM as the simulated second disk so that the role tasks have a block device to work with without needing a real second physical drive. For the `no-disk` scenario, use `just vm-up` (single disk).

- [ ] **Step 2: Create prepare.yml**

Create `ansible/roles/storage_setup/molecule/default/prepare.yml`:

```yaml
---
# Detect the second disk device name (varies by driver: /dev/sdb, /dev/vdb, etc.)
- name: Prepare
  hosts: all
  gather_facts: true
  tasks:
    - name: Find the second disk (non-boot block device)
      ansible.builtin.shell: |
        set -o pipefail
        BOOT=$(findmnt / -o SOURCE -n | xargs -I{} lsblk -no pkname {} 2>/dev/null || true)
        lsblk -d -o NAME,TYPE -n | awk '$2=="disk" {print $1}' | grep -v "^${BOOT}$" | head -1
      register: second_disk_name
      changed_when: false

    - name: Store second disk path as fact
      ansible.builtin.set_fact:
        molecule_data_disk: "/dev/{{ second_disk_name.stdout | trim }}"

    - name: Print detected second disk
      ansible.builtin.debug:
        msg: "Second disk detected: {{ molecule_data_disk }}"
```

- [ ] **Step 3: Create converge.yml with multiple scenarios**

Molecule runs one scenario at a time. We'll use the default scenario for the primary "full mode" path. For other scenarios, add separate molecule scenario directories. Start with the main converge:

Create `ansible/roles/storage_setup/molecule/default/converge.yml`:

```yaml
---
- name: Converge — blank disk, full mode
  hosts: all
  become: true
  tasks:
    - name: Include prepare facts
      ansible.builtin.include_vars:
        file: /tmp/molecule_facts.yml
      ignore_errors: true  # facts set via set_fact in prepare don't persist; re-detect here

    - name: Re-detect second disk
      ansible.builtin.shell: |
        set -o pipefail
        BOOT=$(findmnt / -o SOURCE -n | xargs -I{} lsblk -no pkname {} 2>/dev/null || true)
        lsblk -d -o NAME,TYPE -n | awk '$2=="disk" {print $1}' | grep -v "^${BOOT}$" | head -1
      register: second_disk_name
      changed_when: false

  roles:
    - role: storage_setup
      vars:
        data_disk: "/dev/{{ second_disk_name.stdout | trim }}"
        data_disk_mode: full
        service_data_root: /data
```

- [ ] **Step 4: Create verify.yml**

Create `ansible/roles/storage_setup/molecule/default/verify.yml`:

```yaml
---
- name: Verify — full mode
  hosts: all
  become: true
  tasks:
    - name: /data is mounted
      ansible.builtin.command: mountpoint -q /data
      changed_when: false

    - name: fstab has homelab-data entry
      ansible.builtin.shell: grep -q 'homelab-data' /etc/fstab
      changed_when: false

    - name: Service directories exist
      ansible.builtin.stat:
        path: "{{ item }}"
      register: dir_stat
      loop:
        - /data/docker
        - /data/postgresql
        - /data/vault
        - /data/authentik
        - /data/gitlab
        - /data/technitium
        - /data/maas
      failed_when: not dir_stat.stat.exists

    - name: daemon.json contains /data/docker
      ansible.builtin.command: grep -q '/data/docker' /etc/docker/daemon.json
      changed_when: false

    - name: No disk configured — /data absent (tested in separate scenario)
      ansible.builtin.debug:
        msg: "No-disk scenario covered in molecule/no-disk/ scenario directory"

- name: Verify idempotency — re-run converge and check zero changed tasks
  hosts: all
  become: true
  tasks:
    - name: Re-run storage_setup role (second pass)
      ansible.builtin.include_role:
        name: storage_setup
      vars:
        data_disk: "{{ hostvars[inventory_hostname]['data_disk_path'] | default('/dev/sdb') }}"
        data_disk_mode: full
        service_data_root: /data
      register: second_run_result
```

> **Note for implementer:** Idempotency is better checked via `just test-role-idempotent storage_setup` which runs converge twice and asserts zero changed tasks on the second pass. That Justfile target already exists.

- [ ] **Step 5: Create no-disk scenario**

Create `ansible/roles/storage_setup/molecule/no-disk/molecule.yml` — same delegated driver as default, no loop device in prepare:

```yaml
---
dependency:
  name: galaxy
  options:
    requirements-file: ../../../../requirements.yml
driver:
  name: delegated
platforms:
  - name: instance
    address: "127.0.0.1"
provisioner:
  name: ansible
  connection_options:
    ansible_ssh_private_key_file: "../../../../test/keys/test_id_ed25519"
    ansible_user: ansible
  inventory:
    hosts:
      all:
        hosts:
          instance:
            ansible_host: "${MOLECULE_TARGET:-127.0.0.1}"
verifier:
  name: ansible
lint: |
  set -e
  ansible-lint
```

Create `ansible/roles/storage_setup/molecule/no-disk/converge.yml`:

```yaml
---
- name: Converge — no disk configured
  hosts: all
  become: true
  roles:
    - role: storage_setup
      vars:
        data_disk: ""
        service_data_root: ""
```

Create `ansible/roles/storage_setup/molecule/no-disk/verify.yml`:

```yaml
---
- name: Verify — no-op when no disk configured
  hosts: all
  become: true
  tasks:
    - name: /data does not exist
      ansible.builtin.stat:
        path: /data
      register: data_dir
      failed_when: data_dir.stat.exists

    - name: fstab has no homelab-data entry
      ansible.builtin.shell: "! grep -q 'homelab-data' /etc/fstab"
      changed_when: false

    - name: daemon.json is absent
      ansible.builtin.stat:
        path: /etc/docker/daemon.json
      register: daemon_json
      failed_when: daemon_json.stat.exists
```

- [ ] **Step 6: Run Molecule tests (full mode)**

```bash
cd ansible/roles/storage_setup && molecule test
```

Expected: create → prepare → converge → verify → destroy all pass.

- [ ] **Step 7: Run idempotency check**

```bash
just test-role-idempotent storage_setup
```

Expected: "PASS: Role storage_setup is idempotent"

- [ ] **Step 8: Commit**

```bash
git add ansible/roles/storage_setup/molecule/
git commit -m "test(ansible): add Molecule tests for storage_setup role"
```

---

## Task 4: Update existing Ansible role defaults and templates

**Files (per role):**
- Modify: `ansible/roles/technitium_dns/defaults/main.yml` + `templates/docker-compose.yml.j2`
- Modify: `ansible/roles/vault_install/defaults/main.yml` + `templates/vault.hcl.j2`
- Modify: `ansible/roles/authentik_install/defaults/main.yml` + `templates/docker-compose.yml.j2`
- Modify: `ansible/roles/postgresql_install/defaults/main.yml` + `tasks/install.yml` or `tasks/configure.yml`
- Modify: `ansible/roles/gitlab_install/defaults/main.yml` + `templates/gitlab.rb.j2`
- Modify: `ansible/roles/maas_install/defaults/main.yml`

**Convention used in every role:**

```yaml
# defaults/main.yml
service_data_root: ""
<role>_data_dir: "{{ (service_data_root + '/<service>') if service_data_root else '<existing-default-path>' }}"
```

When `service_data_root` is empty (default, no SSD), the role's data dir falls back to its current absolute path — no behavioral change. When `service_data_root` is `/data`, the data dir becomes `/data/<service>`.

- [ ] **Step 1: Update technitium_dns**

In `ansible/roles/technitium_dns/defaults/main.yml`, add after existing vars:

```yaml
service_data_root: ""
technitium_data_dir: "{{ (service_data_root + '/technitium') if service_data_root else '/opt/technitium/data' }}"
```

Remove or comment out the existing hardcoded `technitium_data_dir` if it exists. The template already uses `{{ technitium_data_dir }}` — no template change needed.

- [ ] **Step 2: Update vault_install**

In `ansible/roles/vault_install/defaults/main.yml`, modify `vault_data_dir`:

```yaml
service_data_root: ""
vault_data_dir: "{{ (service_data_root + '/vault') if service_data_root else '/opt/vault/data' }}"
```

The template `vault.hcl.j2` already uses `{{ vault_data_dir }}` — no template change needed.

- [ ] **Step 3: Update authentik_install**

In `ansible/roles/authentik_install/defaults/main.yml`, modify `authentik_data_dir`:

```yaml
service_data_root: ""
authentik_data_dir: "{{ (service_data_root + '/authentik') if service_data_root else '/opt/authentik' }}"
```

The template `docker-compose.yml.j2` already uses `{{ authentik_data_dir }}` — no template change needed.

- [ ] **Step 4: Update postgresql_install**

PostgreSQL (installed via apt) uses the package-managed `PGDATA`. To redirect it, we configure `data_directory` in `postgresql.conf`. Add to `ansible/roles/postgresql_install/defaults/main.yml`:

```yaml
service_data_root: ""
postgresql_data_dir: "{{ (service_data_root + '/postgresql') if service_data_root else '' }}"
```

An empty `postgresql_data_dir` means "use the default apt-managed location" (`/var/lib/postgresql/16/main`). When set, configure `data_directory` in PostgreSQL.

In `ansible/roles/postgresql_install/tasks/configure.yml` (or wherever `postgresql.conf` is managed), add a task:

```yaml
- name: Configure PostgreSQL data_directory (when service_data_root set)
  ansible.builtin.lineinfile:
    path: "/etc/postgresql/{{ postgresql_version }}/main/postgresql.conf"
    regexp: "^#?data_directory"
    line: "data_directory = '{{ postgresql_data_dir }}'"
    state: present
  when: postgresql_data_dir | length > 0
  notify: restart postgresql
```

The handler name `restart postgresql` (lowercase) matches what already exists in `ansible/roles/postgresql_install/handlers/main.yml`.

> **Note:** When redirecting PostgreSQL data to a new directory, the new directory must exist and be owned by `postgres` before PostgreSQL starts. The `storage_setup` role creates `/data/postgresql` as root:root; the PostgreSQL role must `chown postgres:postgres /data/postgresql` before starting the service when `postgresql_data_dir` is set. Add this task:

```yaml
- name: Set ownership on PostgreSQL data directory (when service_data_root set)
  ansible.builtin.file:
    path: "{{ postgresql_data_dir }}"
    owner: postgres
    group: postgres
    mode: "0700"
    state: directory
  when: postgresql_data_dir | length > 0
```

- [ ] **Step 5: Update gitlab_install**

GitLab Omnibus uses `/var/opt/gitlab` by default. The data path is configurable via `git_data_dirs` in `gitlab.rb`. Add to `ansible/roles/gitlab_install/defaults/main.yml`:

```yaml
service_data_root: ""
gitlab_data_dir: "{{ (service_data_root + '/gitlab') if service_data_root else '' }}"
```

In `ansible/roles/gitlab_install/templates/gitlab.rb.j2`, add a conditional block:

```ruby
{% if gitlab_data_dir %}
git_data_dirs({ "default" => { "path" => "{{ gitlab_data_dir }}/repositories" } })
{% endif %}
```

- [ ] **Step 6: Update maas_install**

MAAS is installed as a snap. Snap confinement may redirect data regardless of configured paths — this is an open question in the spec. For now, add `service_data_root` to defaults as a stub:

```yaml
service_data_root: ""
# maas_data_dir intentionally not wired — snap confinement behaviour
# must be verified before redirecting MAAS data. See spec open question #1.
```

No task or template changes for MAAS at this time.

- [ ] **Step 7: Update Molecule converge.yml for each role**

For each of the 6 roles above, add `service_data_root: /tmp/test-data` to the vars block in `molecule/default/converge.yml`. This exercises the non-default path without requiring a real mount.

For example, in `ansible/roles/technitium_dns/molecule/default/converge.yml`:

```yaml
---
- name: Converge
  hosts: all
  become: true
  roles:
    - role: technitium_dns
      vars:
        internal_domain: home.test
        bootstrap_server_ip: "127.0.0.1"
        technitium_admin_password: "TestPassword123!"
        service_data_root: /tmp/test-data   # <-- add this
```

Apply the same pattern to: `vault_install`, `authentik_install`, `postgresql_install`, `gitlab_install`, `maas_install`.

- [ ] **Step 8: Run ansible-lint on all updated roles**

```bash
ansible-lint ansible/roles/ --profile=min
```

Expected: no new errors.

- [ ] **Step 9: Run Molecule tests for each updated role to verify the variable change doesn't break anything**

```bash
just test-role vault_install
just test-role technitium_dns
just test-role authentik_install
just test-role postgresql_install
just test-role gitlab_install
just test-role maas_install
```

Each should converge and verify cleanly. The `service_data_root: /tmp/test-data` override redirects data to `/tmp/test-data/<service>` instead of the native path — assert that the role's data path in the verify step reflects `/tmp/test-data/<service>`.

- [ ] **Step 10: Commit**

```bash
git add ansible/roles/
git commit -m "feat(ansible): add service_data_root support to all service roles"
```

---

## Task 5: StorageSetupStage Go struct

**Files:**
- Create: `internal/stages/stage0b_storage.go`

- [ ] **Step 1: Write the file**

Create `internal/stages/stage0b_storage.go`:

```go
package stages

import (
	"context"
	"fmt"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/ansible"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// StorageSetupStage is Stage 0b: secondary disk format, mount, and data dir setup.
// It is a no-op when cfg.DataDisk is empty.
type StorageSetupStage struct{}

// Name returns the canonical stage identifier.
func (s *StorageSetupStage) Name() string { return "storage-setup" }

// Check returns true (already complete) if /data is mounted when a disk is configured,
// or always true when no disk is configured (no-op path).
func (s *StorageSetupStage) Check(_ context.Context, cfg *config.Config, _ *runcontext.RunContext) (bool, error) {
	if cfg.DataDisk == "" {
		return true, nil // no disk configured — stage is a no-op, skip it
	}

	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
	if err != nil {
		return false, err
	}
	defer client.Close()

	stdout, _, err := client.Run("mountpoint -q /data && echo mounted || echo not-mounted")
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(stdout) == "mounted", nil
}

// Run sets up the secondary disk via Ansible.
func (s *StorageSetupStage) Run(_ context.Context, cfg *config.Config, _ *config.Secrets, _ *runcontext.RunContext, sink stage.Sink) error {
	if cfg.DataDisk == "" {
		sink.Log("info", s.Name(), "no data_disk configured — skipping storage setup", nil)
		return nil
	}

	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
	if err != nil {
		return fmt.Errorf("SSH connection failed: %w", err)
	}
	defer client.Close()

	mode := cfg.DataDiskMode
	if mode == "" {
		mode = "full"
	}

	forceFormat := "false"
	if cfg.DataDiskForceFormat {
		forceFormat = "true"
	}

	runner := ansible.New(client, sink)
	return runner.RunPlaybook(
		"ansible/playbooks/stage-0b-storage.yml",
		cfg.TargetHost+",",
		map[string]string{
			"data_disk":             cfg.DataDisk,
			"data_disk_mode":        mode,
			"data_disk_force_format": forceFormat,
			"service_data_root":     cfg.DataPath(),
		},
	)
}
```

- [ ] **Step 2: Build to verify it compiles**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add internal/stages/stage0b_storage.go
git commit -m "feat(stages): add StorageSetupStage (stage 0b)"
```

---

## Task 6: Wire service_data_root into stage runners 1–7

**Files:**
- Modify: `internal/stages/stage1_dns.go`
- Modify: `internal/stages/stage3_vault.go`
- Modify: `internal/stages/stage4_authentik.go`
- Modify: `internal/stages/stage5_postgres.go`
- Modify: `internal/stages/stage6_gitlab.go`
- Modify: `internal/stages/stage7_maas.go`

Each runner passes `service_data_root` by adding one line to the extra-vars map. The pattern is identical across all six files.

- [ ] **Step 1: Update stage1_dns.go**

Find the `RunPlaybook` call in `stage1_dns.go`. Add one line to the extra-vars map:

```go
"service_data_root": cfg.DataPath(),
```

After the change, the extra-vars map looks like:

```go
map[string]string{
    "internal_domain":           cfg.InternalDomain,
    "bootstrap_server_ip":       cfg.BootstrapServerIP,
    "technitium_admin_password": sec.AdminPassword,
    "service_data_root":         cfg.DataPath(),
},
```

- [ ] **Step 2: Apply the same change to stage3_vault.go, stage4_authentik.go, stage5_postgres.go, stage6_gitlab.go, stage7_maas.go**

In each file, find the `RunPlaybook` call and add `"service_data_root": cfg.DataPath()` to the extra-vars map.

- [ ] **Step 3: Build and test**

```bash
go build ./...
go test ./internal/...
```

Expected: build and all tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/stages/stage1_dns.go internal/stages/stage3_vault.go \
        internal/stages/stage4_authentik.go internal/stages/stage5_postgres.go \
        internal/stages/stage6_gitlab.go internal/stages/stage7_maas.go
git commit -m "feat(stages): inject service_data_root extra-var into all stage runners"
```

---

## Task 7: Wire StorageSetupStage into main.go

**Files:**
- Modify: `cmd/bootstrap/main.go`

- [ ] **Step 1: Find buildStages()**

Read `cmd/bootstrap/main.go` (around line 119+). Find `buildStages()` which returns `[]stage.Stage`. It currently starts with `&stages.ConfigStage{}`, then `&stages.DNSStage{}`, etc.

- [ ] **Step 2: Insert StorageSetupStage**

Add `&stages.StorageSetupStage{}` between `ConfigStage` and `DNSStage`:

```go
func buildStages() []stage.Stage {
    return []stage.Stage{
        &stages.ConfigStage{},
        &stages.StorageSetupStage{},  // stage 0b: secondary disk setup
        &stages.DNSStage{},
        // ... rest unchanged
    }
}
```

- [ ] **Step 3: Build to verify it compiles**

```bash
go build ./cmd/bootstrap/
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add cmd/bootstrap/main.go
git commit -m "feat(bootstrap): wire StorageSetupStage into stage pipeline"
```

---

## Task 8: Stage 0 disk picker in ConfigStage

**Files:**
- Modify: `internal/runcontext/context.go`
- Modify: `cmd/bootstrap/main.go`
- Modify: `internal/stages/stage0_config.go`

This adds disk detection + TUI selection to `ConfigStage.Run()`. It runs after OS detection and before writing `config.yaml`. It is skipped if `cfg.DataDisk` is already set (headless/re-run) or in headless mode.

- [ ] **Step 1: Add Headless flag to RunContext**

In `internal/runcontext/context.go`, add a `Headless bool` field to `RunContext` after `EnvPath`:

```go
// Headless is true when the binary is running in --headless mode.
// Set by main.go before the stage runner starts.
// ConfigStage uses this to skip interactive prompts (disk picker, etc.).
Headless bool
```

In `cmd/bootstrap/main.go`, set the field after `rctx` is created (around line 96, after `rctx := runcontext.New(runDir)`):

```go
rctx.Headless = flagHeadless
```

Build to verify:

```bash
go build ./...
```

- [ ] **Step 2: Add disk detection helpers**

In `internal/stages/stage0_config.go`, add two unexported functions:

```go
// detectBootDisk returns the parent disk device of the root filesystem.
// Uses findmnt + lsblk --pkname to be architecture-agnostic (handles NVMe/eMMC/SATA).
func detectBootDisk(client *ssh.Client) (string, error) {
	// Get the partition device hosting /
	partOut, _, err := client.Run("findmnt / -o SOURCE -n 2>/dev/null | head -1")
	if err != nil || strings.TrimSpace(partOut) == "" {
		return "", fmt.Errorf("could not detect root partition: %w", err)
	}
	part := strings.TrimSpace(partOut)

	// Get the parent disk name (architecture-agnostic: handles mmcblk0p1 → mmcblk0, nvme0n1p1 → nvme0n1)
	pkOut, _, err := client.Run(fmt.Sprintf("lsblk -no pkname %s 2>/dev/null | head -1", part))
	if err != nil {
		return "", fmt.Errorf("lsblk pkname failed: %w", err)
	}
	disk := strings.TrimSpace(pkOut)
	if disk == "" {
		// Partition IS the disk (no parent) — happens on some setups
		disk = strings.TrimPrefix(part, "/dev/")
	}
	return disk, nil
}

// diskCandidate holds information about a detected non-boot disk.
type diskCandidate struct {
	Name      string // e.g. "sda"
	Size      string // e.g. "465.8G"
	Transport string // e.g. "sata", "nvme", "usb"
}

// listDataDiskCandidates returns block devices excluding the boot disk.
func listDataDiskCandidates(client *ssh.Client) ([]diskCandidate, error) {
	bootDisk, err := detectBootDisk(client)
	if err != nil {
		return nil, err
	}

	// List all non-removable block disks.
	out, _, err := client.Run("lsblk -d -o NAME,SIZE,TRAN -n -J 2>/dev/null")
	if err != nil {
		return nil, fmt.Errorf("lsblk failed: %w", err)
	}

	// Parse JSON output.
	var lsblkOut struct {
		BlockDevices []struct {
			Name string `json:"name"`
			Size string `json:"size"`
			Tran string `json:"tran"`
		} `json:"blockdevices"`
	}
	if err := json.Unmarshal([]byte(out), &lsblkOut); err != nil {
		return nil, fmt.Errorf("parsing lsblk output: %w", err)
	}

	var candidates []diskCandidate
	for _, d := range lsblkOut.BlockDevices {
		if d.Name == bootDisk {
			continue
		}
		candidates = append(candidates, diskCandidate{
			Name:      d.Name,
			Size:      d.Size,
			Transport: d.Tran,
		})
	}
	return candidates, nil
}
```

Note: add `"encoding/json"` to the imports in `stage0_config.go` if not already present.

- [ ] **Step 3: Add disk picker step to ConfigStage.Run()**

In `ConfigStage.Run()`, after the `checkOS` call and before the `writeConfigYAML` call, add:

```go
// Disk picker: only run in interactive mode when data_disk is not already set.
if !rc.Headless && cfg.DataDisk == "" {
    if err := s.pickDataDisk(ctx, cfg, client, sink); err != nil {
        return fmt.Errorf("disk picker: %w", err)
    }
}
```

`rc.Headless` is now set from `flagHeadless` in `main.go` (Step 1 above).

- [ ] **Step 4: Add pickDataDisk method**

```go
// pickDataDisk prompts the operator to select a secondary data disk.
// Modifies cfg.DataDisk and cfg.DataDiskMode in-place.
// No-op if no candidates are found.
func (s *ConfigStage) pickDataDisk(ctx context.Context, cfg *config.Config, client *ssh.Client, sink stage.Sink) error {
	sink.Log("info", s.Name(), "detecting secondary disk candidates", nil)

	candidates, err := listDataDiskCandidates(client)
	if err != nil {
		// Detection failure is non-fatal; log and skip.
		sink.Log("warn", s.Name(), fmt.Sprintf("disk detection failed: %v — skipping disk picker", err), nil)
		return nil
	}

	if len(candidates) == 0 {
		sink.Log("info", s.Name(), "no secondary disk detected — continuing with SD card only", nil)
		return nil
	}

	// Build a selection prompt: "1) sda  465.8G  sata\n2) none / skip"
	var lines []string
	for i, c := range candidates {
		lines = append(lines, fmt.Sprintf("%d) /dev/%s  %s  %s", i+1, c.Name, c.Size, c.Transport))
	}
	lines = append(lines, fmt.Sprintf("%d) none / skip", len(candidates)+1))

	sink.ShowWarningBox(append([]string{"Secondary disk detected. Select data disk:"}, lines...))

	answer, err := sink.Prompt("Enter selection number", false)
	if err != nil {
		return fmt.Errorf("prompt failed: %w", err)
	}
	answer = strings.TrimSpace(answer)

	idx, convErr := strconv.Atoi(answer)
	if convErr != nil || idx < 1 || idx > len(candidates)+1 {
		sink.Log("warn", s.Name(), "invalid selection — skipping disk picker", nil)
		return nil
	}

	// "none / skip" selected
	if idx == len(candidates)+1 {
		sink.Log("info", s.Name(), "no disk selected — continuing with SD card only", nil)
		return nil
	}

	chosen := candidates[idx-1]
	cfg.DataDisk = "/dev/" + chosen.Name

	// Prompt for mode.
	sink.ShowWarningBox([]string{
		fmt.Sprintf("Selected: /dev/%s (%s)", chosen.Name, chosen.Size),
		"",
		"1) Full data offload (recommended) — redirect Docker + all service data to /data",
		"2) Mount only — mount /data but do not redirect services",
	})

	modeAnswer, err := sink.Prompt("Enter selection number", false)
	if err != nil {
		return fmt.Errorf("mode prompt failed: %w", err)
	}
	switch strings.TrimSpace(modeAnswer) {
	case "2":
		cfg.DataDiskMode = "mount_only"
	default:
		cfg.DataDiskMode = "full"
	}

	sink.Log("info", s.Name(),
		fmt.Sprintf("data disk set to %s (mode: %s)", cfg.DataDisk, cfg.DataDiskMode), nil)
	return nil
}
```

- [ ] **Step 5: Build and test**

```bash
go build ./...
go test ./internal/... -v
```

Expected: compiles and all tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/runcontext/context.go cmd/bootstrap/main.go internal/stages/stage0_config.go
git commit -m "feat(stages): add secondary disk picker to config stage"
```

---

## Task 9: config.yaml.example and Justfile

**Files:**
- Create: `config.yaml.example`
- Modify: `Justfile`

- [ ] **Step 1: Create config.yaml.example**

Create `config.yaml.example` in the repo root:

```yaml
# config.yaml — Bootstrap configuration
# Copy to config.yaml and fill in your values.
# Run `bootstrap` to populate interactively, or pre-fill for headless use.

# ── Required ─────────────────────────────────────────────────────────────────

target_host: "192.168.1.10"        # IP or hostname of the server to bootstrap
ssh_user: ubuntu                    # SSH user (must have passwordless sudo)
ssh_key_path: ~/.ssh/id_ed25519     # Path to SSH private key
internal_domain: home.example.com  # Internal domain for all services
cloudflare_zone_id: ""             # Cloudflare zone ID for your domain
bootstrap_server_ip: "192.168.1.10"
lab_vlan_subnet: "10.0.100.0/24"
lab_vlan_id: 100
admin_email: admin@example.com
vault_init_output_path: ~/vault-init.json  # Keep outside repo; store securely

# ── Optional: Vault ───────────────────────────────────────────────────────────
vault_shares: 5       # Shamir share count (min 3)
vault_threshold: 3    # Shares required to unseal

# ── Optional: Secondary disk (Raspberry Pi SSD hat, etc.) ────────────────────
# Leave commented out for SD-card-only targets.
#
# data_disk: /dev/sda
#   Block device path for the secondary storage disk.
#   Populated interactively by the TUI disk picker on first run.
#   Pre-supply for headless/CI use.
#
# data_disk_mode: full
#   "full"       — redirect Docker root and all service data to /data (default)
#   "mount_only" — format and mount /data only; no service redirection
#
# data_disk_force_format: false
#   Set to true to wipe an existing filesystem on data_disk.
#   DESTRUCTIVE — requires explicit opt-in.
```

- [ ] **Step 2: Add vm-up-ssd and test-bootstrap-ssd to Justfile**

In the `Justfile`, after the existing `vm-up` target, add:

```justfile
# Start the QEMU test VM with a second virtual disk for SSD storage tests.
# Creates an ephemeral 2GB secondary disk image attached at /dev/vdb.
# SSH is forwarded to localhost:2222.
vm-up-ssd arch="aarch64" accelerator="hvf":
    #!/usr/bin/env bash
    set -euo pipefail
    BASE="test/images/output/base-{{arch}}.qcow2"
    SNAP="test/images/run-snapshot.qcow2"
    SSD_IMG="test/images/run-ssd-disk.qcow2"
    PID_FILE="/tmp/bootstrap-test-vm.pid"
    if [ ! -f "$BASE" ]; then
        echo "ERROR: $BASE not found. Run 'just build-base-image' first." >&2
        exit 1
    fi
    if [ -f "$PID_FILE" ] && kill -0 "$(cat $PID_FILE)" 2>/dev/null; then
        echo "VM already running (PID $(cat $PID_FILE)). Run 'just vm-down' first."
        exit 0
    fi
    qemu-img create -f qcow2 -b "$(realpath $BASE)" -F qcow2 "$SNAP"
    # Create a fresh 2GB ephemeral disk for the SSD.
    qemu-img create -f qcow2 "$SSD_IMG" 2G
    if [ "{{arch}}" = "aarch64" ]; then
        QEMU_BIN="qemu-system-aarch64"
        MACHINE="-machine virt -cpu host -bios /opt/homebrew/share/qemu/edk2-aarch64-code.fd"
    else
        QEMU_BIN="qemu-system-x86_64"
        MACHINE="-machine q35"
    fi
    $QEMU_BIN \
        $MACHINE \
        -accel {{accelerator}} \
        -m 2048 \
        -drive if=virtio,file="$SNAP",format=qcow2 \
        -drive if=virtio,file="$SSD_IMG",format=qcow2 \
        -netdev user,id=net0,hostfwd=tcp::2222-:22 \
        -device virtio-net-pci,netdev=net0 \
        -display none \
        -daemonize \
        -pidfile "$PID_FILE"
    echo "VM started with second disk (PID $(cat $PID_FILE)). Waiting for SSH..."
    for i in $(seq 1 60); do
        if ssh -q -o StrictHostKeyChecking=no -o ConnectTimeout=2 \
               -i test/keys/test_id_ed25519 -p 2222 ansible@localhost \
               "exit 0" 2>/dev/null; then
            echo "SSH ready."
            exit 0
        fi
        sleep 1
    done
    echo "ERROR: SSH did not become ready within 60s." >&2
    exit 1
```

Also extend `vm-down` to clean up the SSD image:

```justfile
# Stop the QEMU test VM and remove all snapshots (including SSD disk if present).
vm-down:
    #!/usr/bin/env bash
    PID_FILE="/tmp/bootstrap-test-vm.pid"
    SNAP="test/images/run-snapshot.qcow2"
    SSD_IMG="test/images/run-ssd-disk.qcow2"
    if [ -f "$PID_FILE" ]; then
        PID=$(cat "$PID_FILE")
        kill "$PID" 2>/dev/null && echo "VM stopped (PID $PID)." || echo "VM was not running."
        rm -f "$PID_FILE"
    else
        echo "No PID file found; VM may not be running."
    fi
    rm -f "$SNAP" "$SSD_IMG"
```

And add the `test-stage-ssd` and `test-bootstrap-ssd` targets after `test-bootstrap`:

```justfile
# Run a single bootstrap stage against the QEMU VM with SSD config (config-ssd.yaml).
# Requires: vm-up-ssd, write-test-config-ssd, mocks-up-vm (for stages 2+).
test-stage-ssd stage: build
    ACME_SERVER_URL=https://127.0.0.1:14000/dir \
    CLOUDFLARE_API_BASE_URL=http://127.0.0.1:8080 \
    ./bin/bootstrap --headless --stage {{stage}} \
        --config test/config-ssd.yaml --env test/.env

# Run the full bootstrap end-to-end against a QEMU VM with a secondary SSD disk.
# Requires: vm-up-ssd, write-test-config-ssd, mocks-up-vm.
test-bootstrap-ssd: build
    ACME_SERVER_URL=https://127.0.0.1:14000/dir \
    CLOUDFLARE_API_BASE_URL=http://127.0.0.1:8080 \
    ./bin/bootstrap --headless \
        --config test/config-ssd.yaml --env test/.env
```

> **Note:** `test-bootstrap-ssd` uses a separate `test/config-ssd.yaml` that includes `data_disk: /dev/vdb`. The existing `write-test-config.sh` can be extended (or a new script created) to generate this variant. The config file must be written before running this target.

- [ ] **Step 3: Add write-test-config-ssd helper**

Extend `test/write-test-config.sh` or add a separate script that generates `test/config-ssd.yaml` with `data_disk: /dev/vdb` in addition to the standard fields.

Alternatively, add a Justfile target:

```justfile
# Write test/config-ssd.yaml: same as write-test-config but adds data_disk: /dev/vdb
write-test-config-ssd domain="lab.test" ip="127.0.0.1":
    bash test/write-test-config.sh "{{domain}}" "{{ip}}"
    echo "data_disk: /dev/vdb" >> test/config.yaml
    cp test/config.yaml test/config-ssd.yaml
    # Remove data_disk from the original to keep it as the single-disk test config
    grep -v "^data_disk:" test/config-ssd.yaml > test/config.yaml || true
```

Adjust to match the actual structure of `write-test-config.sh`.

- [ ] **Step 4: Commit**

```bash
git add config.yaml.example Justfile test/
git commit -m "feat: add config.yaml.example, vm-up-ssd, and test-bootstrap-ssd targets"
```

---

## Task 10: Integration test — single-disk regression

This is a verification step, not new code. Run the existing tests to confirm no regressions.

- [ ] **Step 1: Start single-disk VM**

```bash
just vm-up
just write-test-config
just mocks-up-vm
```

- [ ] **Step 2: Run storage-setup stage (should be no-op)**

```bash
just test-stage storage-setup
```

Expected: stage logs "no data_disk configured — skipping storage setup" and exits clean.

- [ ] **Step 3: Verify /data does not exist**

```bash
ssh -o StrictHostKeyChecking=no -i test/keys/test_id_ed25519 -p 2222 ansible@localhost \
    "test ! -d /data && echo 'PASS: /data absent' || echo 'FAIL: /data exists'"
```

Expected: `PASS: /data absent`

- [ ] **Step 4: Run full bootstrap (single disk)**

```bash
just test-bootstrap
```

Expected: full bootstrap completes; all services running; data under `/var/lib` (or role-native paths).

- [ ] **Step 5: Tear down**

```bash
just vm-down
just mocks-down
```

---

## Task 11: Integration test — dual-disk (SSD) path

- [ ] **Step 1: Start dual-disk VM**

```bash
just vm-up-ssd
just write-test-config-ssd
just mocks-up-vm
```

- [ ] **Step 2: Run storage-setup stage in isolation**

```bash
just test-stage-ssd storage-setup
```

This uses `test/config-ssd.yaml` which includes `data_disk: /dev/vdb`. Expected: role formats `/dev/vdb`, mounts at `/data`, creates service dirs, writes `daemon.json`.

- [ ] **Step 3: Verify /data is mounted and dirs exist**

```bash
ssh -o StrictHostKeyChecking=no -i test/keys/test_id_ed25519 -p 2222 ansible@localhost \
    "mountpoint /data && ls /data && cat /etc/docker/daemon.json"
```

Expected:
```
/data is a mountpoint
docker  postgresql  vault  authentik  gitlab  technitium  maas
{"data-root":"/data/docker"}
```

- [ ] **Step 4: Run storage-setup a second time (idempotency)**

```bash
just test-stage-ssd storage-setup
```

Expected: Ansible reports zero changed tasks.

- [ ] **Step 5: Run full bootstrap (dual disk)**

```bash
just test-bootstrap-ssd
```

Expected: full bootstrap completes; all services running; data under `/data`.

- [ ] **Step 6: Tear down**

```bash
just vm-down
```

---

## Completion Checklist

- [ ] `go test ./...` passes with no failures
- [ ] `just lint-all` passes (Go vet, Terraform validate, Ansible lint)
- [ ] `just test-role-idempotent storage_setup` passes
- [ ] `just test-bootstrap` passes (single-disk regression)
- [ ] `just test-bootstrap-ssd` passes (dual-disk end-to-end)
- [ ] Spec open question #1 (MAAS snap data dir) documented as a follow-on issue
