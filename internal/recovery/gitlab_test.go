package recovery

import "testing"

func TestValidateIncidentID(t *testing.T) {
	for _, value := range []string{"inc-2026-08-26", "gitlab_loss.1", "abc"} {
		if err := ValidateIncidentID(value); err != nil {
			t.Errorf("ValidateIncidentID(%q): %v", value, err)
		}
	}
	for _, value := range []string{"", "ABCD", "a", "../escape", "has space", "line\nbreak"} {
		if err := ValidateIncidentID(value); err == nil {
			t.Errorf("ValidateIncidentID(%q) unexpectedly passed", value)
		}
	}
}

func TestValidateSnapshotID(t *testing.T) {
	for _, value := range []string{"latest", "01234567", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"} {
		if err := ValidateSnapshotID(value); err != nil {
			t.Errorf("ValidateSnapshotID(%q): %v", value, err)
		}
	}
	for _, value := range []string{"", "123", "ABCDEF12", "../latest", "0123456g"} {
		if err := ValidateSnapshotID(value); err == nil {
			t.Errorf("ValidateSnapshotID(%q) unexpectedly passed", value)
		}
	}
}
