package stage

import (
	"context"
	"errors"
	"testing"

	"github.com/homelab/bootstrap-home-infra/internal/config"
	"github.com/homelab/bootstrap-home-infra/internal/runcontext"
)

// ── Test doubles ──────────────────────────────────────────────────────────────

type mockStage struct {
	name    string
	checkFn func(context.Context, *config.Config, *runcontext.RunContext) (bool, error)
	runFn   func(context.Context, *config.Config, *config.Secrets, *runcontext.RunContext, Sink) error
}

func (m *mockStage) Name() string { return m.name }

func (m *mockStage) Check(ctx context.Context, cfg *config.Config, rc *runcontext.RunContext) (bool, error) {
	return m.checkFn(ctx, cfg, rc)
}

func (m *mockStage) Run(ctx context.Context, cfg *config.Config, sec *config.Secrets, rc *runcontext.RunContext, sink Sink) error {
	return m.runFn(ctx, cfg, sec, rc, sink)
}

type mockSink struct {
	started   []string
	completed []string
	failed    []string
}

func (m *mockSink) StageStarted(name string)  { m.started = append(m.started, name) }
func (m *mockSink) StageComplete(name string) { m.completed = append(m.completed, name) }
func (m *mockSink) StageFailed(name, errMsg, logPath string, logLine int) {
	m.failed = append(m.failed, name)
}
func (m *mockSink) Log(level, stage, msg string, fields map[string]string) {}
func (m *mockSink) Prompt(label string, masked bool) (string, error)       { return "", nil }
func (m *mockSink) Confirm(msg string) (bool, error)                       { return true, nil }
func (m *mockSink) ShowWarningBox(lines []string)                          {}

// neverRun returns a runFn that fails the test if called.
func neverRun(t *testing.T) func(context.Context, *config.Config, *config.Secrets, *runcontext.RunContext, Sink) error {
	t.Helper()
	return func(context.Context, *config.Config, *config.Secrets, *runcontext.RunContext, Sink) error {
		t.Error("Run() was called but should have been skipped")
		return nil
	}
}

// alwaysDone returns a checkFn that reports the stage is already done.
func alwaysDone(_ context.Context, _ *config.Config, _ *runcontext.RunContext) (bool, error) {
	return true, nil
}

// neverDone returns a checkFn that reports the stage is not yet done.
func neverDone(_ context.Context, _ *config.Config, _ *runcontext.RunContext) (bool, error) {
	return false, nil
}

func newTestRunner(stages []Stage, sink *mockSink) *Runner {
	return NewRunner(stages, sink, "/tmp/test.log")
}

func emptyConfig() *config.Config          { return &config.Config{} }
func emptySecrets() *config.Secrets        { return &config.Secrets{} }
func emptyContext() *runcontext.RunContext { return runcontext.New("/tmp") }

// ── Runner.Run ────────────────────────────────────────────────────────────────

func TestRun_SkipsAlreadyCompleteStage(t *testing.T) {
	sink := &mockSink{}
	s := &mockStage{
		name:    "dns",
		checkFn: alwaysDone,
		runFn:   neverRun(t),
	}
	r := newTestRunner([]Stage{s}, sink)
	if err := r.Run(context.Background(), emptyConfig(), emptySecrets(), emptyContext()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sink.started) != 0 {
		t.Errorf("StageStarted called %d times; want 0", len(sink.started))
	}
	if len(sink.completed) != 1 || sink.completed[0] != "dns" {
		t.Errorf("StageComplete: got %v, want [dns]", sink.completed)
	}
}

func TestRun_ExecutesPendingStage(t *testing.T) {
	ran := false
	sink := &mockSink{}
	s := &mockStage{
		name:    "tls",
		checkFn: neverDone,
		runFn: func(_ context.Context, _ *config.Config, _ *config.Secrets, _ *runcontext.RunContext, _ Sink) error {
			ran = true
			return nil
		},
	}
	r := newTestRunner([]Stage{s}, sink)
	if err := r.Run(context.Background(), emptyConfig(), emptySecrets(), emptyContext()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ran {
		t.Error("Run() was not called for pending stage")
	}
	if len(sink.started) != 1 || sink.started[0] != "tls" {
		t.Errorf("StageStarted: got %v, want [tls]", sink.started)
	}
	if len(sink.completed) != 1 || sink.completed[0] != "tls" {
		t.Errorf("StageComplete: got %v, want [tls]", sink.completed)
	}
}

func TestRun_MarksDoneAfterSuccess(t *testing.T) {
	sink := &mockSink{}
	s := &mockStage{
		name:    "vault",
		checkFn: neverDone,
		runFn: func(_ context.Context, _ *config.Config, _ *config.Secrets, _ *runcontext.RunContext, _ Sink) error {
			return nil
		},
	}
	rc := emptyContext()
	r := newTestRunner([]Stage{s}, sink)
	if err := r.Run(context.Background(), emptyConfig(), emptySecrets(), rc); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rc.IsDone("vault") {
		t.Error("stage not marked done after successful Run")
	}
}

func TestRun_ReturnsErrorOnCheckFailure(t *testing.T) {
	checkErr := errors.New("check failed")
	sink := &mockSink{}
	s := &mockStage{
		name: "dns",
		checkFn: func(_ context.Context, _ *config.Config, _ *runcontext.RunContext) (bool, error) {
			return false, checkErr
		},
		runFn: neverRun(t),
	}
	r := newTestRunner([]Stage{s}, sink)
	err := r.Run(context.Background(), emptyConfig(), emptySecrets(), emptyContext())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, checkErr) {
		t.Errorf("error %v does not wrap %v", err, checkErr)
	}
	if len(sink.failed) != 1 || sink.failed[0] != "dns" {
		t.Errorf("StageFailed: got %v, want [dns]", sink.failed)
	}
}

