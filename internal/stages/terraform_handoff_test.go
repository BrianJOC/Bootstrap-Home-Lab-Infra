package stages

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	terraformrunner "github.com/homelab/bootstrap-home-infra/internal/terraform"
)

func TestTerraformHandoffStatusEnforcesRollbackSnapshot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	complete, err := terraformHandoffStatus("bootstrap-dns")
	if err != nil || complete {
		t.Fatalf("pre-handoff status = %t, %v", complete, err)
	}
	statePath, err := terraformrunner.StatePath("bootstrap-dns")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Dir(statePath), 0700); err != nil {
		t.Fatal(err)
	}
	handoffPath := strings.TrimSuffix(statePath, ".tfstate") + ".handoff.json"
	if err = os.WriteFile(handoffPath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = terraformHandoffStatus("bootstrap-dns"); err == nil || !strings.Contains(err.Error(), "rollback state is missing") {
		t.Fatalf("missing rollback state was not rejected: %v", err)
	}
	if err = os.WriteFile(statePath, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	complete, err = terraformHandoffStatus("bootstrap-dns")
	if err != nil || !complete {
		t.Fatalf("completed handoff status = %t, %v", complete, err)
	}
}
