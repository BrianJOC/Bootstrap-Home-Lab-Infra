package stages

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/ansible"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// AuthentikStage is Stage 5: Authentik SSO deployment.
type AuthentikStage struct{}

// Name returns the canonical stage identifier.
func (s *AuthentikStage) Name() string { return "authentik" }

// Check verifies Authentik is running and healthy on the target.
func (s *AuthentikStage) Check(_ context.Context, cfg *config.Config, rc *runcontext.RunContext) (bool, error) {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return false, err
	}
	defer client.Close()

	stdout, _, err := client.Run(
		`docker ps --filter name=authentik-server --filter status=running --format '{{.Names}}' | grep -q authentik && echo running || echo not-running`,
	)
	if err != nil {
		return false, fmt.Errorf("checking authentik status on target: %w", err)
	}
	if strings.TrimSpace(stdout) != "running" {
		return false, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(filepath.Join(homeDir, ".bootstrap", "oidc-vault-secret")); err != nil {
		return false, nil
	}
	return vaultAuthEnabled(client, cfg, "oidc"), nil
}

// Run deploys Authentik via Ansible.
func (s *AuthentikStage) Run(_ context.Context, cfg *config.Config, sec *config.Secrets, rc *runcontext.RunContext, sink stage.Sink) error {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return err
	}
	defer client.Close()

	runner := ansible.New(client, sink)
	if err := runner.RunPlaybook(
		"ansible/playbooks/stage-5-authentik.yml",
		cfg.TargetHost+",",
		map[string]string{
			"internal_domain":          cfg.InternalDomain,
			"bootstrap_server_ip":      cfg.BootstrapServerIP,
			"authentik_admin_password": sec.AdminPassword,
			"admin_email":              cfg.AdminEmail,
			"service_data_root":        cfg.DataPath(),
		},
	); err != nil {
		return err
	}
	return configureVaultOIDC(client, cfg)
}
