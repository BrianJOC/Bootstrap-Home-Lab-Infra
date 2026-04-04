// internal/tui/app.go
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/homelab/bootstrap-home-infra/internal/config"
)

// screen is the internal interface for TUI screens.
type screen interface {
	init() tea.Cmd
	update(tea.Msg) (screen, tea.Cmd)
	view() string
	setSize(width, height int)
}

// InitialMode controls which screen AppModel starts with.
type InitialMode int

const (
	InitialModeWizard   InitialMode = iota // no config / --wizard flag
	InitialModePreamble                    // config present, show "use it?" prompt
)

// StartStagesMsg is sent by the main.go goroutine after wizard completion.
// AppModel swaps to progressModel on receiving it.
type StartStagesMsg struct {
	RC *config.RunConfig
}

// FatalMsg is sent when wizard post-processing fails.
// AppModel renders the error and quits.
type FatalMsg struct {
	Err error
}

// AppModel is the root tea.Model. Holds an active screen and delegates to it.
type AppModel struct {
	current  screen
	width    int
	height   int
	resultCh chan config.WizardResult
	stages   []StageStatus // stored for progressModel construction
	logPath  string        // stored for progressModel construction
	errMsg   string
}

// NewApp constructs the root AppModel.
func NewApp(mode InitialMode, rc *config.RunConfig, resultCh chan config.WizardResult, stages []StageStatus, logPath string) AppModel {
	var initial screen
	switch mode {
	case InitialModePreamble:
		initial = newWizardModel(InitialModePreamble, rc, resultCh)
	default:
		initial = newWizardModel(InitialModeWizard, rc, resultCh)
	}
	return AppModel{
		current:  initial,
		resultCh: resultCh,
		stages:   stages,
		logPath:  logPath,
	}
}

// Init satisfies tea.Model.
func (a AppModel) Init() tea.Cmd {
	return a.current.init()
}

// Update satisfies tea.Model.
func (a AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.current.setSize(msg.Width, msg.Height)

	case FatalMsg:
		a.errMsg = msg.Err.Error()
		return a, tea.Quit

	case StartStagesMsg:
		pm := newProgressModel(a.stages, a.logPath)
		pm.setSize(a.width, a.height)
		a.current = pm
		return a, pm.init()
	}

	// Delegate to current screen.
	next, cmd := a.current.update(msg)
	if next != a.current {
		// Screen swap detected: call init() on new screen and batch cmds.
		a.current = next
		next.setSize(a.width, a.height)
		initCmd := next.init()
		return a, tea.Batch(cmd, initCmd)
	}
	return a, cmd
}

// View satisfies tea.Model.
func (a AppModel) View() string {
	if a.errMsg != "" {
		return styleHeader.Width(a.width).Render("bootstrap-home-infra") + "\n" +
			styleErrorPanel.Width(a.width-4).Render("FATAL: "+a.errMsg)
	}
	return a.current.view()
}
