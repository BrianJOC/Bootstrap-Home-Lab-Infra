package stages

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
)

func readVaultRootToken(cfg *config.Config) (string, error) {
	content, err := os.ReadFile(cfg.VaultInitOutputPath)
	if err != nil {
		return "", fmt.Errorf("reading Vault init material: %w", err)
	}
	var material struct {
		RootToken string `json:"root_token"`
	}
	if err := json.Unmarshal(content, &material); err != nil {
		return "", fmt.Errorf("decoding Vault init material: %w", err)
	}
	if material.RootToken == "" {
		return "", fmt.Errorf("Vault init material has no root_token")
	}
	return material.RootToken, nil
}

func configureVaultOIDC(client *ssh.Client, cfg *config.Config) error {
	rootToken, err := readVaultRootToken(cfg)
	if err != nil {
		return err
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	secret, err := os.ReadFile(filepath.Join(homeDir, ".bootstrap", "oidc-vault-secret"))
	if err != nil {
		return fmt.Errorf("reading Vault OIDC secret: %w", err)
	}
	script := fmt.Sprintf(`set -Eeuo pipefail
IFS= read -r VAULT_TOKEN
IFS= read -r OIDC_SECRET
export VAULT_TOKEN VAULT_ADDR=https://127.0.0.1:8200 VAULT_SKIP_VERIFY=true
install -m 0600 /dev/null /tmp/bootstrap-vault-oidc-secret
printf '%%s' "$OIDC_SECRET" > /tmp/bootstrap-vault-oidc-secret
vault auth list -format=json | grep -q '"oidc/"' || vault auth enable oidc
vault write auth/oidc/config \
  oidc_discovery_url=https://authentik.%[1]s/application/o/vault/ \
  oidc_client_id=vault \
  oidc_client_secret=@/tmp/bootstrap-vault-oidc-secret >/dev/null
vault write auth/oidc/role/default \
  bound_audiences=vault \
  allowed_redirect_uris=https://vault.%[1]s:8200/ui/vault/auth/oidc/oidc/callback \
  user_claim=sub policies=default >/dev/null
rm -f /tmp/bootstrap-vault-oidc-secret
`, cfg.InternalDomain)
	_, stderr, err := client.RunWithStdin(
		script,
		[]byte(rootToken+"\n"+strings.TrimSpace(string(secret))+"\n"),
	)
	if err != nil {
		return fmt.Errorf("configuring Vault OIDC: %w: %s", err, strings.TrimSpace(stderr))
	}
	return nil
}

func configureVaultGitLabJWT(client *ssh.Client, cfg *config.Config) error {
	rootToken, err := readVaultRootToken(cfg)
	if err != nil {
		return err
	}
	script := fmt.Sprintf(`set -Eeuo pipefail
IFS= read -r VAULT_TOKEN
export VAULT_TOKEN VAULT_ADDR=https://127.0.0.1:8200 VAULT_SKIP_VERIFY=true
cat >/tmp/bootstrap-gitlab-ci-policy.hcl <<'POLICY'
path "secret/data/infrastructure/*" {
  capabilities = ["read"]
}
path "secret/data/services/*" {
  capabilities = ["read"]
}
POLICY
vault policy write gitlab-ci /tmp/bootstrap-gitlab-ci-policy.hcl >/dev/null
vault auth list -format=json | grep -q '"jwt/"' || vault auth enable -path=jwt jwt
vault write auth/jwt/config \
  jwks_url=https://gitlab.%[1]s/-/jwks \
  bound_issuer=https://gitlab.%[1]s >/dev/null
vault write auth/jwt/role/gitlab-ci \
  role_type=jwt \
  bound_audiences=https://vault.%[1]s:8200 \
  user_claim=sub token_policies=gitlab-ci token_ttl=1h >/dev/null
rm -f /tmp/bootstrap-gitlab-ci-policy.hcl
`, cfg.InternalDomain)
	_, stderr, err := client.RunWithStdin(script, []byte(rootToken+"\n"))
	if err != nil {
		return fmt.Errorf("configuring Vault GitLab JWT: %w: %s", err, strings.TrimSpace(stderr))
	}
	return nil
}

func vaultAuthEnabled(client *ssh.Client, cfg *config.Config, path string) bool {
	rootToken, err := readVaultRootToken(cfg)
	if err != nil {
		return false
	}
	command := fmt.Sprintf(
		`IFS= read -r VAULT_TOKEN; export VAULT_TOKEN VAULT_ADDR=https://127.0.0.1:8200 VAULT_SKIP_VERIFY=true; vault auth list -format=json | grep -q %q`,
		`"`+path+`/"`,
	)
	_, _, err = client.RunWithStdin(command, []byte(rootToken+"\n"))
	return err == nil
}
