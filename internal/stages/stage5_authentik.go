package stages

import (
	"context"
	"encoding/json"
	"fmt"
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

// AuthentikStage is Stage 5: Authentik SSO deployment.
type AuthentikStage struct{}

// Name returns the canonical stage identifier.
func (s *AuthentikStage) Name() string { return "authentik" }

// Check verifies Authentik is running and healthy on the target.
func (s *AuthentikStage) Check(_ context.Context, cfg *config.Config, rc *runcontext.RunContext) (bool, error) {
	handoffComplete := true
	for _, component := range []string{"bootstrap-authentik", "bootstrap-vault-oidc"} {
		complete, handoffErr := terraformHandoffStatus(component)
		if handoffErr != nil {
			return false, handoffErr
		}
		handoffComplete = handoffComplete && complete
	}
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return false, err
	}
	defer client.Close()

	stdout, _, err := client.Run(
		`sudo docker ps --filter name=authentik-server --filter status=running --format '{{.Names}}' | grep -q authentik && echo running || echo not-running`,
	)
	if err != nil {
		return false, fmt.Errorf("checking authentik status on target: %w", err)
	}
	if strings.TrimSpace(stdout) != "running" {
		if handoffComplete {
			return false, fmt.Errorf("Authentik is unavailable after its GitLab state handoff; inspect the Authentik service instead of replaying bootstrap")
		}
		return false, nil
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(filepath.Join(homeDir, ".bootstrap", "oidc-vault-secret")); err != nil {
		if handoffComplete {
			return false, fmt.Errorf("protected Vault OIDC rollback credential is missing after GitLab handoff: %w", err)
		}
		return false, nil
	}
	if !vaultAuthEnabled(client, cfg, "oidc") {
		if handoffComplete {
			return false, fmt.Errorf("Vault OIDC auth is unavailable after its GitLab state handoff; inspect Vault and reconcile through the GitLab pipeline")
		}
		return false, nil
	}
	return handoffComplete, nil
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
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	authentikToken, err := os.ReadFile(filepath.Join(homeDir, ".bootstrap", "authentik-bootstrap-token"))
	if err != nil {
		return fmt.Errorf("reading Authentik bootstrap token: %w", err)
	}
	services, err := json.Marshal([]map[string]any{
		{
			"name":         "vault",
			"redirect_uri": "https://vault." + cfg.InternalDomain + ":8200/ui/vault/auth/oidc/oidc/callback",
			"groups":       []string{"adults"},
		},
		{
			"name":         "gitlab",
			"redirect_uri": "https://gitlab." + cfg.InternalDomain + "/users/auth/openid_connect/callback",
			"groups":       []string{"adults"},
		},
	})
	if err != nil {
		return err
	}
	terraformRunner := terraformrunner.New(sink, rc)
	if _, err = terraformRunner.ApplyModule(
		"bootstrap-authentik",
		"terraform/environments/bootstrap/authentik",
		map[string]string{
			"authentik_url":   authentikAPIURL(cfg),
			"internal_domain": cfg.InternalDomain,
			"services":        string(services),
		},
		map[string]string{"authentik_token": strings.TrimSpace(string(authentikToken))},
	); err != nil {
		return err
	}
	clientIDs := map[string]string{}
	clientSecrets := map[string]string{}
	if err = terraformRunner.OutputJSON(
		"terraform/environments/bootstrap/authentik",
		"bootstrap-authentik",
		"client_ids",
		&clientIDs,
	); err != nil {
		return err
	}
	if err = terraformRunner.OutputJSON(
		"terraform/environments/bootstrap/authentik",
		"bootstrap-authentik",
		"client_secrets",
		&clientSecrets,
	); err != nil {
		return err
	}
	for service, secret := range clientSecrets {
		if err = writeProtectedControllerFile(filepath.Join(homeDir, ".bootstrap", "oidc-"+service+"-secret"), secret); err != nil {
			return err
		}
	}
	rootToken, err := readVaultRootToken(cfg)
	if err != nil {
		return err
	}
	vaultOIDCVariables := map[string]string{
		"vault_address":   vaultAPIURL(cfg),
		"skip_tls_verify": boolString(cfg.APIInsecure || cfg.VaultAPIURL == ""),
		"internal_domain": cfg.InternalDomain,
		"oidc_client_id":  clientIDs["vault"],
	}
	vaultOIDCSecrets := map[string]string{
		"vault_token":        rootToken,
		"oidc_client_secret": clientSecrets["vault"],
	}
	if vaultAuthEnabled(client, cfg, "oidc") {
		if err = completeInterruptedVaultAuthConfiguration(client, cfg, "oidc", map[string]string{
			"oidc_discovery_url": "https://authentik." + cfg.InternalDomain + "/application/o/vault/",
			"oidc_client_id":     clientIDs["vault"],
			"oidc_client_secret": clientSecrets["vault"],
		}); err != nil {
			return err
		}
		if err = terraformRunner.ImportIfMissing(
			"bootstrap-vault-oidc",
			"terraform/environments/bootstrap/vault-oidc",
			"module.oidc.vault_jwt_auth_backend.oidc",
			"oidc",
			vaultOIDCVariables,
			vaultOIDCSecrets,
		); err != nil {
			return fmt.Errorf("adopting interrupted Vault OIDC backend: %w", err)
		}
	}
	_, err = terraformRunner.ApplyModule(
		"bootstrap-vault-oidc",
		"terraform/environments/bootstrap/vault-oidc",
		vaultOIDCVariables,
		vaultOIDCSecrets,
	)
	return err
}

func writeProtectedControllerFile(path, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("refusing to persist an empty protected value at %s", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, []byte(value), 0600); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
