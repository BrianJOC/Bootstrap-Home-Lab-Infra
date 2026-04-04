package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/homelab/bootstrap-home-infra/internal/config"
)

func completeConfig() *config.Config {
	return &config.Config{
		TargetHost:        "192.168.1.100",
		SSHUser:           "ubuntu",
		SSHKeyPath:        "/home/user/.ssh/id_ed25519",
		InternalDomain:    "home.lab",
		CloudflareZoneID:  "abc123",
		BootstrapServerIP: "192.168.1.100",
		LabVLANSubnet:     "10.0.0.0/24",
		AdminEmail:        "admin@example.com",
		VaultShares:       5,
		VaultThreshold:    3,
	}
}

func TestWriteConfigYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := config.WriteConfigYAML(path, completeConfig()); err != nil {
		t.Fatalf("WriteConfigYAML: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("mode = %o, want 0600", fi.Mode().Perm())
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	for _, want := range []string{
		`target_host: "192.168.1.100"`,
		`ssh_user: "ubuntu"`,
		`internal_domain: "home.lab"`,
		`bootstrap_server_ip: "192.168.1.100"`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("config.yaml missing %q\ngot:\n%s", want, content)
		}
	}

	// No temp file left over (atomic write completed).
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
}

func TestWriteConfigYAML_PermissionDenied(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0555); err != nil {
		t.Skip("cannot set dir permissions:", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0755) })
	err := config.WriteConfigYAML(filepath.Join(dir, "config.yaml"), completeConfig())
	if err == nil {
		t.Error("expected error for read-only dir, got nil")
	}
}

func TestWriteEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	sec := &config.Secrets{
		CloudflareAPIToken: "tok-xyz",
		AdminPassword:      "s3cr3t",
	}

	if err := config.WriteEnvFile(path, sec); err != nil {
		t.Fatalf("WriteEnvFile: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("mode = %o, want 0600", fi.Mode().Perm())
	}

	data, _ := os.ReadFile(path)
	content := string(data)
	if !strings.Contains(content, "CF_DNS_API_TOKEN=tok-xyz") {
		t.Errorf("missing CF_DNS_API_TOKEN, got:\n%s", content)
	}
	if !strings.Contains(content, "BOOTSTRAP_ADMIN_PASSWORD=s3cr3t") {
		t.Errorf("missing BOOTSTRAP_ADMIN_PASSWORD, got:\n%s", content)
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
}

func TestWriteEnvFile_PermissionDenied(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0555); err != nil {
		t.Skip("cannot set dir permissions:", err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0755) })
	err := config.WriteEnvFile(filepath.Join(dir, ".env"), &config.Secrets{})
	if err == nil {
		t.Error("expected error for read-only dir, got nil")
	}
}

func TestWriteEnvFile_NewlineInSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	sec := &config.Secrets{
		CloudflareAPIToken: "tok\nnewline",
		AdminPassword:      "pass",
	}
	if err := config.WriteEnvFile(path, sec); err == nil {
		t.Error("expected error for secret containing newline, got nil")
	}
}
