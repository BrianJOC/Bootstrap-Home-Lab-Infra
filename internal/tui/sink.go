package tui

import (
	"fmt"

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

// Info sends an INFO log message to the TUI.
func (s *TUISink) Info(stageName, msg string) {
	s.program.Send(LogMsg{Level: "info", Stage: stageName, Text: msg})
}

// Warn sends a WARN log message to the TUI.
func (s *TUISink) Warn(stageName, msg string) {
	s.program.Send(LogMsg{Level: "warn", Stage: stageName, Text: msg})
}

// Error sends an ERROR log message to the TUI.
func (s *TUISink) Error(stageName, msg string, err error) {
	text := msg
	if err != nil {
		text = msg + ": " + err.Error()
	}
	s.program.Send(LogMsg{Level: "error", Stage: stageName, Text: text})
}

// Prompt pauses the runner, updates the model to prompt mode, and waits for input.
func (s *TUISink) Prompt(stageName, question string) (string, error) {
	responseChan := make(chan string, 1)
	errChan := make(chan error, 1)

	s.program.Send(PromptMsg{
		Stage:    stageName,
		Question: question,
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

// Verify pauses the runner, updates the model to verify mode, and waits for a yes/no answer.
func (s *TUISink) Verify(stageName, message string) (bool, error) {
	responseChan := make(chan bool, 1)
	errChan := make(chan error, 1)

	s.program.Send(VerifyMsg{
		Stage:    stageName,
		Message:  message,
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

// UpdateStage sends a stage state update to the TUI.
func (s *TUISink) UpdateStage(name, state string) {
	s.program.Send(StageUpdateMsg{Name: name, State: state})
}

// ShowWarningBox displays a warning box with the given lines.
func (s *TUISink) ShowWarningBox(lines []string) {
	s.program.Send(WarningBoxMsg{Lines: lines})
}

// Quit signals the TUI to quit.
func (s *TUISink) Quit() {
	s.program.Send(tea.Quit())
}

// FormatError returns a formatted error string for display.
func FormatError(stageName, msg string, err error) string {
	if err != nil {
		return fmt.Sprintf("[%s] %s: %v", stageName, msg, err)
	}
	return fmt.Sprintf("[%s] %s", stageName, msg)
}

// Ensure TUISink implements stage.Sink.
var _ stage.Sink = (*TUISink)(nil)
