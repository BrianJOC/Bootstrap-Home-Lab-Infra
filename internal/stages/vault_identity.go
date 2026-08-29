package stages

import (
	"encoding/json"
	"fmt"
	"os"
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

func storeWildcardCertificateInVault(client *ssh.Client, cfg *config.Config, rootToken string) error {
	certificatePath := "/opt/certs/certificates/_." + cfg.InternalDomain + ".crt"
	keyPath := "/opt/certs/certificates/_." + cfg.InternalDomain + ".key"
	script := fmt.Sprintf(`set -Eeuo pipefail
IFS= read -r VAULT_TOKEN
sudo -n env VAULT_TOKEN="$VAULT_TOKEN" VAULT_ADDR=https://127.0.0.1:8200 VAULT_SKIP_VERIFY=true \
  vault kv put secret/bootstrap/tls/wildcard cert=@%q key=@%q domain=%q >/dev/null
`, certificatePath, keyPath, cfg.InternalDomain)
	_, stderr, err := client.RunWithStdin(script, []byte(rootToken+"\n"))
	if err != nil {
		return fmt.Errorf("storing wildcard certificate in Vault: %w: %s", err, strings.TrimSpace(stderr))
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

// completeInterruptedVaultAuthConfiguration repairs the narrow state left by
// vault_jwt_auth_backend when Vault enabled the mount but the provider failed
// before writing its configuration. Values and the root token travel only over
// SSH stdin. Terraform imports the repaired mount immediately afterward and
// remains its durable owner.
func completeInterruptedVaultAuthConfiguration(client *ssh.Client, cfg *config.Config, path string, values map[string]string) error {
	rootToken, err := readVaultRootToken(cfg)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(values)
	if err != nil {
		return fmt.Errorf("encoding interrupted Vault auth configuration: %w", err)
	}
	script := fmt.Sprintf(`set -Eeuo pipefail
IFS= read -r VAULT_TOKEN
export VAULT_TOKEN VAULT_ADDR=https://127.0.0.1:8200 VAULT_SKIP_VERIFY=true
if vault read auth/%s/config >/dev/null 2>&1; then
  exit 0
fi
vault write auth/%s/config - >/dev/null
`, path, path)
	stdin := append([]byte(rootToken+"\n"), payload...)
	stdin = append(stdin, '\n')
	_, stderr, err := client.RunWithStdin(script, stdin)
	if err != nil {
		return fmt.Errorf("completing interrupted Vault auth backend %s: %w: %s", path, err, strings.TrimSpace(stderr))
	}
	return nil
}
