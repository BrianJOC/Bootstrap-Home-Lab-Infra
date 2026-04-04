// internal/tui/sshkey_test.go
package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectSSHKey_Ed25519Found(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "id_ed25519")
	if err := os.WriteFile(keyPath, []byte("fake key"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := detectSSHKey(dir); got != keyPath {
		t.Errorf("detectSSHKey = %q, want %q", got, keyPath)
	}
}

func TestDetectSSHKey_RsaFallback(t *testing.T) {
	dir := t.TempDir()
	rsaPath := filepath.Join(dir, "id_rsa")
	if err := os.WriteFile(rsaPath, []byte("fake rsa"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := detectSSHKey(dir); got != rsaPath {
		t.Errorf("detectSSHKey = %q, want %q", got, rsaPath)
	}
}

func TestDetectSSHKey_NoneFound(t *testing.T) {
	dir := t.TempDir()
	if got := detectSSHKey(dir); got != "" {
		t.Errorf("detectSSHKey = %q, want empty", got)
	}
}
