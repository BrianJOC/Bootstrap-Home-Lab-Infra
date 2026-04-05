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
	return runTUI(rc, rctx, allStages, flagStage, logPath)
}

// buildStages returns all bootstrap stages in dependency order.
func buildStages() []stage.Stage {
	return []stage.Stage{
		&stages.ConfigStage{},
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
func runTUI(rc *config.RunConfig, rctx *runcontext.RunContext, allStages []stage.Stage, targetStage, logPath string) error {
	// Build initial stage statuses.
	statuses := make([]tui.StageStatus, len(allStages))
	for i, s := range allStages {
		statuses[i] = tui.StageStatus{
			Name:  s.Name(),
			State: "pending",
		}
	}

	model := tui.New(statuses, logPath)
	program := tea.NewProgram(model, tea.WithAltScreen())

	tuiSink := tui.NewTUISink(program)
	runner := stage.NewRunner(allStages, tuiSink, logPath)

	// Run the stages in a goroutine so the TUI can update.
	runErrChan := make(chan error, 1)
	go func() {
		ctx := context.Background()
		var err error
		if targetStage != "" {
			err = runner.RunStage(ctx, rc.Config, rc.Secrets, rctx, targetStage)
		} else {
			err = runner.Run(ctx, rc.Config, rc.Secrets, rctx)
		}
		runErrChan <- err
		tuiSink.Quit()
	}()

	// Run the TUI (blocks until quit).
	finalModel, err := program.Run()
	if err != nil {
		return fmt.Errorf("TUI error: %w", err)
	}
	_ = finalModel

	return <-runErrChan
}
