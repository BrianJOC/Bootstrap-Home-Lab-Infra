package stages

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureVaultInitParentCreatesPrivateDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault", "vault-init.json")
	if err := ensureVaultInitParent(path); err != nil {
		t.Fatalf("ensureVaultInitParent() error = %v", err)
	}
	info, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat parent directory: %v", err)
	}
	if got := info.Mode().Perm(); got != 0700 {
		t.Errorf("parent mode = %o, want 700", got)
	}
}

func TestEnsureVaultInitParentRejectsRelativePath(t *testing.T) {
	if err := ensureVaultInitParent("vault-init.json"); err == nil {
		t.Fatal("expected error for relative path")
	}
}
