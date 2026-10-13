package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/term"

	"github.com/homelab/bootstrap-home-infra/internal/adoption"
	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/logging"
	"github.com/homelab/bootstrap-home-infra/internal/recovery"
	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
	bootstrapssh "github.com/homelab/bootstrap-home-infra/internal/ssh"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
	"github.com/homelab/bootstrap-home-infra/internal/stages"
	"github.com/homelab/bootstrap-home-infra/internal/tui"
)

var (
	flagTarget                  string
	flagStage                   string
	flagHeadless                bool
	flagWizard                  bool
	flagAcceptNewHostKey        bool
	flagReplaceHostKey          bool
	flagApproveTerraform        bool
	flagConfig                  string
	flagEnv                     string
	flagRecoveryBundle          string
	flagRecoveryOutput          string
	flagVaultConfig             string
	flagVaultEnv                string
	flagVaultUnsealFromInitFile bool
	flagOperationConfig         string
	flagOperationEnv            string
	flagBackupSnapshot          string
	flagBackupTarget            string
	flagAdoptionTarget          string
	flagAdoptionOutput          string
	flagApproveAdoption         bool
	flagGitLabRecoveryMode      string
	flagGitLabRecoveryIncident  string
	flagGitLabRecoverySnapshot  string
	flagGitLabRestoreApproval   string
	flagGitLabUnfreezeIncident  string
)

var rootCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Bootstrap home infrastructure",
	Long: `bootstrap orchestrates the multi-stage setup of a self-hosted homelab.

It supports both an interactive TUI mode (default) and a headless mode for
re-runs and CI automation.`,
	RunE: runBootstrap,
}

func init() {
	rootCmd.Flags().StringVar(&flagTarget, "target", "", "Override target host from config")
	rootCmd.Flags().StringVar(&flagStage, "stage", "", "Run only this stage by name")
	rootCmd.Flags().BoolVar(&flagHeadless, "headless", false, "No TUI, log output only")
	rootCmd.Flags().BoolVar(&flagWizard, "wizard", false, "Run setup wizard (pre-fills from existing config if present)")
	rootCmd.Flags().BoolVar(&flagAcceptNewHostKey, "accept-new-host-key", false, "Accept and pin an unknown SSH host key")
	rootCmd.Flags().BoolVar(&flagReplaceHostKey, "replace-host-key", false, "Replace a changed SSH host key only when ssh_host_fingerprint matches")
	rootCmd.Flags().BoolVar(&flagApproveTerraform, "approve-terraform", false, "Approve saved Terraform plans in headless mode")
	rootCmd.Flags().StringVar(&flagConfig, "config", "./config.yaml", "Path to config.yaml")
	rootCmd.Flags().StringVar(&flagEnv, "env", "./.env", "Path to .env file")
	recoverControllerCmd.Flags().StringVar(&flagRecoveryBundle, "bundle", "", "Path to controller-bundle.tar.gz")
	recoverControllerCmd.Flags().StringVar(&flagRecoveryOutput, "output-dir", "", "Protected staging directory for verified controller files")
	rootCmd.AddCommand(recoverControllerCmd)
	vaultCmd.PersistentFlags().StringVar(&flagVaultConfig, "config", "./config.yaml", "Path to config.yaml")
	vaultCmd.PersistentFlags().StringVar(&flagVaultEnv, "env", "./.env", "Path to .env")
	vaultCmd.AddCommand(vaultStatusCmd, vaultUnsealCmd, vaultReconcileCmd)
	vaultUnsealCmd.Flags().BoolVar(&flagVaultUnsealFromInitFile, "from-init-file", false, "Read shares from vault_init_output_path (explicit recovery or disposable automation only)")
	rootCmd.AddCommand(vaultCmd)
	addOperationFlags(backupCmd)
	backupRestoreCmd.Flags().StringVar(&flagBackupSnapshot, "snapshot", "", "Restic snapshot ID or latest")
	backupRestoreCmd.Flags().StringVar(&flagBackupTarget, "target", "", "Isolated target under /var/lib/bootstrap-home-infra/restore-drills")
	backupCmd.AddCommand(backupStatusCmd, backupRunCmd, backupRestoreCmd)
	rootCmd.AddCommand(backupCmd)
	addOperationFlags(certificateCmd)
	certificateCmd.AddCommand(certificateStatusCmd, certificateRenewCmd)
	rootCmd.AddCommand(certificateCmd)
	addOperationFlags(adoptServerCmd)
	adoptServerCmd.Flags().StringVar(&flagAdoptionTarget, "target", "", "Server address to audit (defaults to config target)")
	adoptServerCmd.Flags().StringVar(&flagAdoptionOutput, "output", "", "Write a read-only YAML draft instead of opening a GitLab MR")
	adoptServerCmd.Flags().BoolVar(&flagApproveAdoption, "approve", false, "Approve opening the displayed adoption merge request in non-interactive mode")
	rootCmd.AddCommand(adoptServerCmd)
	addOperationFlags(recoverCmd)
	gitLabRecoverCmd.Flags().StringVar(&flagGitLabRecoveryMode, "mode", "", "Recovery mode: service or restore")
	gitLabRecoverCmd.Flags().StringVar(&flagGitLabRecoveryIncident, "incident", "", "Stable incident identifier recorded in recovery evidence")
	gitLabRecoverCmd.Flags().StringVar(&flagGitLabRecoverySnapshot, "snapshot", "latest", "Complete Restic snapshot ID or latest (restore mode)")
	gitLabRecoverCmd.Flags().StringVar(&flagGitLabRestoreApproval, "approve-restore", "", "Typed incident identifier required before live restore")
	gitLabRecoverCmd.Flags().StringVar(&flagGitLabUnfreezeIncident, "unfreeze", "", "Explicitly unfreeze this verified incident")
	recoverCmd.AddCommand(gitLabRecoverCmd)
	rootCmd.AddCommand(recoverCmd)
}

