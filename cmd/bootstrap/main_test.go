// cmd/bootstrap/main_test.go
package main

import (
	"testing"

	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/tui"
)

func emptyRCm() *config.RunConfig {
	return &config.RunConfig{Config: &config.Config{}, Secrets: &config.Secrets{}}
}

func completeRCm() *config.RunConfig {
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

func partialRCm() *config.RunConfig {
	rc := emptyRCm()
	rc.TargetHost = "192.168.1.10"
	return rc
}

func TestSelectInitialScreen(t *testing.T) {
	tests := []struct {
		name       string
		rc         *config.RunConfig
		wizardFlag bool
		want       tui.InitialMode
	}{
		{"no config, no flag", emptyRCm(), false, tui.InitialModeWizard},
		{"complete config, no flag", completeRCm(), false, tui.InitialModePreamble},
		{"complete config, wizard", completeRCm(), true, tui.InitialModeWizard},
		{"partial config, no flag", partialRCm(), false, tui.InitialModeWizard},
		{"empty config, wizard", emptyRCm(), true, tui.InitialModeWizard},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := selectInitialScreen(tc.rc, tc.wizardFlag)
			if got != tc.want {
				t.Errorf("selectInitialScreen() = %v, want %v", got, tc.want)
			}
		})
	}
}
