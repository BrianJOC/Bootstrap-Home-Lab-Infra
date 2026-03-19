package main

import (
	"context"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/logging"
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

	// In headless mode, validate config strictly — all fields must be present.
	if flagHeadless && flagStage != "config-collection" {
		if err := config.Validate(rc); err != nil {
			return fmt.Errorf("config validation failed (headless mode requires complete config): %w", err)
		}
	}

	// Set up logging.
	runDir := "run-" + time.Now().UTC().Format("20060102T150405")
	logger, err := logging.NewLogger(runDir)
	if err != nil {
		return fmt.Errorf("initializing logger: %w", err)
	}
	defer logger.Sync() //nolint:errcheck

	logger.Info("bootstrap started",
		zap.String("target", rc.TargetHost),
		zap.Int("pid", os.Getpid()),
	)

	// Build the stage list.
	allStages := buildStages()

	// Choose the appropriate mode.
	if flagHeadless {
		return runHeadless(rc, allStages, logger, flagStage)
	}
	return runTUI(rc, allStages, flagStage, runDir)
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
func runHeadless(rc *config.RunConfig, allStages []stage.Stage, logger *zap.Logger, targetStage string) error {
	sink := logging.NewLogSink(logger, true)
	runner := stage.NewRunner(allStages, sink)

	ctx := context.Background()

	if targetStage != "" {
		return runner.RunStage(ctx, rc, targetStage)
	}
	return runner.Run(ctx, rc)
}

// runTUI runs the bootstrap with the Bubbletea TUI.
func runTUI(rc *config.RunConfig, allStages []stage.Stage, targetStage string, runDir string) error {
	// Build initial stage statuses.
	statuses := make([]tui.StageStatus, len(allStages))
	for i, s := range allStages {
		statuses[i] = tui.StageStatus{
			Name:  s.Name(),
			State: "pending",
		}
	}

	logPath := fmt.Sprintf("logs/%s/bootstrap.log", runDir)
	model := tui.New(statuses, logPath)
	program := tea.NewProgram(model, tea.WithAltScreen())

	// Create a TUI sink.
	tuiSink := tui.NewTUISink(program)

	// Run the stages in a goroutine so the TUI can update.
	runErrChan := make(chan error, 1)
	go func() {
		ctx := context.Background()
		runner := stage.NewRunner(allStages, tuiSink)

		var err error
		if targetStage != "" {
			err = runner.RunStage(ctx, rc, targetStage)
		} else {
			err = runner.Run(ctx, rc)
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

	// Check if the runner completed successfully.
	runErr := <-runErrChan
	return runErr
}