func addOperationFlags(command *cobra.Command) {
	command.PersistentFlags().StringVar(&flagOperationConfig, "config", "./config.yaml", "Path to config.yaml")
	command.PersistentFlags().StringVar(&flagOperationEnv, "env", "./.env", "Path to .env")
}

var recoverControllerCmd = &cobra.Command{
	Use:   "recover-controller",
	Short: "Verify and open a portable controller recovery bundle",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		if flagRecoveryBundle == "" || flagRecoveryOutput == "" {
			return fmt.Errorf("--bundle and --output-dir are required")
		}
		info, err := recovery.Inspect(flagRecoveryBundle)
		if err != nil {
			return fmt.Errorf("verifying recovery bundle: %w", err)
		}
		fmt.Printf("Verified controller bundle for %s\n", info.Target)
		if info.Fingerprint != "" {
			fmt.Printf("Expected SSH fingerprint: %s\n", info.Fingerprint)
		}

		password := os.Getenv("CONTROLLER_RECOVERY_PASSWORD")
		if password == "" {
			fmt.Fprint(os.Stderr, "Controller recovery password: ")
			value, readErr := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Fprintln(os.Stderr)
			if readErr != nil {
				return fmt.Errorf("reading recovery password: %w", readErr)
			}
			password = strings.TrimSpace(string(value))
		}
		if err := recovery.Restore(flagRecoveryBundle, flagRecoveryOutput, password); err != nil {
			return fmt.Errorf("opening recovery bundle: %w", err)
		}
		fmt.Printf("Verified files staged at %s; inspect them before explicit controller adoption.\n", flagRecoveryOutput)
		return nil
	},
}

var vaultCmd = &cobra.Command{
	Use:   "vault",
	Short: "Operate manual Vault reboot recovery over pinned SSH",
}

var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Inspect, run, or perform an isolated Restic restore drill",
}

var recoverCmd = &cobra.Command{
	Use:   "recover",
	Short: "Run guarded target-service recovery workflows",
}

var gitLabRecoverCmd = &cobra.Command{
	Use:   "gitlab",
	Short: "Freeze, recover, verify, and explicitly unfreeze GitLab",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		client, _, err := operationSSHClient(flagOperationConfig, flagOperationEnv, "")
		if err != nil {
			return err
		}
		defer client.Close()

		if flagGitLabUnfreezeIncident != "" {
			if flagGitLabRecoveryMode != "" || flagGitLabRecoveryIncident != "" || flagGitLabRestoreApproval != "" {
				return fmt.Errorf("--unfreeze cannot be combined with --mode, --incident, or --approve-restore")
			}
			if err := recovery.ValidateIncidentID(flagGitLabUnfreezeIncident); err != nil {
				return err
			}
			stdout, stderr, runErr := client.RunWithStdin(
				"sudo -n /usr/local/sbin/bootstrap-gitlab-recovery unfreeze",
				[]byte(flagGitLabUnfreezeIncident+"\n"),
			)
			fmt.Print(stdout)
			if runErr != nil {
				return fmt.Errorf("GitLab unfreeze failed: %w: %s", runErr, strings.TrimSpace(stderr))
			}
			return nil
		}

		if err := recovery.ValidateIncidentID(flagGitLabRecoveryIncident); err != nil {
			return err
		}
		switch flagGitLabRecoveryMode {
		case "service":
			stdout, stderr, runErr := client.RunWithStdin(
				"sudo -n /usr/local/sbin/bootstrap-gitlab-recovery service",
				[]byte(flagGitLabRecoveryIncident+"\n"),
			)
			fmt.Print(stdout)
			if runErr != nil {
				return fmt.Errorf("GitLab service recovery failed and remains frozen: %w: %s", runErr, strings.TrimSpace(stderr))
			}
			return nil
		case "restore":
			if flagGitLabRestoreApproval != flagGitLabRecoveryIncident {
				return fmt.Errorf("live GitLab restore requires --approve-restore %s", flagGitLabRecoveryIncident)
			}
			if err := recovery.ValidateSnapshotID(flagGitLabRecoverySnapshot); err != nil {
				return err
			}
			payload := strings.Join([]string{
				flagGitLabRecoveryIncident,
				flagGitLabRecoverySnapshot,
				flagGitLabRestoreApproval,
				"",
			}, "\n")
			stdout, stderr, runErr := client.RunWithStdin(
				"sudo -n /usr/local/sbin/bootstrap-gitlab-recovery restore",
				[]byte(payload),
			)
			fmt.Print(stdout)
			if runErr != nil {
				return fmt.Errorf("GitLab restore failed and remains frozen: %w: %s", runErr, strings.TrimSpace(stderr))
			}
			return nil
		default:
			return fmt.Errorf("--mode must be service or restore")
		}
	},
}

var backupStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the backup timer, latest job, and newest snapshot",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		client, _, err := operationSSHClient(flagOperationConfig, flagOperationEnv, "")
		if err != nil {
			return err
		}
		defer client.Close()
		stdout, stderr, err := client.Run(`sudo -n systemctl status --no-pager bootstrap-backup.timer bootstrap-backup.service || true
if ! sudo -n test -f /etc/bootstrap-home-infra/backup/primary.env; then
  sudo -n cat /var/lib/bootstrap-home-infra/backup-status 2>/dev/null || echo "backup protection is not configured"
  exit 0
fi
sudo -n sh -c 'set -a; . /etc/bootstrap-home-infra/backup/primary.env; set +a; restic snapshots --latest 1'`)
		fmt.Print(stdout)
		if err != nil {
			return fmt.Errorf("reading backup status: %w: %s", err, strings.TrimSpace(stderr))
		}
		return nil
	},
}

var backupRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the application-consistent backup service now",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		client, _, err := operationSSHClient(flagOperationConfig, flagOperationEnv, "")
		if err != nil {
			return err
		}
		defer client.Close()
		stdout, stderr, err := client.Run(`sudo -n systemctl start bootstrap-backup.service || exit $?
sudo -n systemctl status --no-pager bootstrap-backup.service || true`)
		fmt.Print(stdout)
		if err != nil {
			return fmt.Errorf("running backup: %w: %s", err, strings.TrimSpace(stderr))
		}
		return nil
	},
}

var backupRestoreCmd = &cobra.Command{
	Use:   "restore",
	Short: "Restore a snapshot into an isolated drill directory without touching live data",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		if flagBackupSnapshot == "" || flagBackupTarget == "" {
			return fmt.Errorf("--snapshot and --target are required")
		}
		if flagBackupSnapshot != "latest" {
			for _, character := range flagBackupSnapshot {
				if !strings.ContainsRune("0123456789abcdef", character) {
					return fmt.Errorf("--snapshot must be latest or a lowercase hexadecimal snapshot ID")
				}
			}
			if len(flagBackupSnapshot) < 8 || len(flagBackupSnapshot) > 64 {
				return fmt.Errorf("--snapshot must contain 8 to 64 hexadecimal characters")
			}
		}
		cleanTarget := filepath.Clean(flagBackupTarget)
		restoreRoot := "/var/lib/bootstrap-home-infra/restore-drills"
		if !strings.HasPrefix(cleanTarget, restoreRoot+"/") {
			return fmt.Errorf("--target must be a new directory below %s", restoreRoot)
		}
		client, _, err := operationSSHClient(flagOperationConfig, flagOperationEnv, "")
		if err != nil {
			return err
		}
		defer client.Close()
		script := `set -Eeuo pipefail
IFS= read -r SNAPSHOT
IFS= read -r TARGET
test ! -e "$TARGET"
sudo -n install -d -m 0700 "$TARGET"
sudo -n sh -c 'set -a; . /etc/bootstrap-home-infra/backup/primary.env; set +a; restic restore "$1" --target "$2"' sh "$SNAPSHOT" "$TARGET"
sudo -n find "$TARGET" -xdev -type f -print0 | sudo -n xargs -0 sha256sum > /tmp/bootstrap-restore-drill.sha256
echo "Isolated restore completed at $TARGET"
`
		stdout, stderr, err := client.RunWithStdin(script, []byte(flagBackupSnapshot+"\n"+cleanTarget+"\n"))
		fmt.Print(stdout)
		if err != nil {
			return fmt.Errorf("isolated backup restore failed: %w: %s", err, strings.TrimSpace(stderr))
		}
		return nil
	},
}

