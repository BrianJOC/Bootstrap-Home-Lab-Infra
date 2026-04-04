// internal/tui/sshkey.go
package tui

import (
	"os"
	"path/filepath"
)

// detectSSHKey returns the first found SSH private key in sshDir, checking
// id_ed25519, id_rsa, id_ed25519_sk in order. Returns "" if none found.
func detectSSHKey(sshDir string) string {
	for _, name := range []string{"id_ed25519", "id_rsa", "id_ed25519_sk"} {
		p := filepath.Join(sshDir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}
