package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

// WriteConfigYAML writes structural config fields (no secrets) to path atomically.
// Uses temp file + rename. Mode 0600. Accepts explicit path for testability.
func WriteConfigYAML(path string, cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshalling config: %w", err)
	}
	return atomicWrite(path, string(data))
}

// WriteEnvFile writes secrets to path atomically (mode 0600).
func WriteEnvFile(path string, sec *Secrets) error {
	if sec == nil {
		return fmt.Errorf("secrets are nil")
	}
	values := map[string]string{
		"CF_DNS_API_TOKEN":             sec.CloudflareAPIToken,
		"BOOTSTRAP_ADMIN_PASSWORD":     sec.AdminPassword,
		"RESTIC_PRIMARY_PASSWORD":      sec.ResticPrimaryPassword,
		"RESTIC_USB_PASSWORD":          sec.ResticUSBPassword,
		"CONTROLLER_RECOVERY_PASSWORD": sec.ControllerRecoveryPassword,
		"PAGING_DESTINATION_URL":       sec.PagingDestinationURL,
	}
	// Validate secrets before writing
	for name, val := range values {
		if strings.ContainsAny(val, "\n\r") {
			return fmt.Errorf("secret %q contains newline characters", name)
		}
		if val == "" {
			delete(values, name)
		}
	}
	content, err := godotenv.Marshal(values)
	if err != nil {
		return fmt.Errorf("marshalling secrets: %w", err)
	}
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