var certificateCmd = &cobra.Command{
	Use:   "certificate",
	Short: "Inspect or trigger the host-managed certificate renewal workflow",
}

var certificateStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the active wildcard certificate and renewal timer",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		client, runConfig, err := operationSSHClient(flagOperationConfig, flagOperationEnv, "")
		if err != nil {
			return err
		}
		defer client.Close()
		command := fmt.Sprintf(
			`openssl x509 -fingerprint -sha256 -issuer -subject -dates -noout -in /opt/certs/certificates/_.%s.crt; sudo -n systemctl status --no-pager bootstrap-certificate-renew.timer || true`,
			runConfig.InternalDomain,
		)
		stdout, stderr, err := client.Run(command)
		fmt.Print(stdout)
		if err != nil {
			return fmt.Errorf("reading certificate status: %w: %s", err, strings.TrimSpace(stderr))
		}
		return nil
	},
}

var certificateRenewCmd = &cobra.Command{
	Use:   "renew",
	Short: "Run the staged validation and atomic certificate renewal service now",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		client, _, err := operationSSHClient(flagOperationConfig, flagOperationEnv, "")
		if err != nil {
			return err
		}
		defer client.Close()
		stdout, stderr, err := client.Run(`sudo -n systemctl start bootstrap-certificate-renew.service && sudo -n systemctl status --no-pager bootstrap-certificate-renew.service`)
		fmt.Print(stdout)
		if err != nil {
			return fmt.Errorf("certificate renewal failed: %w: %s", err, strings.TrimSpace(stderr))
		}
		return nil
	},
}

var adoptServerCmd = &cobra.Command{
	Use:   "adopt-server",
	Short: "Create a read-only server adoption draft for GitLab review",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		client, runConfig, err := operationSSHClient(flagOperationConfig, flagOperationEnv, flagAdoptionTarget)
		if err != nil {
			return err
		}
		defer client.Close()
		stdout, stderr, err := client.Run(adoption.AuditCommand)
		if err != nil {
			return fmt.Errorf("auditing adoption target: %w: %s", err, strings.TrimSpace(stderr))
		}
		audit, err := adoption.Parse(runConfig.TargetHost, stdout)
		if err != nil {
			return fmt.Errorf("parsing adoption audit: %w", err)
		}
		document, err := adoption.RenderNode(audit)
		if err != nil {
			return fmt.Errorf("rendering node definition: %w", err)
		}
		if flagAdoptionOutput != "" {
			if err := os.WriteFile(flagAdoptionOutput+".tmp", document, 0600); err != nil {
				return err
			}
			if err := os.Rename(flagAdoptionOutput+".tmp", flagAdoptionOutput); err != nil {
				return err
			}
			fmt.Printf("Read-only adoption draft written to %s; no GitLab change was made.\n", flagAdoptionOutput)
			return nil
		}
		fmt.Printf("Proposed GitLab change: create infrastructure/nodes/%s.yaml\n\n%s\n", audit.Hostname, document)
		if !flagApproveAdoption {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("opening the adoption merge request requires an interactive confirmation or --approve")
			}
			fmt.Fprint(os.Stderr, "Open this draft merge request? [y/N]: ")
			answer, readErr := bufio.NewReader(os.Stdin).ReadString('\n')
			if readErr != nil {
				return fmt.Errorf("reading adoption approval: %w", readErr)
			}
			answer = strings.ToLower(strings.TrimSpace(answer))
			if answer != "y" && answer != "yes" {
				return fmt.Errorf("adoption merge request was not approved")
			}
		}
		token, err := gitLabAdoptionCredential(runConfig, flagOperationConfig, flagOperationEnv)
		if err != nil {
			return err
		}
		gitlab := adoption.NewGitLabClientWithTLS(gitLabOperationBaseURL(runConfig), token, runConfig.APIInsecure)
		mergeRequestURL, err := gitlab.OpenAdoptionMergeRequest(audit, document)
		if err != nil {
			return err
		}
		fmt.Printf("MR opened: %s\n", mergeRequestURL)
		return nil
	},
}

var gitLabPATPattern = regexp.MustCompile(`(?m)^glpat-[A-Za-z0-9_.-]{16,128}$`)

func gitLabOperationBaseURL(cfg *config.Config) string {
	if cfg.GitLabBaseURL != "" {
		return strings.TrimRight(cfg.GitLabBaseURL, "/")
	}
	return "https://gitlab." + cfg.InternalDomain
}

