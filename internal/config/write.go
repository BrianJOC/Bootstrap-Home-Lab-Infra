package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteConfigYAML writes structural config fields (no secrets) to path atomically.
// Uses temp file + rename. Mode 0600. Accepts explicit path for testability.
func WriteConfigYAML(path string, cfg *Config) error {
	lines := []string{
		fmt.Sprintf("target_host: %q", cfg.TargetHost),
		fmt.Sprintf("ssh_user: %q", cfg.SSHUser),
		fmt.Sprintf("ssh_key_path: %q", cfg.SSHKeyPath),
		fmt.Sprintf("internal_domain: %q", cfg.InternalDomain),
		fmt.Sprintf("cloudflare_zone_id: %q", cfg.CloudflareZoneID),
		fmt.Sprintf("bootstrap_server_ip: %q", cfg.BootstrapServerIP),
		fmt.Sprintf("lab_vlan_subnet: %q", cfg.LabVLANSubnet),
		fmt.Sprintf("lab_vlan_id: %d", cfg.LabVLANID),
		fmt.Sprintf("admin_email: %q", cfg.AdminEmail),
		fmt.Sprintf("vault_shares: %d", cfg.VaultShares),
		fmt.Sprintf("vault_threshold: %d", cfg.VaultThreshold),
		fmt.Sprintf("vault_init_output_path: %q", cfg.VaultInitOutputPath),
	}
	if cfg.DataDisk != "" {
		lines = append(lines, fmt.Sprintf("data_disk: %q", cfg.DataDisk))
		if cfg.DataDiskMode != "" {
			lines = append(lines, fmt.Sprintf("data_disk_mode: %q", cfg.DataDiskMode))
		}
		if cfg.DataDiskForceFormat {
			lines = append(lines, fmt.Sprintf("data_disk_force_format: %v", cfg.DataDiskForceFormat))
		}
	}
	return atomicWrite(path, strings.Join(lines, "\n")+"\n")
}

// WriteEnvFile writes secrets to path atomically (mode 0600).
func WriteEnvFile(path string, sec *Secrets) error {
	// Validate secrets before writing
	for name, val := range map[string]string{
		"CF_DNS_API_TOKEN":         sec.CloudflareAPIToken,
		"BOOTSTRAP_ADMIN_PASSWORD": sec.AdminPassword,
	} {
		if strings.ContainsAny(val, "\n\r") {
			return fmt.Errorf("secret %q contains newline characters", name)
		}
	}
	content := fmt.Sprintf("CF_DNS_API_TOKEN=%s\nBOOTSTRAP_ADMIN_PASSWORD=%s\n",
		sec.CloudflareAPIToken, sec.AdminPassword)
	return atomicWrite(path, content)
}

// atomicWrite writes content to path via temp file + rename to avoid partial writes.
func atomicWrite(path, content string) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+"*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName) // no-op if rename succeeded
	}()
	if err := tmp.Chmod(0600); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.WriteString(content); err != nil {
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("renaming %s → %s: %w", tmpName, path, err)
	}
	return nil
}
