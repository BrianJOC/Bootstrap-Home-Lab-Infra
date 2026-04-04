// internal/tui/tui_testhelpers_test.go
package tui

import "github.com/homelab/bootstrap-home-infra/internal/config"

func emptyRC() *config.RunConfig {
	return &config.RunConfig{Config: &config.Config{}, Secrets: &config.Secrets{}}
}

func completeRC() *config.RunConfig {
	return &config.RunConfig{
		Config: &config.Config{
			TargetHost:        "192.168.1.10",
			SSHUser:           "ubuntu",
			SSHKeyPath:        "/home/user/.ssh/id_ed25519",
			InternalDomain:    "lab.example.com",
			CloudflareZoneID:  "abc123",
			BootstrapServerIP: "192.168.1.10",
			LabVLANSubnet:     "192.168.1.0/24",
			AdminEmail:        "admin@example.com",
			VaultShares:       5,
			VaultThreshold:    3,
		},
		Secrets: &config.Secrets{
			CloudflareAPIToken: "cf-token",
			AdminPassword:      "secret",
		},
	}
}
