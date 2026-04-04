// internal/tui/wizard.go
// Stub — full implementation in Task 5.
package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/homelab/bootstrap-home-infra/internal/config"
)

type wizardModel struct{}

func newWizardModel(_ InitialMode, _ *config.RunConfig, _ chan config.WizardResult) *wizardModel {
	return &wizardModel{}
}

func (w *wizardModel) init() tea.Cmd                      { return nil }
func (w *wizardModel) update(_ tea.Msg) (screen, tea.Cmd) { return w, nil }
func (w *wizardModel) view() string                        { return "wizard stub" }
func (w *wizardModel) setSize(_, _ int)                    {}
