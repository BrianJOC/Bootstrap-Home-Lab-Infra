package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/logging"
	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
	"github.com/homelab/bootstrap-home-infra/internal/stage"
	"github.com/homelab/bootstrap-home-infra/internal/stages"
	"github.com/homelab/bootstrap-home-infra/internal/tui"
)

var (
	flagTarget   string
	flagStage    string
	flagHeadless bool
	flagWizard   bool
	flagConfig   string
	flagEnv      string
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
	rootCmd.Flags().StringVar(&flagConfig, "config", "./config.yaml", "Path to config.yaml")
	rootCmd.Flags().StringVar(&flagEnv, "env", "./.env", "Path to .env file")
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

// selectInitialScreen determines the initial TUI mode without starting the TUI.
func selectInitialScreen(rc *config.RunConfig, wizardFlag bool) tui.InitialMode {
	if wizardFlag {
		return tui.InitialModeWizard
	}
	if config.Validate(rc) != nil {
		return tui.InitialModeWizard
	}
	return tui.InitialModePreamble
}

// buildStages returns all bootstrap stages in dependency order.
func buildStages() []stage.Stage {
	return []stage.Stage{
		&stages.ConfigStage{},
		&stages.StorageSetupStage{},
		&stages.DNSStage{},
		&stages.TLSStage{},
		&stages.VaultStage{},
		&stages.AuthentikStage{},
		&stages.PostgreSQLStage{},
		&stages.GitLabStage{},
		&stages.MAASStage{},
		&stages.K3sStage{},
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

// runTUI runs the bootstrap with the Bubbletea TUI.
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
	appModel := tui.NewApp(mode, rc, resultCh, statuses, logPath)
	program := tea.NewProgram(appModel, tea.WithAltScreen())
	tuiSink := tui.NewTUISink(program)
	runnerErrCh := make(chan error, 1)

	// Wait for wizard completion, persist any new configuration, then launch
	// the stage runner while the TUI displays progress.
	go func() {
		result := <-resultCh
		activeRC := rc

		if !result.Noop {
			var err error
			activeRC, err = config.FromWizardResult(result)
			if err != nil {
				program.Send(tui.FatalMsg{Err: err})
				runnerErrCh <- err
				return
			}
			if err := config.WriteConfigYAML(rctx.ConfigPath, activeRC.Config); err != nil {
				program.Send(tui.FatalMsg{Err: err})
				runnerErrCh <- err
				return
			}
			if err := config.WriteEnvFile(rctx.EnvPath, activeRC.Secrets); err != nil {
				program.Send(tui.FatalMsg{Err: err})
				runnerErrCh <- err
				return
			}
		}

		program.Send(tui.StartStagesMsg{RC: activeRC})

		go func() {
			ctx := context.Background()
			runner := stage.NewRunner(allStages, tuiSink, logPath)
			var err error
			if targetStage != "" {
				err = runner.RunStage(ctx, activeRC.Config, activeRC.Secrets, rctx, targetStage)
			} else {
				err = runner.Run(ctx, activeRC.Config, activeRC.Secrets, rctx)
			}
			runnerErrCh <- err
			tuiSink.Quit()
		}()
	}()

	if _, err := program.Run(); err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}

	select {
	case err := <-runnerErrCh:
		return err
	default:
		return nil
	}
}
