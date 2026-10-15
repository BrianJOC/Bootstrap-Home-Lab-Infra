package stages

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/ansible"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/gitops"
	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
	"github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// GitOpsStage completes the operational handoff after every provider and
// cluster is available. A failed handoff remains a resumable bootstrap stage.
type GitOpsStage struct{}

func (*GitOpsStage) Name() string { return "gitops" }
func (*GitOpsStage) Check(_ context.Context, cfg *config.Config, rc *runcontext.RunContext) (bool, error) {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return false, err
	}
	defer client.Close()
	_, _, err = client.Run("sudo grep -Fxq 'gitops-runtime=1' /var/lib/bootstrap-home-infra/gitops-runtime-configured && sudo test -x /usr/local/sbin/bootstrap-gitops-host-binary && sudo test -x /usr/local/sbin/bootstrap-gitops-host && sudo test -s /etc/bootstrap-home-infra/gitops-host.json")
	return err == nil, nil
}

const day2TokenValidationRuby = `token = STDIN.read.strip
record = PersonalAccessToken.find_by_token(token)
abort('Stored day-2 credential is inactive; deliberate renewal required') unless record && record.active?
puts token`

const day2TokenCreationRuby = `user = User.find_by!(username: 'root')
abort('Bootstrap user is not an administrator') unless user.admin?
record = user.personal_access_tokens.create!(name: 'bootstrap-day2-iac', scopes: ['api'], expires_at: 365.days.from_now)
puts record.token`

