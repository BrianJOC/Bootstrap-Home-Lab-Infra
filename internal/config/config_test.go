package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validRunConfig returns a RunConfig with all required fields populated.
func validRunConfig() *RunConfig {
	return &RunConfig{
		Config: &Config{
			TargetHost:          "192.168.1.100",
			SSHUser:             "ubuntu",
			SSHKeyPath:          "/home/user/.ssh/id_ed25519",
			InternalDomain:      "home.example.com",
			CloudflareZoneID:    "abc123zone",
			BootstrapServerIP:   "192.168.1.100",
			LabVLANSubnet:       "10.0.0.0/24",
			LabGateway:          "10.0.0.1",
			AdminEmail:          "admin@example.com",
			VaultInitOutputPath: "/tmp/vault-init.json",
			VaultShares:         5,
			VaultThreshold:      3,
		},
		Secrets: &Secrets{
			CloudflareAPIToken: "cf-token-value",
			AdminPassword:      "s3cr3tpassword",
		},
	}
}

func TestValidate_Valid(t *testing.T) {
	if err := Validate(validRunConfig()); err != nil {
		t.Fatalf("expected no error for valid config, got: %v", err)
	}
}

func TestValidate_MissingRequiredFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*RunConfig)
		substr string
	}{
		{"missing target_host", func(rc *RunConfig) { rc.TargetHost = "" }, "target_host"},
		{"missing ssh_user", func(rc *RunConfig) { rc.SSHUser = "" }, "ssh_user"},
		{"missing ssh_key_path", func(rc *RunConfig) { rc.SSHKeyPath = "" }, "ssh_key_path"},
		{"missing internal_domain", func(rc *RunConfig) { rc.InternalDomain = "" }, "internal_domain"},
		{"missing cloudflare_zone_id", func(rc *RunConfig) { rc.CloudflareZoneID = "" }, "cloudflare_zone_id"},
		{"missing bootstrap_server_ip", func(rc *RunConfig) { rc.BootstrapServerIP = "" }, "bootstrap_server_ip"},
		{"missing lab_vlan_subnet", func(rc *RunConfig) { rc.LabVLANSubnet = "" }, "lab_vlan_subnet"},
		{"missing admin_email", func(rc *RunConfig) { rc.AdminEmail = "" }, "admin_email"},
		{"missing vault_init_output_path", func(rc *RunConfig) { rc.VaultInitOutputPath = "" }, "vault_init_output_path"},
		{"missing CF_DNS_API_TOKEN", func(rc *RunConfig) { rc.Secrets.CloudflareAPIToken = "" }, "CF_DNS_API_TOKEN"},
		{"missing BOOTSTRAP_ADMIN_PASSWORD", func(rc *RunConfig) { rc.Secrets.AdminPassword = "" }, "BOOTSTRAP_ADMIN_PASSWORD"},
		{"whitespace-only target_host", func(rc *RunConfig) { rc.TargetHost = "   " }, "target_host"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rc := validRunConfig()
			tc.mutate(rc)
			err := Validate(rc)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.substr)
			}
			if !strings.Contains(err.Error(), tc.substr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.substr)
			}
		})
	}
}

func TestValidate_InternalDomain(t *testing.T) {
	invalid := []string{
		"UPPERCASE.lab",
		"has space.lab",
		"has;semicolon.lab",
		"has$(injection).lab",
		"noext",
		".leading-dot.lab",
		"trailing-dot.lab.",
	}
	for _, domain := range invalid {
		t.Run("invalid_"+domain, func(t *testing.T) {
			rc := validRunConfig()
			rc.InternalDomain = domain
			if err := Validate(rc); err == nil {
				t.Errorf("expected error for invalid domain %q", domain)
			}
		})
	}

	valid := []string{"home.lab", "my-homelab.internal", "home.example.com", "a.b.c"}
	for _, domain := range valid {
		t.Run("valid_"+domain, func(t *testing.T) {
			rc := validRunConfig()
			rc.InternalDomain = domain
			if err := Validate(rc); err != nil {
				t.Errorf("unexpected error for valid domain %q: %v", domain, err)
			}
		})
	}
}

func TestValidate_NetworkAndVault(t *testing.T) {
	t.Run("invalid bootstrap IP", func(t *testing.T) {
		rc := validRunConfig()
		rc.BootstrapServerIP = "not-an-ip"
		err := Validate(rc)
		if err == nil || !strings.Contains(err.Error(), "bootstrap_server_ip") {
			t.Errorf("expected bootstrap_server_ip error, got: %v", err)
		}
	})

	t.Run("invalid VLAN CIDR", func(t *testing.T) {
		rc := validRunConfig()
		rc.LabVLANSubnet = "10.0.0.0/99"
		err := Validate(rc)
		if err == nil || !strings.Contains(err.Error(), "lab_vlan_subnet") {
			t.Errorf("expected lab_vlan_subnet error, got: %v", err)
		}
	})

	t.Run("threshold exceeds shares", func(t *testing.T) {
		rc := validRunConfig()
		rc.VaultShares = 3
		rc.VaultThreshold = 4
		err := Validate(rc)
		if err == nil || !strings.Contains(err.Error(), "vault_threshold") {
			t.Errorf("expected vault_threshold error, got: %v", err)
		}
	})

	t.Run("too few shares", func(t *testing.T) {
		rc := validRunConfig()
		rc.VaultShares = 2
		rc.VaultThreshold = 2
		err := Validate(rc)
		if err == nil || !strings.Contains(err.Error(), "vault_shares") {
			t.Errorf("expected vault_shares error, got: %v", err)
		}
	})
}

