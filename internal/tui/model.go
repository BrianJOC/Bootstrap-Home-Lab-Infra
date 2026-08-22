package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// StageStatus holds the display state of a single bootstrap stage.
type StageStatus struct {
	Name  string
	State string // "pending", "running", "done", "failed", "skipped"
}

// LogMsg is a message sent from the runner to update log lines.
type LogMsg struct {
	Level string
	Stage string
	Text  string
}

// StageUpdateMsg is a message sent to update a stage's state.
type StageUpdateMsg struct {
	Name  string
	State string
}

// PromptMsg is a message that triggers a prompt in the TUI.
type PromptMsg struct {
	Label    string
	Masked   bool
	Response chan string
	ErrChan  chan error
}

// ConfirmMsg is a message that triggers a yes/no confirmation in the TUI.
type ConfirmMsg struct {
	Message  string
	Response chan bool
	ErrChan  chan error
}

// WarningBoxMsg triggers a warning box display.
type WarningBoxMsg struct {
	Lines []string
}

// progressModel is the Bubbletea model for the bootstrap progress screen.
type progressModel struct {
	stages       []StageStatus
	logView      viewport.Model
	input        textinput.Model
	promptMsg    string
	verifyMsg    string
	warningLines []string
	mode         string // "progress", "prompt", "verify", "warning"
	logPath      string
	width        int
	height       int
	err          error
	errMsg       string
	logLines     []string

	// Channels for synchronization with the runner goroutine.
	promptResponse chan string
	promptErrChan  chan error
	verifyResponse chan bool
	verifyErrChan  chan error
}

// newProgressModel creates a new progressModel with the given stages and log path.
func newProgressModel(stages []StageStatus, logPath string) *progressModel {
	if stages == nil {
		stages = []StageStatus{}
	}
	vp := viewport.New(80, 20)
	ti := textinput.New()
	ti.CharLimit = 512

	return &progressModel{
		stages:  stages,
		logView: vp,
		input:   ti,
		mode:    "progress",
		logPath: logPath,
	}
}

// Init initializes the model.
func (m *progressModel) Init() tea.Cmd {
	return nil
}

// Update handles incoming messages and updates the model state.
func (m *progressModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.mode {
		case "prompt":
			switch msg.Type {
			case tea.KeyEnter:
				response := m.input.Value()
				m.input.Reset()
				m.mode = "progress"
				if m.promptResponse != nil {
					m.promptResponse <- response
					m.promptResponse = nil
				}
				return m, nil
			case tea.KeyCtrlC, tea.KeyEsc:
				if m.promptErrChan != nil {
					m.promptErrChan <- fmt.Errorf("prompt cancelled")
					m.promptErrChan = nil
				}
				m.mode = "progress"
				return m, nil
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)

		case "verify":
			switch msg.String() {
			case "y", "Y":
				if m.verifyResponse != nil {
					m.verifyResponse <- true
					m.verifyResponse = nil
				}
				m.mode = "progress"
			case "n", "N", "q":
				if m.verifyResponse != nil {
					m.verifyResponse <- false
					m.verifyResponse = nil
				}
				m.mode = "progress"
			case "ctrl+c":
				if m.verifyErrChan != nil {
					m.verifyErrChan <- fmt.Errorf("verify cancelled")
					m.verifyErrChan = nil
				}
				m.mode = "progress"
			}

		case "warning":
			m.mode = "progress"
			m.warningLines = nil

		default:
			switch msg.Type {
			case tea.KeyCtrlC:
				return m, tea.Quit
			}
		}

	case tea.WindowSizeMsg:
		m.setSize(msg.Width, msg.Height)

	case LogMsg:
		line := fmt.Sprintf("[%s] %s: %s", strings.ToUpper(msg.Level), msg.Stage, msg.Text)
		m.logLines = append(m.logLines, line)
		// Keep rolling window of last 1000 lines.
		if len(m.logLines) > 1000 {
			m.logLines = m.logLines[len(m.logLines)-1000:]
		}
		m.logView.SetContent(strings.Join(m.logLines, "\n"))
		m.logView.GotoBottom()

	case StageUpdateMsg:
		for i, s := range m.stages {
			if s.Name == msg.Name {
				m.stages[i].State = msg.State
				break
			}
		}

	case PromptMsg:
		m.mode = "prompt"
		m.promptMsg = msg.Label
		m.promptResponse = msg.Response
		m.promptErrChan = msg.ErrChan
		if msg.Masked {
			m.input.EchoMode = textinput.EchoPassword
		} else {
			m.input.EchoMode = textinput.EchoNormal
		}
		m.input.Focus()
		cmds = append(cmds, textinput.Blink)

	case ConfirmMsg:
		m.mode = "verify"
		m.verifyMsg = msg.Message + " (y/n)"
		m.verifyResponse = msg.Response
		m.verifyErrChan = msg.ErrChan

	case WarningBoxMsg:
		m.mode = "warning"
		m.warningLines = msg.Lines

	case error:
		m.errMsg = msg.Error()
	}

	var vpCmd tea.Cmd
	m.logView, vpCmd = m.logView.Update(msg)
	cmds = append(cmds, vpCmd)

	return m, tea.Batch(cmds...)
}

