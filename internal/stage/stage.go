package stage

import (
	"context"
	"fmt"

	"github.com/homelab/bootstrap-home-infra/internal/config"
)

// Stage is implemented by every bootstrap stage.
type Stage interface {
	Name() string
	Check(ctx context.Context, cfg *config.RunConfig) (bool, error)
	Run(ctx context.Context, cfg *config.RunConfig, sink Sink) error
}

// Sink is the communication interface between stage runners and the operator.
type Sink interface {
	Info(stage, msg string)
	Warn(stage, msg string)
	Error(stage, msg string, err error)
	Prompt(stage, question string) (string, error)
	Verify(stage, message string) (bool, error)
}

// Runner orchestrates stage execution.
type Runner struct {
	stages []Stage
	sink   Sink
}

// NewRunner creates a new Runner with the given stages and sink.
func NewRunner(stages []Stage, sink Sink) *Runner {
	return &Runner{stages: stages, sink: sink}
}

// Run iterates stages: calls Check first, skips if true, calls Run otherwise.
func (r *Runner) Run(ctx context.Context, cfg *config.RunConfig) error {
	for _, s := range r.stages {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		r.sink.Info(s.Name(), "checking stage prerequisites")
		done, err := s.Check(ctx, cfg)
		if err != nil {
			r.sink.Error(s.Name(), "check failed", err)
			return fmt.Errorf("stage %q check failed: %w", s.Name(), err)
		}
		if done {
			r.sink.Info(s.Name(), "already complete; skipping")
			continue
		}

		r.sink.Info(s.Name(), "starting")
		if err := s.Run(ctx, cfg, r.sink); err != nil {
			r.sink.Error(s.Name(), "stage failed", err)
			return fmt.Errorf("stage %q failed: %w", s.Name(), err)
		}
		r.sink.Info(s.Name(), "complete")
	}
	return nil
}

// RunStage runs only the named stage (by name), still loading config first.
func (r *Runner) RunStage(ctx context.Context, cfg *config.RunConfig, name string) error {
	for _, s := range r.stages {
		if s.Name() == name {
			r.sink.Info(s.Name(), "starting (targeted run)")
			if err := s.Run(ctx, cfg, r.sink); err != nil {
				r.sink.Error(s.Name(), "stage failed", err)
				return fmt.Errorf("stage %q failed: %w", s.Name(), err)
			}
			r.sink.Info(s.Name(), "complete")
			return nil
		}
	}
	return fmt.Errorf("stage %q not found", name)
}