func TestDataPath(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"no disk", Config{}, ""},
		{"full mode", Config{DataDisk: "/dev/sda", DataDiskMode: "full"}, "/data"},
		{"unset mode defaults full", Config{DataDisk: "/dev/sda"}, "/data"},
		{"mount only", Config{DataDisk: "/dev/sda", DataDiskMode: "mount_only"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.DataPath(); got != tc.want {
				t.Errorf("DataPath() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidate_DataDisk(t *testing.T) {
	cases := []struct {
		name      string
		disk      string
		mode      string
		wantError bool
		substr    string
	}{
		{"valid sda", "/dev/sda", "full", false, ""},
		{"valid nvme", "/dev/nvme0n1", "full", false, ""},
		{"valid mmcblk", "/dev/mmcblk1", "full", false, ""},
		{"valid virtio", "/dev/vdb", "full", false, ""},
		{"invalid path", "/tmp/notadisk", "full", true, "data_disk"},
		{"mode without disk", "", "full", true, "data_disk_mode"},
		{"invalid mode", "/dev/sda", "wiped", true, "data_disk_mode"},
		{"no secondary disk", "", "", false, ""},
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
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseArch(t *testing.T) {
	cases := []struct {
		input string
		want  TargetArch
	}{
		{"x86_64", ArchAMD64},
		{"aarch64", ArchARM64},
		{"arm64", ArchARM64},
		{"  x86_64  ", ArchAMD64},
		{"i686", ArchUnknown},
		{"", ArchUnknown},
		{"armv7l", ArchUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			if got := ParseArch(tc.input); got != tc.want {
				t.Errorf("ParseArch(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestTargetArchMethods(t *testing.T) {
	cases := []struct {
		arch        TargetArch
		wantGo      string
		wantAnsible string
	}{
		{ArchAMD64, "amd64", "x86_64"},
		{ArchARM64, "arm64", "aarch64"},
		{ArchUnknown, "unknown", "unknown"},
	}
	for _, tc := range cases {
		if got := tc.arch.GoArch(); got != tc.wantGo {
			t.Errorf("%q.GoArch() = %q, want %q", tc.arch, got, tc.wantGo)
		}
		if got := tc.arch.AnsibleArch(); got != tc.wantAnsible {
			t.Errorf("%q.AnsibleArch() = %q, want %q", tc.arch, got, tc.wantAnsible)
		}
	}
}

func TestLoad_NonExistentConfigAppliesDefaults(t *testing.T) {
	rc, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), "")
	if err != nil {
		t.Fatalf("Load with nonexistent file: %v", err)
	}
	if rc.VaultShares != 5 || rc.VaultThreshold != 3 || rc.SSHUser != "ubuntu" {
		t.Errorf("unexpected defaults: shares=%d threshold=%d ssh_user=%q", rc.VaultShares, rc.VaultThreshold, rc.SSHUser)
	}
}

func TestLoad_ACMEServerURL(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		t.Setenv("ACME_SERVER_URL", "")
		rc, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), "")
		if err != nil {
			t.Fatal(err)
		}
		const want = "https://acme-v02.api.letsencrypt.org/directory"
		if rc.ACMEServerURL != want {
			t.Errorf("ACMEServerURL = %q, want %q", rc.ACMEServerURL, want)
		}
	})

	t.Run("override", func(t *testing.T) {
		t.Setenv("ACME_SERVER_URL", "https://pebble.test/dir")
		rc, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), "")
		if err != nil {
			t.Fatal(err)
		}
		if rc.ACMEServerURL != "https://pebble.test/dir" {
			t.Errorf("ACMEServerURL = %q", rc.ACMEServerURL)
		}
	})
}

func TestLoad_ACMETestTrustOverrides(t *testing.T) {
	t.Setenv("ACME_CA_CERT_URL", "http://harness.test/pebble.minica.pem")
	t.Setenv("ACME_ISSUING_CA_CERT_URL", "https://pebble:15000/roots/0")
	t.Setenv("ACME_SERVER_HOST_IP", "10.0.2.2")
	rc, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	if rc.ACMECACertURL != "http://harness.test/pebble.minica.pem" || rc.ACMEIssuingCACertURL != "https://pebble:15000/roots/0" || rc.ACMEServerHostIP != "10.0.2.2" {
		t.Fatalf("ACME trust overrides not loaded: API_CA=%q issuing_CA=%q host_ip=%q", rc.ACMECACertURL, rc.ACMEIssuingCACertURL, rc.ACMEServerHostIP)
	}
}

func TestLoad_ParsesValidYAML(t *testing.T) {
	content := `
target_host: 10.0.0.1
ssh_user: ansible
ssh_key_path: /home/test/.ssh/id_ed25519
internal_domain: homelab.test
cloudflare_zone_id: zone123
bootstrap_server_ip: 10.0.0.1
lab_vlan_subnet: 192.168.10.0/24
lab_gateway: 192.168.10.1
lab_vlan_id: 42
admin_email: ops@homelab.test
vault_shares: 7
vault_threshold: 4
vault_init_output_path: /tmp/vault.json
data_disk: /dev/vdb
data_disk_mode: full
`
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	rc, err := Load(path, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rc.TargetHost != "10.0.0.1" || rc.VaultShares != 7 || rc.LabVLANID != 42 {
		t.Errorf("unexpected parsed config: %+v", rc.Config)
	}
	if rc.DataDisk != "/dev/vdb" || rc.DataDiskMode != "full" {
		t.Errorf("unexpected data disk config: disk=%q mode=%q", rc.DataDisk, rc.DataDiskMode)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("target_host: [not: valid: yaml"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path, ""); err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestLoad_RejectsUnknownField(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("target_host: 192.168.1.10\nmade_up_option: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path, "")
	if err == nil || !strings.Contains(err.Error(), "made_up_option") {
		t.Fatalf("expected unknown-field error, got %v", err)
	}
}

func TestLoad_AppliesAcceptedDefaults(t *testing.T) {
	rc, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	if !rc.CertificateRenewalEnabled || !rc.ApplicationUpgradesEnabled || !rc.MonitoringEnabled {
		t.Fatalf("accepted enabled defaults were not applied: %+v", rc.Config)
	}
	if rc.BackupEnabled || rc.PagingEnabled || rc.MAASDHCPEnabled || rc.OSAutomaticReboot {
		t.Fatalf("safe disabled defaults were not applied: %+v", rc.Config)
	}
	if rc.BackupKeepDaily != 7 || rc.BackupKeepWeekly != 5 || rc.BackupKeepMonthly != 12 {
		t.Fatalf("unexpected retention defaults: %d/%d/%d", rc.BackupKeepDaily, rc.BackupKeepWeekly, rc.BackupKeepMonthly)
	}
}

func TestValidate_ConditionalBackupAndPagingSecrets(t *testing.T) {
	t.Run("backup requires repository", func(t *testing.T) {
		rc := validRunConfig()
		rc.BackupEnabled = true
		err := Validate(rc)
		if err == nil || !strings.Contains(err.Error(), "backup_repository") {
			t.Fatalf("expected repository error, got %v", err)
		}
	})

	t.Run("backup requires passwords", func(t *testing.T) {
		rc := validRunConfig()
		rc.BackupEnabled = true
		rc.BackupRepository = "/mnt/truenas/restic"
		err := Validate(rc)
		if err == nil || !strings.Contains(err.Error(), "RESTIC_PRIMARY_PASSWORD") {
			t.Fatalf("expected Restic password error, got %v", err)
		}
	})

	t.Run("paging requires destination", func(t *testing.T) {
		rc := validRunConfig()
		rc.PagingEnabled = true
		rc.PagingProvider = "discord"
		rc.PagingDiskWarningPercent = 85
		rc.PagingDiskCriticalPercent = 95
		err := Validate(rc)
		if err == nil || !strings.Contains(err.Error(), "PAGING_DESTINATION_URL") {
			t.Fatalf("expected paging destination error, got %v", err)
		}
	})
}

func TestValidate_MAASDHCPRequiresExplicitNetworkInputs(t *testing.T) {
	rc := validRunConfig()
	rc.MAASDHCPEnabled = true
	err := Validate(rc)
	if err == nil || !strings.Contains(err.Error(), "lab_vlan_interface") {
		t.Fatalf("expected interface error, got %v", err)
	}

	rc.LabVLANInterface = "enp1s0"
	rc.DHCPRangeLow = "10.0.0.100"
	rc.DHCPRangeHigh = "10.0.0.199"
	if err := Validate(rc); err != nil {
		t.Fatalf("valid explicit DHCP config rejected: %v", err)
	}
}

func TestLoad_SecretsFromEnv(t *testing.T) {
	t.Setenv("CF_DNS_API_TOKEN", "test-cf-token")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "test-admin-pass")

	rc, err := Load(filepath.Join(t.TempDir(), "missing.yaml"), "")
	if err != nil {
		t.Fatal(err)
	}
	if rc.Secrets.CloudflareAPIToken != "test-cf-token" {
		t.Errorf("CloudflareAPIToken = %q", rc.Secrets.CloudflareAPIToken)
	}
	if rc.Secrets.AdminPassword != "test-admin-pass" {
		t.Errorf("AdminPassword = %q", rc.Secrets.AdminPassword)
	}
}
