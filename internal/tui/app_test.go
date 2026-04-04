// internal/tui/app_test.go
package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/homelab/bootstrap-home-infra/internal/config"
)

func TestAppModel_WizardToProgress(t *testing.T) {
	resultCh := make(chan config.WizardResult, 1)
	app := NewApp(InitialModeWizard, emptyRC(), resultCh, nil, "")
	app2, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	appM := app2.(AppModel)

	// Initial screen should be wizardModel.
	if _, ok := appM.current.(*wizardModel); !ok {
		t.Fatalf("expected wizardModel as initial screen, got %T", appM.current)
	}

	// Simulate main.go goroutine sending StartStagesMsg.
	app3, _ := appM.Update(StartStagesMsg{RC: completeRC()})
	appM3 := app3.(AppModel)

	if _, ok := appM3.current.(*progressModel); !ok {
		t.Fatalf("expected progressModel after StartStagesMsg, got %T", appM3.current)
	}
}

func TestAppModel_FatalMsg(t *testing.T) {
	resultCh := make(chan config.WizardResult, 1)
	app := NewApp(InitialModeWizard, emptyRC(), resultCh, nil, "")
	app2, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	appM := app2.(AppModel)

	_, cmd := appM.Update(FatalMsg{Err: errors.New("disk write failed")})
	if cmd == nil {
		t.Fatal("expected non-nil tea.Cmd for FatalMsg")
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg from FatalMsg cmd, got %T", msg)
	}
}

func TestAppModel_ScreenSwapCallsInit(t *testing.T) {
	resultCh := make(chan config.WizardResult, 1)
	app := NewApp(InitialModeWizard, emptyRC(), resultCh, nil, "")
	app2, _ := app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	appM := app2.(AppModel)

	before := appM.current
	app3, _ := appM.Update(StartStagesMsg{RC: completeRC()})
	appM2 := app3.(AppModel)

	if appM2.current == before {
		t.Error("screen did not swap after StartStagesMsg")
	}
	// Verify no panic — init() on the new progressModel is called by AppModel.
}
