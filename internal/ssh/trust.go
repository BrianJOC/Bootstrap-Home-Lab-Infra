package ssh

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type TrustState int

const (
	TrustUnknown TrustState = iota
	TrustMatch
	TrustChanged
)

type HostKeyInfo struct {
	Target      string
	Address     string
	Algorithm   string
	Fingerprint string
	Key         gossh.PublicKey
}

func DefaultKnownHostsPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".local", "share", "bootstrap-home-infra", "ssh", "known_hosts"), nil
}

func NormalizeTarget(target string) string {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		host = target
		port = "22"
	}
	if port == "22" {
		return host
	}
	return net.JoinHostPort(host, port)
}

func targetAddress(target string) string {
	if _, _, err := net.SplitHostPort(target); err == nil {
		return target
	}
	return net.JoinHostPort(target, "22")
}

// ObserveHostKey performs only enough of the SSH handshake to receive the
// server public key. The callback deliberately aborts before authentication.
func ObserveHostKey(target string) (HostKeyInfo, error) {
	addr := targetAddress(target)
	conn, err := net.DialTimeout("tcp", addr, defaultDialTimeout)
	if err != nil {
		return HostKeyInfo{}, classifyDialError(addr, err)
	}
	defer conn.Close()

	var observed HostKeyInfo
	probeStop := errors.New("host key observed")
	cfg := &gossh.ClientConfig{
		User:    "bootstrap-host-key-probe",
		Timeout: defaultDialTimeout,
		HostKeyCallback: func(_ string, remote net.Addr, key gossh.PublicKey) error {
			observed = HostKeyInfo{
				Target:      target,
				Address:     remote.String(),
				Algorithm:   key.Type(),
				Fingerprint: gossh.FingerprintSHA256(key),
				Key:         key,
			}
			return probeStop
		},
	}
	_ = conn.SetDeadline(time.Now().Add(defaultDialTimeout))
	_, _, _, err = gossh.NewClientConn(conn, addr, cfg)
	if observed.Key == nil {
		return HostKeyInfo{}, fmt.Errorf("observing SSH host key from %s: %w", addr, err)
	}
	return observed, nil
}

// InspectHostKey compares an observed key with the target's current pin.
func InspectHostKey(path, target string, observed gossh.PublicKey) (TrustState, string, error) {
	lines, err := readKnownHosts(path)
	if err != nil {
		return TrustUnknown, "", err
	}
	normalized := knownhosts.Normalize(NormalizeTarget(target))
	for _, line := range lines {
		hosts, key, parseErr := parseKnownHostsLine(line)
		if parseErr != nil {
			return TrustUnknown, "", fmt.Errorf("parsing SSH trust store %q: %w", path, parseErr)
		}
		for _, host := range hosts {
			if host != normalized {
				continue
			}
			oldFingerprint := gossh.FingerprintSHA256(key)
			if string(key.Marshal()) == string(observed.Marshal()) {
				return TrustMatch, oldFingerprint, nil
			}
			return TrustChanged, oldFingerprint, nil
		}
	}
	return TrustUnknown, "", nil
}

// PinHostKey atomically adds or replaces only the selected target entry.
func PinHostKey(path, target string, key gossh.PublicKey) error {
	lines, err := readKnownHosts(path)
	if err != nil {
		return err
	}
	normalized := knownhosts.Normalize(NormalizeTarget(target))
	filtered := make([]string, 0, len(lines)+1)
	for _, line := range lines {
		hosts, _, parseErr := parseKnownHostsLine(line)
		if parseErr != nil {
			return fmt.Errorf("parsing SSH trust store %q: %w", path, parseErr)
		}
		remove := false
		for _, host := range hosts {
			if host == normalized {
				remove = true
				break
			}
		}
		if !remove {
			filtered = append(filtered, line)
		}
	}
	filtered = append(filtered, knownhosts.Line([]string{normalized}, key))

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating SSH trust directory: %w", err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return fmt.Errorf("restricting SSH trust directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "known_hosts-*.tmp")
	if err != nil {
		return fmt.Errorf("creating SSH trust temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	content := strings.Join(filtered, "\n")
	if content != "" {
		content += "\n"
	}
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("activating SSH trust file: %w", err)
	}
	return nil
}

func parseKnownHostsLine(line string) ([]string, gossh.PublicKey, error) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return nil, nil, fmt.Errorf("invalid known_hosts entry")
	}
	hostIndex := 0
	if strings.HasPrefix(fields[0], "@") {
		hostIndex = 1
	}
	if len(fields) < hostIndex+3 {
		return nil, nil, fmt.Errorf("invalid known_hosts entry")
	}
	key, _, _, _, err := gossh.ParseAuthorizedKey(
		[]byte(strings.Join(fields[hostIndex+1:], " ")),
	)
	if err != nil {
		return nil, nil, err
	}
	return strings.Split(fields[hostIndex], ","), key, nil
}

func readKnownHosts(path string) ([]string, error) {
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opening SSH trust store: %w", err)
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading SSH trust store: %w", err)
	}
	return lines, nil
}
