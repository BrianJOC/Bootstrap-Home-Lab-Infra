package runcontext

import (
	"time"

	"github.com/homelab/bootstrap-home-infra/internal/config"
)

// RunContext holds per-run in-memory state that is not persisted to disk.
// It is created once per bootstrap invocation and passed by pointer through
// the runner into each stage.
type RunContext struct {
	// BootstrapToken is the Vault root token generated during Stage 3.
	// Never persisted to disk. Held in memory for the lifetime of the run.
	BootstrapToken string

	// TargetArch is detected during Stage 0 via `uname -m`.
	TargetArch config.TargetArch

	// StartTime is the time the bootstrap run began.
	StartTime time.Time

	// RunDir is the absolute log/run directory path.
	RunDir string

	// ConfigPath is the absolute path to config.yaml (from --config flag).
	// Stage 0 writes the resolved config here, so it lands exactly where the
	// operator pointed the flag — not at a relative CWD path.
	ConfigPath string

	// EnvPath is the absolute path to .env (from --env flag).
	// Stage 0 writes secrets here; see ConfigPath rationale above.
	EnvPath string

	// KnownHostsPath is the bootstrap-owned strict SSH trust store shared by Go
	// and Ansible.
	KnownHostsPath string

	// Host-key policy flags are runtime intent and are never persisted in
	// structural configuration.
	Headless         bool
	AcceptNewHostKey bool
	ReplaceHostKey   bool
	ApproveTerraform bool

	// StagesDone tracks which stages completed successfully in this run.
	StagesDone map[string]bool
}

// New creates an initialized RunContext.
func New(runDir string) *RunContext {
	return &RunContext{
		StartTime:  time.Now().UTC(),
		RunDir:     runDir,
		StagesDone: make(map[string]bool),
	}
}

// MarkDone records that a stage completed successfully.
func (rc *RunContext) MarkDone(stage string) {
	rc.StagesDone[stage] = true
}

// IsDone reports whether a stage has been marked complete in this run.
func (rc *RunContext) IsDone(stage string) bool {
	return rc.StagesDone[stage]
}