// gitLabAdoptionCredential validates the controller's short-lived PAT and
// mints a replacement over the already pinned bootstrap-host SSH connection
// only when it has expired or been revoked. This keeps adoption usable on day
// two without turning the local bootstrap runner back into a Terraform owner.
func gitLabAdoptionCredential(cfg *config.Config, configPath, envPath string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	tokenPath := filepath.Join(homeDir, ".bootstrap", "gitlab-api-token")
	tokenBytes, readErr := os.ReadFile(tokenPath)
	token := strings.TrimSpace(string(tokenBytes))
	gitlab := adoption.NewGitLabClientWithTLS(gitLabOperationBaseURL(cfg), token, cfg.APIInsecure)
	if readErr == nil && token != "" {
		valid, validateErr := gitlab.ValidateCredential()
		if validateErr != nil {
			return "", fmt.Errorf("validating GitLab adoption credential: %w", validateErr)
		}
		if valid {
			return token, nil
		}
	} else if readErr != nil && !os.IsNotExist(readErr) {
		return "", fmt.Errorf("reading GitLab adoption credential: %w", readErr)
	}

	controller, _, err := operationSSHClient(configPath, envPath, "")
	if err != nil {
		return "", fmt.Errorf("opening pinned bootstrap-host connection for GitLab credential refresh: %w", err)
	}
	defer controller.Close()
	stdout, stderr, err := controller.Run(`sudo gitlab-rails runner "user = User.find(1); user.personal_access_tokens.where(name: 'bootstrap-adoption-token').destroy_all; token = user.personal_access_tokens.create!(name: 'bootstrap-adoption-token', scopes: ['api'], expires_at: 2.days.from_now); puts token.token"`)
	if err != nil {
		return "", fmt.Errorf("refreshing GitLab adoption credential: %w: %s", err, strings.TrimSpace(stderr))
	}
	refreshed := gitLabPATPattern.FindString(stdout)
	if refreshed == "" {
		return "", fmt.Errorf("GitLab did not return a valid refreshed adoption credential")
	}
	if err = os.MkdirAll(filepath.Dir(tokenPath), 0700); err != nil {
		return "", err
	}
	temporary := tokenPath + ".tmp"
	if err = os.WriteFile(temporary, []byte(refreshed+"\n"), 0600); err != nil {
		return "", err
	}
	if err = os.Rename(temporary, tokenPath); err != nil {
		return "", err
	}
	valid, err := adoption.NewGitLabClientWithTLS(gitLabOperationBaseURL(cfg), refreshed, cfg.APIInsecure).ValidateCredential()
	if err != nil {
		return "", fmt.Errorf("validating refreshed GitLab adoption credential: %w", err)
	}
	if !valid {
		return "", fmt.Errorf("GitLab rejected the refreshed adoption credential")
	}
	fmt.Fprintln(os.Stderr, "Refreshed the expired short-lived GitLab adoption credential over pinned SSH.")
	return refreshed, nil
}

var vaultStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show Vault seal and health state",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		client, _, err := operationalSSHClient()
		if err != nil {
			return err
		}
		defer client.Close()
		stdout, stderr, err := client.Run(
			"rc=0; VAULT_ADDR=https://127.0.0.1:8200 VAULT_SKIP_VERIFY=true vault status -format=json || rc=$?; [ \"$rc\" -eq 0 ] || [ \"$rc\" -eq 2 ]",
		)
		fmt.Print(stdout)
		if err != nil {
			return fmt.Errorf("Vault status failed: %w: %s", err, strings.TrimSpace(stderr))
		}
		return nil
	},
}

