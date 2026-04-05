package runcontext

import (
	"testing"
	"time"
)

func TestNew_InitializesFields(t *testing.T) {
	before := time.Now().UTC()
	rc := New("/tmp/run")
	after := time.Now().UTC()

	if rc.RunDir != "/tmp/run" {
		t.Errorf("RunDir: got %q, want %q", rc.RunDir, "/tmp/run")
	}
	if rc.StagesDone == nil {
		t.Error("StagesDone is nil; want initialized map")
	}
	if rc.StartTime.Before(before) || rc.StartTime.After(after) {
		t.Errorf("StartTime %v not within expected range [%v, %v]", rc.StartTime, before, after)
	}
}

func TestMarkDone_RecordsStage(t *testing.T) {
	rc := New("/tmp/run")
	rc.MarkDone("dns")
	if !rc.StagesDone["dns"] {
		t.Error("MarkDone(\"dns\") did not set StagesDone[\"dns\"] to true")
	}
}

func TestIsDone_TrueAfterMarkDone(t *testing.T) {
	rc := New("/tmp/run")
	rc.MarkDone("tls")
	if !rc.IsDone("tls") {
		t.Error("IsDone(\"tls\") returned false after MarkDone")
	}
}

func TestIsDone_FalseForUnmarkedStage(t *testing.T) {
	rc := New("/tmp/run")
	if rc.IsDone("vault") {
		t.Error("IsDone(\"vault\") returned true for a stage that was never marked done")
	}
}

func TestMarkDone_MultipleStages(t *testing.T) {
	rc := New("/tmp/run")
	stages := []string{"dns", "tls", "vault", "authentik"}
	for _, s := range stages {
		rc.MarkDone(s)
	}
	for _, s := range stages {
		if !rc.IsDone(s) {
			t.Errorf("IsDone(%q) returned false after MarkDone", s)
		}
	}
	if rc.IsDone("postgresql") {
		t.Error("IsDone(\"postgresql\") returned true for unmarked stage")
	}
}
