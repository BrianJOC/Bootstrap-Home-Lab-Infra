package stage

import (
	"context"
	"fmt"

	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
)

// Stage is implemented by every bootstrap stage.
type Stage interface {
	// Name returns the canonical lowercase hyphenated stage identifier.
	Name() string

	// Check inspects the target system and returns true if this stage's
	// postconditions are already satisfied. Must not modify the target.
	Check(ctx context.Context, cfg *config.Config, rc *runcontext.RunContext) (bool, error)

	// Run executes the stage. Must be idempotent.
	// Run must never write secret values to any log or display surface.
	Run(ctx context.Context, cfg *config.Config, sec *config.Secrets, rc *runcontext.RunContext, sink Sink) error
}

// Sink is the communication interface between stage runners and the operator.
// Implementations: TUISink (Bubbletea model), LogSink (stdout/file).
type Sink interface {
	StageStarted(name string)
	StageComplete(name string)
	StageFailed(name, errMsg, logPath string, logLine int)
	Log(level, stage, msg string, fields map[string]string)
	Prompt(label string, masked bool) (string, error)
	Confirm(msg string) (bool, error)
	ShowWarningBox(lines []string)
}

// Runner orchestrates stage execution.
type Runner struct {
	stages  []Stage
	sink    Sink
	logPath string // current log file path, passed to StageFailed
}

// NewRunner creates a new Runner with the given stages and sink.
func NewRunner(stages []Stage, sink Sink, logPath string) *Runner {
	return &Runner{stages: stages, sink: sink, logPath: logPath}
}

// Run iterates stages: calls Check first, skips if already complete, calls Run otherwise.
func (r *Runner) Run(ctx context.Context, cfg *config.Config, sec *config.Secrets, rc *runcontext.RunContext) error {
	for _, s := range r.stages {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		r.sink.Log("info", s.Name(), "checking stage prerequisites", nil)
		done, err := s.Check(ctx, cfg, rc)
		if err != nil {
			r.sink.StageFailed(s.Name(), err.Error(), r.logPath, 0)
			return fmt.Errorf("stage %q check failed: %w", s.Name(), err)
		}
		if done {
			r.sink.Log("info", s.Name(), "already complete; skipping", nil)
			r.sink.StageComplete(s.Name())
			continue
		}

		r.sink.StageStarted(s.Name())
		if err := s.Run(ctx, cfg, sec, rc, r.sink); err != nil {
			r.sink.StageFailed(s.Name(), err.Error(), r.logPath, 0)
			return fmt.Errorf("stage %q failed: %w", s.Name(), err)
		}
		r.sink.StageComplete(s.Name())
		rc.MarkDone(s.Name())
	}
	return nil
}

// RunStage runs only the named stage, calling Check first for idempotency (B6 fix).
func (r *Runner) RunStage(ctx context.Context, cfg *config.Config, sec *config.Secrets, rc *runcontext.RunContext, name string) error {
	for _, s := range r.stages {
		if s.Name() == name {
			r.sink.Log("info", s.Name(), "checking prerequisites (targeted run)", nil)
			done, err := s.Check(ctx, cfg, rc)
			if err != nil {
				r.sink.StageFailed(s.Name(), err.Error(), r.logPath, 0)
				return fmt.Errorf("stage %q check failed: %w", s.Name(), err)
			}
			if done {
				r.sink.Log("info", s.Name(), "already complete; skipping", nil)
				r.sink.StageComplete(s.Name())
				return nil
			}

			r.sink.StageStarted(s.Name())
			if err := s.Run(ctx, cfg, sec, rc, r.sink); err != nil {
				r.sink.StageFailed(s.Name(), err.Error(), r.logPath, 0)
				return fmt.Errorf("stage %q failed: %w", s.Name(), err)
			}
			r.sink.StageComplete(s.Name())
			rc.MarkDone(s.Name())
			return nil
		}
	}
	return fmt.Errorf("stage %q not found", name)
}
