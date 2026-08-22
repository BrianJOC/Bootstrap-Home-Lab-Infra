package stages

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/ansible"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
	terraformrunner "github.com/homelab/bootstrap-home-infra/internal/terraform"
)

// GitLabStage is Stage 6: GitLab CE + Runner deployment.
type GitLabStage struct{}

// Name returns the canonical stage identifier.
func (s *GitLabStage) Name() string { return "gitlab" }

// Check verifies GitLab is running and healthy on the target.
func (s *GitLabStage) Check(_ context.Context, cfg *config.Config, rc *runcontext.RunContext) (bool, error) {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
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
	if strings.TrimSpace(stdout) != "running" {
		return false, nil
	}
	if !vaultAuthEnabled(client, cfg, "jwt") {
		return false, nil
	}
	statePath, err := terraformrunner.StatePath("gitlab-config")
	if err != nil {
		return false, err
	}
	if _, err = os.Stat(statePath); err != nil {
		return false, nil
	}
	_, err = os.Stat(strings.TrimSuffix(statePath, ".tfstate") + ".handoff.json")
	return err == nil, nil
}

// Run deploys GitLab CE and Runner via Ansible.
func (s *GitLabStage) Run(_ context.Context, cfg *config.Config, sec *config.Secrets, rc *runcontext.RunContext, sink stage.Sink) error {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return err
	}
	defer client.Close()

	runner := ansible.New(client, sink)
	if err := runner.RunPlaybook(
		"ansible/playbooks/stage-6-gitlab.yml",
		cfg.TargetHost+",",
		map[string]string{
			"internal_domain":       cfg.InternalDomain,
			"bootstrap_server_ip":   cfg.BootstrapServerIP,
			"gitlab_admin_password": sec.AdminPassword,
			"admin_email":           cfg.AdminEmail,
		},
	); err != nil {
		return err
	}
	if err := configureVaultGitLabJWT(client, cfg); err != nil {
		return err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	tokenBytes, err := os.ReadFile(filepath.Join(homeDir, ".bootstrap", "gitlab-api-token"))
	if err != nil {
		return err
	}
	terraformRunner := terraformrunner.New(sink, rc)
	statePath, err := terraformRunner.ApplyModule(
		"gitlab-config",
		"terraform/modules/gitlab-config",
		map[string]string{
			"gitlab_url":      "https://gitlab." + cfg.InternalDomain + "/api/v4/",
			"vault_address":   "https://vault." + cfg.InternalDomain + ":8200",
			"internal_domain": cfg.InternalDomain,
		},
		map[string]string{
			"gitlab_token": strings.TrimSpace(string(tokenBytes)),
		},
	)
	if err != nil {
		return err
	}
	projectID, err := terraformRunner.Output("terraform/modules/gitlab-config", "gitlab-config", "iac_project_id")
	if err != nil {
		return err
	}
	if err := terraformRunner.MigrateStateToGitLab(
		"gitlab-config",
		"terraform/modules/gitlab-config",
		"https://gitlab."+cfg.InternalDomain,
		projectID,
		strings.TrimSpace(string(tokenBytes)),
	); err != nil {
		return err
	}
	sink.Log("info", "gitlab", "GitLab is authoritative for the verified Terraform state; local rollback state retained", map[string]string{
		"state": statePath,
	})
	return nil
}
