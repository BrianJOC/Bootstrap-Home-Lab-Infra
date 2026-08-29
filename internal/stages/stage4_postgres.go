package stages

import (
	"context"
	"fmt"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/ansible"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// PostgreSQLStage is Stage 4: PostgreSQL shared database deployment.
type PostgreSQLStage struct{}

// Name returns the canonical stage identifier.
func (s *PostgreSQLStage) Name() string { return "postgres" }

// Check verifies PostgreSQL is running and accepting connections.
func (s *PostgreSQLStage) Check(_ context.Context, cfg *config.Config, rc *runcontext.RunContext) (bool, error) {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return false, err
	}
	defer client.Close()

	stdout, _, err := client.Run(
		`systemctl is-active --quiet postgresql 2>/dev/null && pg_isready -h localhost -p 5432 >/dev/null 2>&1 && sudo grep -Fq '172.30.10.0/24' /etc/postgresql/17/main/pg_hba.conf && test "$(sudo -u postgres psql -tAc "SELECT count(*) FROM pg_database d JOIN pg_roles r ON d.datdba=r.oid WHERE (d.datname,r.rolname) IN (('authentik','authentik'),('gitlab','gitlab'),('maas','maas'))")" = 3 && echo running || echo not-running`,
	)
	if err != nil {
		return false, fmt.Errorf("checking postgresql status on target: %w", err)
	}
	return strings.TrimSpace(stdout) == "running", nil
}

// Run deploys PostgreSQL before any database-backed application.
func (s *PostgreSQLStage) Run(_ context.Context, cfg *config.Config, sec *config.Secrets, rc *runcontext.RunContext, sink stage.Sink) error {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return err
	}
	defer client.Close()

	runner := ansible.New(client, sink)
	return runner.RunPlaybook(
		"ansible/playbooks/stage-4-postgres.yml",
		cfg.TargetHost+",",
		map[string]string{
			"internal_domain":     cfg.InternalDomain,
			"bootstrap_server_ip": cfg.BootstrapServerIP,
			"pg_admin_password":   sec.AdminPassword,
		},
	)
}