func TestRun_ReturnsErrorOnRunFailure(t *testing.T) {
	runErr := errors.New("run failed")
	sink := &mockSink{}
	s := &mockStage{
		name:    "tls",
		checkFn: neverDone,
		runFn: func(_ context.Context, _ *config.Config, _ *config.Secrets, _ *runcontext.RunContext, _ Sink) error {
			return runErr
		},
	}
	r := newTestRunner([]Stage{s}, sink)
	err := r.Run(context.Background(), emptyConfig(), emptySecrets(), emptyContext())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, runErr) {
		t.Errorf("error %v does not wrap %v", err, runErr)
	}
	if len(sink.failed) != 1 || sink.failed[0] != "tls" {
		t.Errorf("StageFailed: got %v, want [tls]", sink.failed)
	}
}

func TestRun_StopsOnFirstFailure(t *testing.T) {
	secondRan := false
	sink := &mockSink{}
	stages := []Stage{
		&mockStage{
			name:    "dns",
			checkFn: neverDone,
			runFn: func(_ context.Context, _ *config.Config, _ *config.Secrets, _ *runcontext.RunContext, _ Sink) error {
				return errors.New("dns failed")
			},
		},
		&mockStage{
			name:    "tls",
			checkFn: neverDone,
			runFn: func(_ context.Context, _ *config.Config, _ *config.Secrets, _ *runcontext.RunContext, _ Sink) error {
				secondRan = true
				return nil
			},
		},
	}
	r := newTestRunner(stages, sink)
	_ = r.Run(context.Background(), emptyConfig(), emptySecrets(), emptyContext())
	if secondRan {
		t.Error("second stage ran after first stage failed")
	}
}

func TestRun_HonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	ran := false
	sink := &mockSink{}
	s := &mockStage{
		name:    "dns",
		checkFn: neverDone,
		runFn: func(_ context.Context, _ *config.Config, _ *config.Secrets, _ *runcontext.RunContext, _ Sink) error {
			ran = true
			return nil
		},
	}
	r := newTestRunner([]Stage{s}, sink)
	err := r.Run(ctx, emptyConfig(), emptySecrets(), emptyContext())
	if err == nil {
		t.Fatal("expected context cancellation error, got nil")
	}
	if ran {
		t.Error("stage ran despite cancelled context")
	}
}

// ── Runner.RunStage ───────────────────────────────────────────────────────────

func TestRunStage_NotFound(t *testing.T) {
	sink := &mockSink{}
	r := newTestRunner([]Stage{}, sink)
	err := r.RunStage(context.Background(), emptyConfig(), emptySecrets(), emptyContext(), "dns")
	if err == nil {
		t.Fatal("expected error for unknown stage, got nil")
	}
	if !errors.Is(err, err) || err.Error() == "" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunStage_SkipsAlreadyCompleteStage(t *testing.T) {
	sink := &mockSink{}
	s := &mockStage{
		name:    "dns",
		checkFn: alwaysDone,
		runFn:   neverRun(t),
	}
	r := newTestRunner([]Stage{s}, sink)
	if err := r.RunStage(context.Background(), emptyConfig(), emptySecrets(), emptyContext(), "dns"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sink.started) != 0 {
		t.Errorf("StageStarted called %d times; want 0", len(sink.started))
	}
}

func TestRunStage_ExecutesNamedStageOnly(t *testing.T) {
	firstRan := false
	secondRan := false
	sink := &mockSink{}
	stages := []Stage{
		&mockStage{
			name:    "dns",
			checkFn: neverDone,
			runFn: func(_ context.Context, _ *config.Config, _ *config.Secrets, _ *runcontext.RunContext, _ Sink) error {
				firstRan = true
				return nil
			},
		},
		&mockStage{
			name:    "tls",
			checkFn: neverDone,
			runFn: func(_ context.Context, _ *config.Config, _ *config.Secrets, _ *runcontext.RunContext, _ Sink) error {
				secondRan = true
				return nil
			},
		},
	}
	r := newTestRunner(stages, sink)
	if err := r.RunStage(context.Background(), emptyConfig(), emptySecrets(), emptyContext(), "tls"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if firstRan {
		t.Error("\"dns\" stage ran; only \"tls\" should have run")
	}
	if !secondRan {
		t.Error("\"tls\" stage did not run")
	}
}

func TestRunStage_MarksDoneAfterSuccess(t *testing.T) {
	sink := &mockSink{}
	s := &mockStage{
		name:    "vault",
		checkFn: neverDone,
		runFn: func(_ context.Context, _ *config.Config, _ *config.Secrets, _ *runcontext.RunContext, _ Sink) error {
			return nil
		},
	}
	rc := emptyContext()
	r := newTestRunner([]Stage{s}, sink)
	if err := r.RunStage(context.Background(), emptyConfig(), emptySecrets(), rc, "vault"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rc.IsDone("vault") {
		t.Error("stage not marked done after RunStage")
	}
}