var vaultUnsealCmd = &cobra.Command{
	Use:   "unseal",
	Short: "Submit the configured Shamir threshold without exposing shares in arguments",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		client, runConfig, err := operationalSSHClient()
		if err != nil {
			return err
		}
		defer client.Close()
		var fileShares []string
		if flagVaultUnsealFromInitFile {
			initBytes, readErr := os.ReadFile(runConfig.VaultInitOutputPath)
			if readErr != nil {
				return fmt.Errorf("reading Vault recovery JSON: %w", readErr)
			}
			var initMaterial struct {
				UnsealKeys []string `json:"unseal_keys_b64"`
			}
			if err := json.Unmarshal(initBytes, &initMaterial); err != nil {
				return fmt.Errorf("decoding Vault recovery JSON: %w", err)
			}
			if len(initMaterial.UnsealKeys) < runConfig.VaultThreshold {
				return fmt.Errorf("Vault recovery JSON has %d shares; threshold is %d", len(initMaterial.UnsealKeys), runConfig.VaultThreshold)
			}
			fileShares = initMaterial.UnsealKeys
		} else if !term.IsTerminal(int(os.Stdin.Fd())) {
			return fmt.Errorf("manual unseal requires an interactive terminal; use --from-init-file only for explicit recovery or disposable automation")
		}
		unsealed := false
		for index := 0; index < runConfig.VaultThreshold; index++ {
			share := ""
			if flagVaultUnsealFromInitFile {
				share = fileShares[index]
			} else {
				fmt.Fprintf(os.Stderr, "Vault unseal share %d of %d: ", index+1, runConfig.VaultThreshold)
				value, readErr := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(os.Stderr)
				if readErr != nil {
					return fmt.Errorf("reading Vault unseal share %d: %w", index+1, readErr)
				}
				share = strings.TrimSpace(string(value))
				if share == "" {
					return fmt.Errorf("Vault unseal share %d is empty", index+1)
				}
			}
			payload, marshalErr := json.Marshal(map[string]string{"key": share})
			share = ""
			if marshalErr != nil {
				return fmt.Errorf("encoding Vault unseal share %d: %w", index+1, marshalErr)
			}
			stdout, stderr, unsealErr := client.RunWithStdin(
				"curl --silent --show-error --fail-with-body --insecure --request POST --header 'Content-Type: application/json' --data-binary @- https://127.0.0.1:8200/v1/sys/unseal",
				append(payload, '\n'),
			)
			if unsealErr != nil {
				return fmt.Errorf("submitting Vault unseal share %d: %w: %s", index+1, unsealErr, strings.TrimSpace(stderr))
			}
			var response struct {
				Sealed   bool `json:"sealed"`
				Progress int  `json:"progress"`
			}
			if err := json.Unmarshal([]byte(stdout), &response); err != nil {
				return fmt.Errorf("decoding Vault unseal response for share %d: %w", index+1, err)
			}
			fmt.Printf("Accepted unseal share %d of %d\n", index+1, runConfig.VaultThreshold)
			if !response.Sealed {
				unsealed = true
				break
			}
		}
		if !unsealed {
			return fmt.Errorf("Vault remains sealed after submitting the configured threshold")
		}
		fmt.Println("Vault unsealed. Run `bootstrap vault reconcile` to clear deferred secret synchronization.")
		return nil
	},
}

var vaultReconcileCmd = &cobra.Command{
	Use:   "reconcile",
	Short: "Reconcile deferred certificate inventory and the scoped backup token",
	Args:  cobra.NoArgs,
	RunE: func(_ *cobra.Command, _ []string) error {
		client, runConfig, err := operationalSSHClient()
		if err != nil {
			return err
		}
		defer client.Close()
		initBytes, err := os.ReadFile(runConfig.VaultInitOutputPath)
		if err != nil {
			return fmt.Errorf("reading Vault recovery JSON: %w", err)
		}
		var initMaterial struct {
			RootToken string `json:"root_token"`
		}
		if err := json.Unmarshal(initBytes, &initMaterial); err != nil {
			return fmt.Errorf("decoding Vault recovery JSON: %w", err)
		}
		if initMaterial.RootToken == "" {
			return fmt.Errorf("Vault recovery JSON does not contain root_token")
		}
		script := fmt.Sprintf(`set -Eeuo pipefail
IFS= read -r VAULT_TOKEN
export VAULT_TOKEN VAULT_ADDR=https://127.0.0.1:8200 VAULT_SKIP_VERIFY=true
vault status >/dev/null
SECRET_MOUNTS="$(vault secrets list -format=json)"
case "$SECRET_MOUNTS" in *'"secret/"'*) ;; *) exit 1 ;; esac
sudo -n env VAULT_TOKEN="$VAULT_TOKEN" VAULT_ADDR="$VAULT_ADDR" VAULT_SKIP_VERIFY=true \
  vault kv put secret/bootstrap/tls/wildcard \
  cert=@/opt/certs/certificates/_.%[1]s.crt \
  key=@/opt/certs/certificates/_.%[1]s.key \
  domain=%[1]s >/dev/null
sudo -n env VAULT_TOKEN="$VAULT_TOKEN" VAULT_ADDR="$VAULT_ADDR" VAULT_SKIP_VERIFY=true \
  vault policy write bootstrap-backup - >/dev/null <<'POLICY'
path "sys/storage/raft/snapshot" {
  capabilities = ["read"]
}
POLICY
sudo -n env VAULT_TOKEN="$VAULT_TOKEN" VAULT_ADDR="$VAULT_ADDR" VAULT_SKIP_VERIFY=true \
  vault token create -policy=bootstrap-backup -period=24h -orphan -format=json |
  jq -r .auth.client_token |
  sudo -n install -m 0600 /dev/stdin /etc/bootstrap-home-infra/vault-backup-token
AUTH_MOUNTS="$(vault auth list -format=json)"
case "$AUTH_MOUNTS" in *'"oidc/"'*) ;; *) exit 1 ;; esac
case "$AUTH_MOUNTS" in *'"jwt/"'*) ;; *) exit 1 ;; esac
sudo -n rm -f /var/lib/bootstrap-home-infra/certificate-vault-sync-pending
`, runConfig.InternalDomain)
		_, stderr, err := client.RunWithStdin(script, []byte(initMaterial.RootToken+"\n"))
		if err != nil {
			return fmt.Errorf("reconciling Vault-backed runtime state: %w: %s", err, strings.TrimSpace(stderr))
		}
		fmt.Println("Vault reconciliation complete; certificate inventory and scoped backup token are current.")
		return nil
	},
}

