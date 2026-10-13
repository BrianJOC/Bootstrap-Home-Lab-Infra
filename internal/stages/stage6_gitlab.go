package stages

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/ansible"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/gitops"
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
	handoffComplete, err := terraformHandoffStatus("bootstrap-gitlab")
	if err != nil {
		return false, err
	}
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return false, err
	}
	defer client.Close()

	stdout, _, err := client.Run(
		`sudo gitlab-ctl status 2>/dev/null | grep -q "run:" && echo running || echo not-running`,
	)
	if err != nil {
		return false, nil
	}
	if strings.TrimSpace(stdout) != "running" {
		if handoffComplete {
			return false, fmt.Errorf("GitLab is unavailable after its Terraform state handoff; inspect GitLab instead of replaying bootstrap")
		}
		return false, nil
	}
	if !vaultAuthEnabled(client, cfg, "jwt") {
		if handoffComplete {
			return false, fmt.Errorf("Vault GitLab JWT auth is unavailable after its GitLab state handoff; inspect Vault and reconcile through the GitLab pipeline")
		}
		return false, nil
	}
	return handoffComplete, nil
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
			"service_data_root":     cfg.DataPath(),
		},
	); err != nil {
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
	projectSeed, err := os.MkdirTemp("", "bootstrap-gitops-seed-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(projectSeed)
	if err = gitops.SeedProject(".", projectSeed, cfg); err != nil {
		return fmt.Errorf("preparing GitLab management project: %w", err)
	}
	gitlabVariables := map[string]string{
		"iac_source_dir":  projectSeed,
		"gitlab_url":      gitlabAPIURL(cfg),
		"gitlab_insecure": boolString(cfg.APIInsecure),
		"vault_address":   "https://vault." + cfg.InternalDomain + ":8200",
		"internal_domain": cfg.InternalDomain,
		"maas_api_url":    "http://" + cfg.BootstrapServerIP + ":5240/MAAS",
	}
	gitlabSecrets := map[string]string{
		"gitlab_token": strings.TrimSpace(string(tokenBytes)),
	}
	for _, groupPath := range []string{"infrastructure", "services"} {
		metadata, exists, findErr := findGitLabResource(
			gitlabAPIURL(cfg), gitlabSecrets["gitlab_token"], "groups", groupPath, cfg.APIInsecure,
		)
		if findErr != nil {
			return findErr
		}
		if !exists {
			continue
		}
		if err = terraformRunner.ImportIfMissing(
			"bootstrap-gitlab",
			"terraform/environments/bootstrap/gitlab",
			"module.gitlab.gitlab_group."+groupPath,
			strconv.Itoa(metadata.ID),
			gitlabVariables,
			gitlabSecrets,
		); err != nil {
			return fmt.Errorf("adopting interrupted GitLab group %s: %w", groupPath, err)
		}
	}
	project, projectExists, err := findGitLabResource(
		gitlabAPIURL(cfg), gitlabSecrets["gitlab_token"], "projects", "infrastructure/iac", cfg.APIInsecure,
	)
	if err != nil {
		return err
	}
	if projectExists {
		if project.EmptyRepo {
			if err = initializeEmptyGitLabProject(
				gitlabAPIURL(cfg), gitlabSecrets["gitlab_token"], project.ID, cfg.APIInsecure,
			); err != nil {
				return fmt.Errorf("repairing interrupted GitLab project initialization: %w", err)
			}
		}
		if err = terraformRunner.ImportIfMissing(
			"bootstrap-gitlab",
			"terraform/environments/bootstrap/gitlab",
			"module.gitlab.gitlab_project.iac",
			strconv.Itoa(project.ID),
			gitlabVariables,
			gitlabSecrets,
		); err != nil {
			return fmt.Errorf("adopting interrupted GitLab IaC project: %w", err)
		}
	}
	statePath, err := terraformRunner.ApplyModule(
		"bootstrap-gitlab",
		"terraform/environments/bootstrap/gitlab",
		gitlabVariables,
		gitlabSecrets,
	)
	if err != nil {
		return err
	}
	rootToken, err := readVaultRootToken(cfg)
	if err != nil {
		return err
	}
	vaultGitLabVariables := map[string]string{
		"vault_address":   vaultAPIURL(cfg),
		"skip_tls_verify": boolString(cfg.APIInsecure || cfg.VaultAPIURL == ""),
		"internal_domain": cfg.InternalDomain,
	}
	vaultGitLabSecrets := map[string]string{"vault_token": rootToken}
	if vaultAuthEnabled(client, cfg, "jwt") {
		if err = completeInterruptedVaultAuthConfiguration(client, cfg, "jwt", map[string]string{
			"oidc_discovery_url": "https://gitlab." + cfg.InternalDomain,
			"bound_issuer":       "https://gitlab." + cfg.InternalDomain,
		}); err != nil {
			return err
		}
		if err = terraformRunner.ImportIfMissing(
			"bootstrap-vault-gitlab",
			"terraform/environments/bootstrap/vault-gitlab",
			"module.gitlab.vault_jwt_auth_backend.gitlab",
			"jwt",
			vaultGitLabVariables,
			vaultGitLabSecrets,
		); err != nil {
			return fmt.Errorf("adopting interrupted Vault GitLab JWT backend: %w", err)
		}
	}
	if _, err = terraformRunner.ApplyModule(
		"bootstrap-vault-gitlab",
		"terraform/environments/bootstrap/vault-gitlab",
		vaultGitLabVariables,
		vaultGitLabSecrets,
	); err != nil {
		return err
	}
	projectID, err := terraformRunner.Output("terraform/environments/bootstrap/gitlab", "bootstrap-gitlab", "iac_project_id")
	if err != nil {
		return err
	}
	components := []struct {
		name string
		root string
	}{
		{"bootstrap-gitlab", "terraform/environments/bootstrap/gitlab"},
		{"bootstrap-dns", "terraform/environments/bootstrap/dns"},
		{"bootstrap-vault", "terraform/environments/bootstrap/vault"},
		{"bootstrap-authentik", "terraform/environments/bootstrap/authentik"},
		{"bootstrap-vault-oidc", "terraform/environments/bootstrap/vault-oidc"},
		{"bootstrap-vault-gitlab", "terraform/environments/bootstrap/vault-gitlab"},
	}
	for _, component := range components {
		componentState, stateErr := terraformrunner.StatePath(component.name)
		if stateErr != nil {
			return stateErr
		}
		if _, stateErr = os.Stat(componentState); os.IsNotExist(stateErr) {
			continue
		}
		if stateErr != nil {
			return stateErr
		}
		if err := terraformRunner.MigrateStateToGitLab(
			component.name,
			component.root,
			gitlabBaseURL(cfg),
			projectID,
			strings.TrimSpace(string(tokenBytes)),
		); err != nil {
			return err
		}
	}
	sink.Log("info", "gitlab", "GitLab is authoritative for the verified Terraform state; local rollback state retained", map[string]string{
		"state": statePath,
	})
	return nil
}
