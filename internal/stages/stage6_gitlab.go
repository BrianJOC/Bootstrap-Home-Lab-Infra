package stages

import (
	"context"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/ansible"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// GitLabStage is Stage 6: GitLab CE + Runner deployment.
type GitLabStage struct{}

// Name returns the canonical stage identifier.
func (s *GitLabStage) Name() string { return "gitlab" }

// Check verifies GitLab is running and healthy on the target.
func (s *GitLabStage) Check(ctx context.Context, cfg *config.RunConfig) (bool, error) {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
	if err != nil {
		return false, err
	}
	defer client.Close()

	stdout, _, err := client.Run(
		`gitlab-ctl status 2>/dev/null | grep -q "run:" && echo running || echo not-running`,
	)
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(stdout) == "running", nil
}

// Run deploys GitLab CE and Runner via Ansible.
func (s *GitLabStage) Run(ctx context.Context, cfg *config.RunConfig, sink stage.Sink) error {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath)
	if err != nil {
		return err
	}
	defer client.Close()

	runner := ansible.New(client, sink)
	return runner.RunPlaybook(
		"ansible/playbooks/stage-6-gitlab.yml",
		cfg.TargetHost+",",
		map[string]string{
			"internal_domain":        cfg.InternalDomain,
			"bootstrap_server_ip":    cfg.BootstrapServerIP,
			"gitlab_admin_password":  cfg.Secrets.AdminPassword,
			"admin_email":            cfg.AdminEmail,
		},
	)
}