func operationalSSHClient() (*bootstrapssh.Client, *config.Config, error) {
	return operationSSHClient(flagVaultConfig, flagVaultEnv, "")
}

func operationSSHClient(configPath, envPath, targetOverride string) (*bootstrapssh.Client, *config.Config, error) {
	runConfig, err := config.Load(configPath, envPath)
	if err != nil {
		return nil, nil, err
	}
	if targetOverride != "" {
		runConfig.TargetHost = targetOverride
	}
	knownHostsPath, err := bootstrapssh.DefaultKnownHostsPath()
	if err != nil {
		return nil, nil, err
	}
	client, err := bootstrapssh.New(
		runConfig.TargetHost,
		runConfig.SSHUser,
		runConfig.SSHKeyPath,
		knownHostsPath,
	)
	if err != nil {
		return nil, nil, err
	}
	return client, runConfig.Config, nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runBootstrap(cmd *cobra.Command, args []string) error {
	// Load configuration.
	rc, err := config.Load(flagConfig, flagEnv)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Apply --target override if provided.
	if flagTarget != "" {
		rc.TargetHost = flagTarget
	}

	// In headless mode (except config-collection), validate config strictly.
	if flagHeadless && flagStage != "config-collection" {
		if err := config.Validate(rc); err != nil {
			return fmt.Errorf("config validation failed (headless mode requires complete config): %w", err)
		}
	}

	// Resolve an absolute run directory so log paths are stable regardless of
	// working-directory changes later in the process (S6/S8).
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}
	runSlug := "run-" + time.Now().UTC().Format("20060102T150405")
	runDir := fmt.Sprintf("%s/logs/%s", cwd, runSlug)

	logger, err := logging.NewLogger(runDir)
	if err != nil {
		return fmt.Errorf("initializing logger: %w", err)
	}
	defer logger.Sync() //nolint:errcheck

	logger.Info("bootstrap started",
		zap.String("target", rc.TargetHost),
		zap.Int("pid", os.Getpid()),
	)

	logPath := runDir + "/bootstrap.log"
	rctx := runcontext.New(runDir)
	knownHostsPath, err := bootstrapssh.DefaultKnownHostsPath()
	if err != nil {
		return fmt.Errorf("resolving SSH trust store: %w", err)
	}
	rctx.KnownHostsPath = knownHostsPath
	rctx.Headless = flagHeadless
	rctx.AcceptNewHostKey = flagAcceptNewHostKey
	rctx.ReplaceHostKey = flagReplaceHostKey
	rctx.ApproveTerraform = flagApproveTerraform

	// Resolve absolute paths for config.yaml and .env so Stage 0 writes them
	// to exactly where the operator pointed the flags, not a relative CWD path.
	absConfig, err := filepath.Abs(flagConfig)
	if err != nil {
		return fmt.Errorf("resolving config path: %w", err)
	}
	absEnv, err := filepath.Abs(flagEnv)
	if err != nil {
		return fmt.Errorf("resolving env path: %w", err)
	}
	rctx.ConfigPath = absConfig
	rctx.EnvPath = absEnv

	// Build the stage list.
	allStages := buildStages()

	if flagHeadless {
		return runHeadless(rc, rctx, allStages, logger, logPath, flagStage)
	}
	return runTUI(rc, rctx, allStages, flagStage, logPath, flagWizard)
}

// buildStages returns all bootstrap stages in dependency order.
func buildStages() []stage.Stage {
	return []stage.Stage{
		&stages.ConfigStage{},
		&stages.StorageSetupStage{},
		&stages.DNSStage{},
		&stages.TLSStage{},
		&stages.VaultStage{},
		&stages.PostgreSQLStage{},
		&stages.AuthentikStage{},
		&stages.GitLabStage{},
		&stages.MAASStage{},
		&stages.K3sStage{},
		&stages.ControllerRecoveryStage{},
		&stages.LifecycleStage{},
		&stages.GitOpsStage{},
	}
}

