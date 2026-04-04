package config_test

import (
	"testing"
	"github.com/homelab/bootstrap-home-infra/internal/config"
)

func baseWizardResult() config.WizardResult {
	return config.WizardResult{
		Values: map[string]string{
			"target_host":         "192.168.1.50",
			"ssh_user":            "ubuntu",
			"ssh_key_path":        "/home/user/.ssh/id_ed25519",
			"internal_domain":     "home.lab",
			"cloudflare_zone_id":  "abc123",
			"bootstrap_server_ip": "192.168.1.50",
			"lab_vlan_subnet":     "10.0.0.0/24",
			"admin_email":         "admin@example.com",
		},
		Secrets: map[string]string{
			"CF_DNS_API_TOKEN":         "tok-abc",
			"BOOTSTRAP_ADMIN_PASSWORD": "hunter2",
		},
	}
}

func TestFromWizardResult(t *testing.T) {
	r := baseWizardResult()
	rc, err := config.FromWizardResult(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tests := []struct{ name, got, want string }{
		{"TargetHost",        rc.TargetHost,        "192.168.1.50"},
		{"SSHUser",           rc.SSHUser,           "ubuntu"},
		{"SSHKeyPath",        rc.SSHKeyPath,        "/home/user/.ssh/id_ed25519"},
		{"InternalDomain",    rc.InternalDomain,    "home.lab"},
		{"CloudflareZoneID",  rc.CloudflareZoneID,  "abc123"},
		{"BootstrapServerIP", rc.BootstrapServerIP, "192.168.1.50"},
		{"LabVLANSubnet",     rc.LabVLANSubnet,     "10.0.0.0/24"},
		{"AdminEmail",        rc.AdminEmail,        "admin@example.com"},
		{"CloudflareToken",   rc.Secrets.CloudflareAPIToken, "tok-abc"},
		{"AdminPassword",     rc.Secrets.AdminPassword,      "hunter2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
	if rc.VaultShares != 5 {
		t.Errorf("VaultShares default = %d, want 5", rc.VaultShares)
	}
	if rc.VaultThreshold != 3 {
		t.Errorf("VaultThreshold default = %d, want 3", rc.VaultThreshold)
	}
}

func TestFromWizardResult_MissingField(t *testing.T) {
	required := []string{
		"target_host", "ssh_user", "ssh_key_path", "internal_domain",
		"cloudflare_zone_id", "bootstrap_server_ip", "lab_vlan_subnet", "admin_email",
	}
	for _, key := range required {
		t.Run("missing_"+key, func(t *testing.T) {
			r := baseWizardResult()
			delete(r.Values, key)
			_, err := config.FromWizardResult(r)
			if err == nil {
				t.Errorf("expected error for missing key %q", key)
			}
		})
	}
	for _, key := range []string{"CF_DNS_API_TOKEN", "BOOTSTRAP_ADMIN_PASSWORD"} {
		t.Run("missing_secret_"+key, func(t *testing.T) {
			r := baseWizardResult()
			delete(r.Secrets, key)
			_, err := config.FromWizardResult(r)
			if err == nil {
				t.Errorf("expected error for missing secret %q", key)
			}
		})
	}
	t.Run("whitespace_target_host", func(t *testing.T) {
		r := baseWizardResult()
		r.Values["target_host"] = "   "
		_, err := config.FromWizardResult(r)
		if err == nil {
			t.Error("expected error for whitespace-only target_host")
		}
	})
}
