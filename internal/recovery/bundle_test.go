package recovery

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/homelab/bootstrap-home-infra/internal/config"
)

func TestCreateInspectAndDecrypt(t *testing.T) {
	tempDir := t.TempDir()
	knownHostsPath := filepath.Join(tempDir, "known_hosts")
	keyPath := filepath.Join(tempDir, "id_ed25519")
	bundlePath := filepath.Join(tempDir, "output", "controller-bundle.tar.gz")
	if err := os.WriteFile(knownHostsPath, []byte("host ssh-ed25519 AAAA\n"), 0600); err != nil {
		t.Fatal(err)
	}
	privateKey := []byte("test-private-key")
	if err := os.WriteFile(keyPath, privateKey, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.DefaultConfig()
	cfg.TargetHost = "host"
	cfg.SSHKeyPath = keyPath
	cfg.SSHHostFingerprint = "SHA256:expected"

	if err := Create(bundlePath, BundleOptions{
		Config:         cfg,
		KnownHostsPath: knownHostsPath,
		SSHKeyPath:     keyPath,
		Password:       "separate-password",
		IncludeSSHKey:  true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	info, err := Inspect(bundlePath)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Target != "host" {
		t.Errorf("target = %q", info.Target)
	}
	decrypted, err := DecryptSSHKey(info.Files["ssh/private-key.enc"], "separate-password")
	if err != nil {
		t.Fatalf("DecryptSSHKey: %v", err)
	}
	if string(decrypted) != string(privateKey) {
		t.Error("decrypted key differs")
	}
	if _, err := DecryptSSHKey(info.Files["ssh/private-key.enc"], "wrong-password"); err == nil {
		t.Error("wrong password unexpectedly decrypted the key")
	}
	fileInfo, err := os.Stat(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0600 {
		t.Errorf("bundle mode = %04o", fileInfo.Mode().Perm())
	}

	restorePath := filepath.Join(tempDir, "restored")
	if err := Restore(bundlePath, restorePath, "separate-password"); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	restoredKey, err := os.ReadFile(filepath.Join(restorePath, "ssh", "id_ed25519"))
	if err != nil {
		t.Fatal(err)
	}
	if string(restoredKey) != string(privateKey) {
		t.Error("restored key differs")
	}
	if err := Restore(bundlePath, restorePath, "separate-password"); err == nil {
		t.Error("restore unexpectedly overwrote an existing directory")
	}
}
