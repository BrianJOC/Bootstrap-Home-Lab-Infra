package config

import (
	"os"
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
			InternalDomain:      "home.lab",
			CloudflareZoneID:    "abc123zone",
			BootstrapServerIP:   "192.168.1.100",
			LabVLANSubnet:       "10.0.0.0/24",
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

// ── Validate ──────────────────────────────────────────────────────────────────

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
		{
			name:   "missing target_host",
			mutate: func(rc *RunConfig) { rc.TargetHost = "" },
			substr: "target_host",
		},
		{
			name:   "missing ssh_user",
			mutate: func(rc *RunConfig) { rc.SSHUser = "" },
			substr: "ssh_user",
		},
		{
			name:   "missing ssh_key_path",
			mutate: func(rc *RunConfig) { rc.SSHKeyPath = "" },
			substr: "ssh_key_path",
		},
		{
			name:   "missing internal_domain",
			mutate: func(rc *RunConfig) { rc.InternalDomain = "" },
			substr: "internal_domain",
		},
		{
			name:   "missing cloudflare_zone_id",
			mutate: func(rc *RunConfig) { rc.CloudflareZoneID = "" },
			substr: "cloudflare_zone_id",
		},
		{
			name:   "missing bootstrap_server_ip",
			mutate: func(rc *RunConfig) { rc.BootstrapServerIP = "" },
			substr: "bootstrap_server_ip",
		},
		{
			name:   "missing lab_vlan_subnet",
			mutate: func(rc *RunConfig) { rc.LabVLANSubnet = "" },
			substr: "lab_vlan_subnet",
		},
		{
			name:   "missing admin_email",
			mutate: func(rc *RunConfig) { rc.AdminEmail = "" },
			substr: "admin_email",
		},
		{
			name:   "missing vault_init_output_path",
			mutate: func(rc *RunConfig) { rc.VaultInitOutputPath = "" },
			substr: "vault_init_output_path",
		},
		{
			name:   "missing CF_DNS_API_TOKEN",
			mutate: func(rc *RunConfig) { rc.Secrets.CloudflareAPIToken = "" },
			substr: "CF_DNS_API_TOKEN",
		},
		{
			name:   "missing BOOTSTRAP_ADMIN_PASSWORD",
			mutate: func(rc *RunConfig) { rc.Secrets.AdminPassword = "" },
			substr: "BOOTSTRAP_ADMIN_PASSWORD",
		},
		{
			name:   "whitespace-only target_host",
			mutate: func(rc *RunConfig) { rc.TargetHost = "   " },
			substr: "target_host",
		},
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

func TestValidate_InvalidInternalDomain(t *testing.T) {
	cases := []string{
		"UPPERCASE.lab",
		"has space.lab",
		"has;semicolon.lab",
		"has$(injection).lab",
		"noext",      // single label, no dot
		".leading-dot.lab",
		"trailing-dot.lab.",
	}
	for _, domain := range cases {
		t.Run(domain, func(t *testing.T) {
			rc := validRunConfig()
			rc.InternalDomain = domain
			err := Validate(rc)
			if err == nil {
				t.Errorf("expected error for invalid domain %q, got nil", domain)
			}
		})
	}
}

func TestValidate_ValidInternalDomains(t *testing.T) {
	cases := []string{
		"home.lab",
		"my-homelab.internal",
		"home.example.com",
		"a.b.c",
	}
	for _, domain := range cases {
		t.Run(domain, func(t *testing.T) {
			rc := validRunConfig()
			rc.InternalDomain = domain
			if err := Validate(rc); err != nil {
				t.Errorf("unexpected error for valid domain %q: %v", domain, err)
			}
		})
	}
}

func TestValidate_InvalidBootstrapServerIP(t *testing.T) {
	rc := validRunConfig()
	rc.BootstrapServerIP = "not-an-ip"
	err := Validate(rc)
	if err == nil || !strings.Contains(err.Error(), "bootstrap_server_ip") {
		t.Errorf("expected bootstrap_server_ip error, got: %v", err)
	}
}

func TestValidate_InvalidLabVLANSubnet(t *testing.T) {
	rc := validRunConfig()
	rc.LabVLANSubnet = "10.0.0.0/99"
	err := Validate(rc)
	if err == nil || !strings.Contains(err.Error(), "lab_vlan_subnet") {
		t.Errorf("expected lab_vlan_subnet error, got: %v", err)
	}
}

func TestValidate_VaultThresholdExceedsShares(t *testing.T) {
	rc := validRunConfig()
	rc.VaultShares = 3
	rc.VaultThreshold = 4
	err := Validate(rc)
	if err == nil || !strings.Contains(err.Error(), "vault_threshold") {
		t.Errorf("expected vault_threshold error, got: %v", err)
	}
}

func TestValidate_VaultSharesTooFew(t *testing.T) {
	rc := validRunConfig()
	rc.VaultShares = 2
	rc.VaultThreshold = 2
	err := Validate(rc)
	if err == nil || !strings.Contains(err.Error(), "vault_shares") {
		t.Errorf("expected vault_shares error, got: %v", err)
	}
}

// ── ParseArch ─────────────────────────────────────────────────────────────────

func TestParseArch(t *testing.T) {
	cases := []struct {
		input string
		want  TargetArch
	}{
		{"x86_64", ArchAMD64},
		{"aarch64", ArchARM64},
		{"arm64", ArchARM64},
		{"  x86_64  ", ArchAMD64}, // trimmed
		{"i686", ArchUnknown},
		{"", ArchUnknown},
		{"armv7l", ArchUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := ParseArch(tc.input)
			if got != tc.want {
				t.Errorf("ParseArch(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// ── TargetArch methods ────────────────────────────────────────────────────────

func TestTargetArch_GoArch(t *testing.T) {
	cases := []struct {
		arch TargetArch
		want string
	}{
		{ArchAMD64, "amd64"},
		{ArchARM64, "arm64"},
		{ArchUnknown, "unknown"},
	}
	for _, tc := range cases {
		if got := tc.arch.GoArch(); got != tc.want {
			t.Errorf("TargetArch(%q).GoArch() = %q, want %q", tc.arch, got, tc.want)
		}
	}
}

func TestTargetArch_AnsibleArch(t *testing.T) {
	cases := []struct {
		arch TargetArch
		want string
	}{
		{ArchAMD64, "x86_64"},
		{ArchARM64, "aarch64"},
		{ArchUnknown, "unknown"},
	}
	for _, tc := range cases {
		if got := tc.arch.AnsibleArch(); got != tc.want {
			t.Errorf("TargetArch(%q).AnsibleArch() = %q, want %q", tc.arch, got, tc.want)
		}
	}
}

// ── Load ──────────────────────────────────────────────────────────────────────

func TestLoad_NonExistentConfigAppliesDefaults(t *testing.T) {
	rc, err := Load("/nonexistent/config.yaml", "")
	if err != nil {
		t.Fatalf("Load with nonexistent file: %v", err)
	}
	if rc.VaultShares != 5 {
		t.Errorf("VaultShares default: got %d, want 5", rc.VaultShares)
	}
	if rc.VaultThreshold != 3 {
		t.Errorf("VaultThreshold default: got %d, want 3", rc.VaultThreshold)
	}
	if rc.SSHUser != "ubuntu" {
		t.Errorf("SSHUser default: got %q, want %q", rc.SSHUser, "ubuntu")
	}
}

func TestLoad_DefaultACMEServerURL(t *testing.T) {
	t.Setenv("ACME_SERVER_URL", "")
	rc, err := Load("/nonexistent/config.yaml", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const wantDefault = "https://acme-v02.api.letsencrypt.org/directory"
	if rc.ACMEServerURL != wantDefault {
		t.Errorf("ACMEServerURL: got %q, want %q", rc.ACMEServerURL, wantDefault)
	}
}

func TestLoad_EnvOverridesACMEServerURL(t *testing.T) {
	t.Setenv("ACME_SERVER_URL", "https://pebble.test/dir")
	rc, err := Load("/nonexistent/config.yaml", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rc.ACMEServerURL != "https://pebble.test/dir" {
		t.Errorf("ACMEServerURL: got %q, want override value", rc.ACMEServerURL)
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
lab_vlan_id: 42
admin_email: ops@homelab.test
vault_shares: 7
vault_threshold: 4
vault_init_output_path: /tmp/vault.json
`
	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	f.Close()

	rc, err := Load(f.Name(), "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if rc.TargetHost != "10.0.0.1" {
		t.Errorf("TargetHost: got %q, want %q", rc.TargetHost, "10.0.0.1")
	}
	if rc.VaultShares != 7 {
		t.Errorf("VaultShares: got %d, want 7", rc.VaultShares)
	}
	if rc.LabVLANID != 42 {
		t.Errorf("LabVLANID: got %d, want 42", rc.LabVLANID)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	f, err := os.CreateTemp("", "config-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	f.WriteString("target_host: [not: valid: yaml")
	f.Close()

	_, err = Load(f.Name(), "")
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestLoad_SecretsFromEnv(t *testing.T) {
	t.Setenv("CF_DNS_API_TOKEN", "test-cf-token")
	t.Setenv("BOOTSTRAP_ADMIN_PASSWORD", "test-admin-pass")

	rc, err := Load("/nonexistent/config.yaml", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rc.Secrets.CloudflareAPIToken != "test-cf-token" {
		t.Errorf("CloudflareAPIToken: got %q, want %q", rc.Secrets.CloudflareAPIToken, "test-cf-token")
	}
	if rc.Secrets.AdminPassword != "test-admin-pass" {
		t.Errorf("AdminPassword: got %q, want %q", rc.Secrets.AdminPassword, "test-admin-pass")
	}
}