// runHeadless runs the bootstrap without a TUI, logging to stdout.
func runHeadless(rc *config.RunConfig, rctx *runcontext.RunContext, allStages []stage.Stage, logger *zap.Logger, logPath, targetStage string) error {
	sink := logging.NewLogSink(logger, true)
	runner := stage.NewRunner(allStages, sink, logPath)

	ctx := context.Background()

	if targetStage != "" {
		if targetStage != "config-collection" {
			if err := runner.RunStage(ctx, rc.Config, rc.Secrets, rctx, "config-collection"); err != nil {
				return fmt.Errorf("targeted-run preflight failed: %w", err)
			}
		}
		return runner.RunStage(ctx, rc.Config, rc.Secrets, rctx, targetStage)
	}
	return runner.Run(ctx, rc.Config, rc.Secrets, rctx)
}

// selectInitialScreen determines the initial TUI mode without starting a TUI.
func selectInitialScreen(rc *config.RunConfig, wizardFlag bool) tui.InitialMode {
	if wizardFlag || config.Validate(rc) != nil {
		return tui.InitialModeWizard
	}
	return tui.InitialModePreamble
}

// runTUI collects or accepts configuration, then runs stages in the progress TUI.
func runTUI(rc *config.RunConfig, rctx *runcontext.RunContext, allStages []stage.Stage, targetStage, logPath string, wizardFlag bool) error {
	// Build initial stage statuses.
	statuses := make([]tui.StageStatus, len(allStages))
	for i, s := range allStages {
		statuses[i] = tui.StageStatus{
			Name:  s.Name(),
			State: "pending",
		}
	}

	resultCh := make(chan config.WizardResult, 1)
	mode := selectInitialScreen(rc, wizardFlag)
	model := tui.NewApp(mode, rc, resultCh, statuses, logPath)
	program := tea.NewProgram(model, tea.WithAltScreen())

	tuiSink := tui.NewTUISink(program)
	runErrCh := make(chan error, 1)

	// Wait for the wizard or preamble, persist new values, then start the runner.
	go func() {
		result := <-resultCh
		activeRC := rc

		if !result.Noop {
			var err error
			activeRC, err = config.FromWizardResult(result)
			if err != nil {
				program.Send(tui.FatalMsg{Err: err})
				runErrCh <- err
				return
			}

			// Preserve environment-derived test/API overrides loaded before the wizard.
			activeRC.ACMEServerURL = rc.ACMEServerURL
			activeRC.ACMECACertURL = rc.ACMECACertURL
			activeRC.ACMEServerHostIP = rc.ACMEServerHostIP
			activeRC.CloudflareAPIBaseURL = rc.CloudflareAPIBaseURL
			activeRC.TechnitiumAPIURL = rc.TechnitiumAPIURL
			activeRC.VaultAPIURL = rc.VaultAPIURL
			activeRC.AuthentikAPIURL = rc.AuthentikAPIURL
			activeRC.GitLabAPIURL = rc.GitLabAPIURL
			activeRC.GitLabBaseURL = rc.GitLabBaseURL
			activeRC.MAASAPIURL = rc.MAASAPIURL
			activeRC.APIInsecure = rc.APIInsecure

			if err := config.Validate(activeRC); err != nil {
				program.Send(tui.FatalMsg{Err: err})
				runErrCh <- err
				return
			}
			if err := config.WriteConfigYAML(rctx.ConfigPath, activeRC.Config); err != nil {
				program.Send(tui.FatalMsg{Err: err})
				runErrCh <- err
				return
			}
			if err := config.WriteEnvFile(rctx.EnvPath, activeRC.Secrets); err != nil {
				program.Send(tui.FatalMsg{Err: err})
				runErrCh <- err
				return
			}
		}

		program.Send(tui.StartStagesMsg{RC: activeRC})

		runner := stage.NewRunner(allStages, tuiSink, logPath)
		ctx := context.Background()
		if targetStage != "" {
			var err error
			if targetStage != "config-collection" {
				err = runner.RunStage(ctx, activeRC.Config, activeRC.Secrets, rctx, "config-collection")
			}
			if err == nil {
				err = runner.RunStage(ctx, activeRC.Config, activeRC.Secrets, rctx, targetStage)
			}
			runErrCh <- err
		} else {
			err := runner.Run(ctx, activeRC.Config, activeRC.Secrets, rctx)
			runErrCh <- err
		}
		tuiSink.Quit()
	}()

	// Run the TUI (blocks until quit).
	finalModel, err := program.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	_ = finalModel

	select {
	case err := <-runErrCh:
		return err
	default:
		// The user exited before completing the wizard.
		return nil
	}
}