// day2CredentialScript runs as root on the GitLab host. Rails only validates
// or creates the token; the privileged shell owns its lock and durable file.
// Parameters are fixed production paths, with test-only overrides for the
// shell integration fixtures.
func day2CredentialScript(directory, railsExecutable string) string {
	return fmt.Sprintf(`set -Eeuo pipefail
umask 077
directory=%s
rails=%s
lock_file="$directory/bootstrap-day2-iac.lock"
token_file="$directory/bootstrap-day2-iac.token"
[[ ! -L "$lock_file" && ! -L "$token_file" ]] || exit 1
exec 9>>"$lock_file"
[[ -O "$lock_file" ]] || exit 1
chmod 0600 "$lock_file"
flock -x 9

if [[ -e "$token_file" ]]; then
  [[ -f "$token_file" && ! -L "$token_file" && -O "$token_file" ]] || exit 1
  [[ "$(find "$token_file" -prune -type f -perm 600 -print)" == "$token_file" ]] || exit 1
  token=$(cat -- "$token_file")
  [[ "$token" =~ ^glpat-[A-Za-z0-9_.-]{16,128}$ ]] || exit 1
  if validated=$(printf '%%s\n' "$token" | "$rails" runner %s 2>/dev/null); then
    [[ "$validated" == "$token" ]] || exit 1
    printf '%%s\n' "$token"
    exit 0
  fi
  # Never replace an existing credential automatically, including an inactive one.
  exit 1
fi

token=$("$rails" runner %s 2>/dev/null) || exit 1
[[ "$token" =~ ^glpat-[A-Za-z0-9_.-]{16,128}$ ]] || exit 1
temporary=$(mktemp "$token_file.tmp.XXXXXX")
trap 'rm -f -- "$temporary"' EXIT
chmod 0600 "$temporary"
printf '%%s' "$token" >"$temporary"
[[ ! -e "$token_file" ]] || exit 1
mv -- "$temporary" "$token_file"
trap - EXIT
printf '%%s\n' "$token"
`, shellSingleQuote(directory), shellSingleQuote(railsExecutable), shellSingleQuote(day2TokenValidationRuby), shellSingleQuote(day2TokenCreationRuby))
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func (*GitOpsStage) Run(ctx context.Context, cfg *config.Config, sec *config.Secrets, rc *runcontext.RunContext, sink stage.Sink) error {
	client, err := ssh.New(cfg.TargetHost, cfg.SSHUser, cfg.SSHKeyPath, rc.KnownHostsPath)
	if err != nil {
		return err
	}
	defer client.Close()
	rootToken, err := readVaultRootToken(cfg)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	read := func(name string) (string, error) {
		data, err := os.ReadFile(filepath.Join(home, ".bootstrap", name))
		if err != nil {
			return "", fmt.Errorf("required handoff credential %s is missing", name)
		}
		return strings.TrimSpace(string(data)), nil
	}
	providers := map[string]string{}
	for field, file := range map[string]string{"technitium_api_token": "technitium-api-token", "authentik_token": "authentik-bootstrap-token", "maas_api_key": "maas-api-key", "oidc_vault_secret": "oidc-vault-secret"} {
		value, err := read(file)
		if err != nil {
			return err
		}
		providers[field] = value
	}
	// Create a dedicated day-2 API credential rather than shipping the two-day
	// bootstrap PAT to CI. Inventory it in Vault, outside GitLab CI variables.
	stdout, _, err := client.Run("sudo bash -c " + shellSingleQuote(day2CredentialScript(
		"/etc/gitlab", "/opt/gitlab/bin/gitlab-rails",
	)))
	if err != nil {
		return fmt.Errorf("creating day-2 GitLab API credential failed")
	}
	providers["gitlab_token"] = strings.TrimSpace(stdout)
	if !strings.HasPrefix(providers["gitlab_token"], "glpat-") {
		return fmt.Errorf("invalid day-2 GitLab credential response")
	}
	runtime := map[string]any{"technitium_admin_password": sec.AdminPassword, "pg_admin_password": sec.AdminPassword, "authentik_admin_password": sec.AdminPassword, "gitlab_admin_password": sec.AdminPassword, "maas_admin_password": sec.AdminPassword, "cf_dns_api_token": sec.CloudflareAPIToken, "restic_primary_password": sec.ResticPrimaryPassword, "restic_usb_password": sec.ResticUSBPassword, "paging_destination_url": sec.PagingDestinationURL}
	for field, file := range map[string]string{"authentik_secret_key": "authentik-secret-key", "authentik_db_password": "pg-authentik-password", "gitlab_db_password": "pg-gitlab-password", "maas_db_password": "pg-maas-password", "authentik_bootstrap_token": "authentik-bootstrap-token", "gitlab_oidc_secret": "oidc-gitlab-secret"} {
		value, err := read(file)
		if err != nil {
			return err
		}
		runtime[field] = value
	}
	for path, value := range map[string]any{"providers": providers, "runtime": runtime} {
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		script := `set -Eeuo pipefail
IFS= read -r VAULT_TOKEN
export VAULT_TOKEN VAULT_ADDR=https://127.0.0.1:8200 VAULT_SKIP_VERIFY=true
vault kv put secret/bootstrap/gitops/` + path + ` - >/dev/null
`
		if _, _, err = client.RunWithStdin(script, append([]byte(rootToken+"\n"), data...)); err != nil {
			return fmt.Errorf("storing GitOps %s in Vault failed", path)
		}
	}
	architecture, _, err := client.Run("uname -m")
	if err != nil {
		return err
	}
	goarch := ""
	switch strings.TrimSpace(architecture) {
	case "aarch64":
		goarch = "arm64"
	case "x86_64":
		goarch = "amd64"
	default:
		return fmt.Errorf("unsupported GitOps runner architecture")
	}
	work, err := os.MkdirTemp("", "bootstrap-gitops-helper-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	binary := filepath.Join(work, "gitops")
	build := exec.CommandContext(ctx, "bin/go", "build", "-trimpath", "-o", binary, "./cmd/gitops")
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		if key != "GOOS" && key != "GOARCH" && key != "CGO_ENABLED" && key != "GOCACHE" {
			build.Env = append(build.Env, value)
		}
	}
	build.Env = append(build.Env, "GOOS=linux", "GOARCH="+goarch, "CGO_ENABLED=0", "GOCACHE="+filepath.Join(os.TempDir(), "bootstrap-home-infra-go-cache"))
	if output, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("building GitOps host helper: %w: %s", err, output)
	}
	identity, _ := json.Marshal(gitops.Platform{Schema: 1, Domain: cfg.InternalDomain, Address: cfg.BootstrapServerIP, VaultAddress: "https://vault." + cfg.InternalDomain + ":8200", ProjectPath: "infrastructure/iac"})
	if err = ansible.New(client, sink).RunPlaybook("ansible/playbooks/stage-10-gitops.yml", cfg.TargetHost+",", map[string]string{"gitops_binary_path": binary, "gitops_host_identity": string(identity)}); err != nil {
		return err
	}
	sink.Log("info", "gitops", "GitLab management source, Vault credentials, and protected host executor are ready", nil)
	return nil
}
