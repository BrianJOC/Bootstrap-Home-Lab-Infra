package stages

import (
	"context"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/ansible"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// PostgreSQLStage is Stage 5: PostgreSQL shared database deployment.
type PostgreSQLStage struct{}

// Name returns the canonical stage identifier.
func (s *PostgreSQLStage) Name() string { return "postgres" }

// Check verifies PostgreSQL is running and accepting connections.
func (s *PostgreSQLStage) Check(ctx context.Context, cfg *config.RunConfig) (bool, error) {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
	if err != nil {
		return false, err
	}
	defer client.Close()

	stdout, _, err := client.Run(
		`systemctl is-active postgresql 2>/dev/null && pg_isready -h localhost -p 5432 2>/dev/null && echo running || echo not-running`,
	)
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(stdout) == "running", nil
}

// Run deploys PostgreSQL and migrates Authentik via Ansible.
func (s *PostgreSQLStage) Run(ctx context.Context, cfg *config.RunConfig, sink stage.Sink) error {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
	if err != nil {
		return err
	}
	defer client.Close()

	runner := ansible.New(client, sink)
	return runner.RunPlaybook(
		"ansible/playbooks/stage-5-postgres.yml",
		cfg.TargetHost+",",
		map[string]string{
			"internal_domain":     cfg.InternalDomain,
			"bootstrap_server_ip": cfg.BootstrapServerIP,
			"pg_admin_password":   cfg.Secrets.AdminPassword,
		},
	)
}
