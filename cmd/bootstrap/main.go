package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/term"

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
	flagTarget           string
	flagStage            string
	flagHeadless         bool
	flagWizard           bool
	flagAcceptNewHostKey bool
	flagReplaceHostKey   bool
	flagApproveTerraform bool
	flagConfig           string
	flagEnv              string
	flagRecoveryBundle   string
	flagRecoveryOutput   string
	flagVaultConfig      string
	flagVaultEnv         string
	flagOperationConfig  string
	flagOperationEnv     string
	flagBackupSnapshot   string
	flagBackupTarget     string
	flagAdoptionTarget   string
	flagAdoptionOutput   string
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
	adoptServerCmd.Flags().StringVar(&flagAdoptionOutput, "output", "", "Optional path for the read-only JSON adoption draft")
	rootCmd.AddCommand(adoptServerCmd)
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
sudo -n sh -c '. /etc/bootstrap-home-infra/backup/restic-primary.env; restic snapshots --latest 1'`)
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
		stdout, stderr, err := client.Run(`sudo -n systemctl start bootstrap-backup.service && sudo -n systemctl status --no-pager bootstrap-backup.service`)
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
sudo -n sh -c '. /etc/bootstrap-home-infra/backup/restic-primary.env; restic restore "$1" --target "$2"' sh "$SNAPSHOT" "$TARGET"
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
		stdout, stderr, err := client.Run(`set -Eeuo pipefail
printf 'hostname=%s\n' "$(hostname -f)"
printf 'architecture=%s\n' "$(uname -m)"
. /etc/os-release
printf 'operating_system=%s %s\n' "$ID" "$VERSION_ID"
printf 'machine_id=%s\n' "$(sha256sum /etc/machine-id | awk '{print $1}')"
printf 'cpu_count=%s\n' "$(getconf _NPROCESSORS_ONLN)"
printf 'memory_bytes=%s\n' "$(awk '/MemTotal/{print $2 * 1024}' /proc/meminfo)"
printf 'root_bytes=%s\n' "$(df -B1 --output=size / | tail -1 | tr -d ' ')"
printf 'interfaces=%s\n' "$(ip -brief link | awk '{print $1}' | paste -sd, -)"
`)
		if err != nil {
			return fmt.Errorf("auditing adoption target: %w: %s", err, strings.TrimSpace(stderr))
		}
		fields := map[string]string{
			"target":                   runConfig.TargetHost,
			"expected_ssh_fingerprint": runConfig.SSHHostFingerprint,
			"review_state":             "draft-read-only",
		}
		for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
			key, value, found := strings.Cut(line, "=")
			if found {
				fields[key] = value
			}
		}
		document, err := json.MarshalIndent(fields, "", "  ")
		if err != nil {
			return err
		}
		document = append(document, '\n')
		if flagAdoptionOutput == "" {
			fmt.Print(string(document))
			return nil
		}
		if err := os.WriteFile(flagAdoptionOutput+".tmp", document, 0600); err != nil {
			return err
		}
		if err := os.Rename(flagAdoptionOutput+".tmp", flagAdoptionOutput); err != nil {
			return err
		}
		fmt.Printf("Read-only adoption draft written to %s; commit it through the GitLab review workflow to adopt.\n", flagAdoptionOutput)
		return nil
	},
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
		initBytes, err := os.ReadFile(runConfig.VaultInitOutputPath)
		if err != nil {
			return fmt.Errorf("reading Vault recovery JSON: %w", err)
		}
		var initMaterial struct {
			UnsealKeys []string `json:"unseal_keys_b64"`
			RootToken  string   `json:"root_token"`
		}
		if err := json.Unmarshal(initBytes, &initMaterial); err != nil {
			return fmt.Errorf("decoding Vault recovery JSON: %w", err)
		}
		if len(initMaterial.UnsealKeys) < runConfig.VaultThreshold {
			return fmt.Errorf("Vault recovery JSON has %d shares; threshold is %d", len(initMaterial.UnsealKeys), runConfig.VaultThreshold)
		}
		for index := 0; index < runConfig.VaultThreshold; index++ {
			_, stderr, unsealErr := client.RunWithStdin(
				"VAULT_ADDR=https://127.0.0.1:8200 VAULT_SKIP_VERIFY=true vault operator unseal",
				[]byte(initMaterial.UnsealKeys[index]+"\n"),
			)
			if unsealErr != nil {
				return fmt.Errorf("submitting Vault unseal share %d: %w: %s", index+1, unsealErr, strings.TrimSpace(stderr))
			}
			fmt.Printf("Accepted unseal share %d of %d\n", index+1, runConfig.VaultThreshold)
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
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		oidcSecret, err := os.ReadFile(filepath.Join(homeDir, ".bootstrap", "oidc-vault-secret"))
		if err != nil {
			return fmt.Errorf("reading Vault OIDC client secret: %w", err)
		}
		script := fmt.Sprintf(`set -Eeuo pipefail
IFS= read -r VAULT_TOKEN
IFS= read -r OIDC_SECRET
export VAULT_TOKEN VAULT_ADDR=https://127.0.0.1:8200 VAULT_SKIP_VERIFY=true
vault status >/dev/null
vault secrets list -format=json | grep -q '"secret/"' || vault secrets enable -path=secret kv-v2
vault kv put secret/bootstrap/tls/wildcard \
  cert=@/opt/certs/certificates/_.%[1]s.crt \
  key=@/opt/certs/certificates/_.%[1]s.key \
  domain=%[1]s >/dev/null
cat >/tmp/bootstrap-backup-policy.hcl <<'POLICY'
path "sys/storage/raft/snapshot" {
  capabilities = ["read"]
}
POLICY
vault policy write bootstrap-backup /tmp/bootstrap-backup-policy.hcl >/dev/null
vault token create -policy=bootstrap-backup -period=24h -orphan -format=json |
  jq -r .auth.client_token |
  install -m 0600 /dev/stdin /etc/bootstrap-home-infra/vault-backup-token
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
  user_claim=sub \
  policies=default >/dev/null
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
  user_claim=sub \
  token_policies=gitlab-ci \
  token_ttl=1h >/dev/null
rm -f /tmp/bootstrap-backup-policy.hcl
rm -f /tmp/bootstrap-gitlab-ci-policy.hcl /tmp/bootstrap-vault-oidc-secret
rm -f /var/lib/bootstrap-home-infra/certificate-vault-sync-pending
`, runConfig.InternalDomain)
		reconcileInput := initMaterial.RootToken + "\n" + strings.TrimSpace(string(oidcSecret)) + "\n"
		_, stderr, err := client.RunWithStdin(script, []byte(reconcileInput))
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
	}
}

// runHeadless runs the bootstrap without a TUI, logging to stdout.
func runHeadless(rc *config.RunConfig, rctx *runcontext.RunContext, allStages []stage.Stage, logger *zap.Logger, logPath, targetStage string) error {
	sink := logging.NewLogSink(logger, true)
	runner := stage.NewRunner(allStages, sink, logPath)

	ctx := context.Background()

	if targetStage != "" {
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
			activeRC.CloudflareAPIBaseURL = rc.CloudflareAPIBaseURL

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
			err := runner.RunStage(ctx, activeRC.Config, activeRC.Secrets, rctx, targetStage)
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
