package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/homelab/bootstrap-home-infra/internal/stage"
)

// TUISink implements stage.Sink by sending messages to a Bubbletea program.
type TUISink struct {
	program *tea.Program
}

// NewTUISink creates a TUISink backed by the given Bubbletea program.
func NewTUISink(program *tea.Program) *TUISink {
	return &TUISink{program: program}
}

// StageStarted sends a stage state update to the TUI (state: "running").
func (s *TUISink) StageStarted(name string) {
	s.program.Send(StageUpdateMsg{Name: name, State: "running"})
}

// StageComplete sends a stage state update to the TUI (state: "done").
func (s *TUISink) StageComplete(name string) {
	s.program.Send(StageUpdateMsg{Name: name, State: "done"})
}

// StageFailed sends a stage state update to the TUI (state: "failed").
func (s *TUISink) StageFailed(name, errMsg, logPath string, logLine int) {
	s.program.Send(StageUpdateMsg{Name: name, State: "failed"})
	s.program.Send(LogMsg{Level: "error", Stage: name, Text: errMsg})
}

// Log sends a leveled log message to the TUI.
func (s *TUISink) Log(level, stageName, msg string, fields map[string]string) {
	s.program.Send(LogMsg{Level: level, Stage: stageName, Text: msg})
}

// Prompt pauses the runner, updates the model to prompt mode, and waits for input.
// If masked is true, input is hidden (password mode).
func (s *TUISink) Prompt(label string, masked bool) (string, error) {
	responseChan := make(chan string, 1)
	errChan := make(chan error, 1)

	s.program.Send(PromptMsg{
		Label:    label,
		Masked:   masked,
		Response: responseChan,
		ErrChan:  errChan,
	})

	select {
	case response := <-responseChan:
		return response, nil
	case err := <-errChan:
		return "", err
	}
}

// Confirm pauses the runner, updates the model to confirm mode, and waits for a yes/no answer.
func (s *TUISink) Confirm(msg string) (bool, error) {
	responseChan := make(chan bool, 1)
	errChan := make(chan error, 1)

	s.program.Send(ConfirmMsg{
		Message:  msg,
		Response: responseChan,
		ErrChan:  errChan,
	})

	select {
	case response := <-responseChan:
		return response, nil
	case err := <-errChan:
		return false, err
	}
}

// ShowWarningBox displays a warning box with the given lines.
func (s *TUISink) ShowWarningBox(lines []string) {
	s.program.Send(WarningBoxMsg{Lines: lines})
}

// Quit signals the TUI to quit.
func (s *TUISink) Quit() {
	s.program.Send(tea.Quit())
}

// Ensure TUISink implements stage.Sink.
var _ stage.Sink = (*TUISink)(nil)