// View renders the TUI.
func (m *progressModel) View() string {
	if m.width == 0 {
		return "Initializing bootstrap..."
	}

	stageWidth := 30
	logWidth := m.width - stageWidth - 4
	if logWidth < 20 {
		logWidth = 20
	}

	// Build stage list.
	var stageRows []string
	for _, s := range m.stages {
		stageRows = append(stageRows, renderStageRow(s.Name, s.State))
	}
	stageContent := strings.Join(stageRows, "\n")
	stagePane := styleStagePane.Width(stageWidth).Render(
		styleLogLabel.Render("STAGE PROGRESS") + "\n\n" + stageContent,
	)

	// Build log pane.
	logContent := m.logView.View()
	logPane := styleLogPane.Width(logWidth).Render(
		styleLogLabel.Render("Log") + "\n" + logContent,
	)

	// Main content area.
	mainArea := lipgloss.JoinHorizontal(lipgloss.Top, stagePane, " ", logPane)

	// Bottom area: warning, prompt, verify, or error.
	var bottom string
	switch m.mode {
	case "warning":
		content := strings.Join(m.warningLines, "\n")
		bottom = styleWarningBox.Width(m.width-4).Render(content) + "\n(Press any key to continue)"
	case "prompt":
		bottom = stylePromptLabel.Render(m.promptMsg) + "\n" + m.input.View()
	case "verify":
		bottom = stylePromptLabel.Render(m.verifyMsg)
	default:
		if m.errMsg != "" {
			bottom = styleErrorPanel.Width(m.width - 4).Render("ERROR: " + m.errMsg)
		}
	}

	header := styleHeader.Width(m.width).Render("bootstrap-home-infra")

	if bottom != "" {
		return header + "\n" + mainArea + "\n" + bottom
	}
	return header + "\n" + mainArea
}

// screen interface implementation.

func (m *progressModel) init() tea.Cmd { return nil }

func (m *progressModel) update(msg tea.Msg) (screen, tea.Cmd) {
	// Discard the tea.Model return (always m); only need cmd.
	_, cmd := m.Update(msg)
	return m, cmd
}

func (m *progressModel) view() string { return m.View() }

func (m *progressModel) setSize(w, h int) {
	m.width = w
	m.height = h
	logWidth := (w * 70 / 100) - 4
	if logWidth < 20 {
		logWidth = 20
	}
	logHeight := h - 6
	if logHeight < 5 {
		logHeight = 5
	}
	m.logView.Width = logWidth
	m.logView.Height = logHeight
}
