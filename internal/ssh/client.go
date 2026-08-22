package ssh

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// ErrAuthFailed is returned when the SSH server rejects the key.
var ErrAuthFailed = errors.New("SSH authentication failed: check SSH key path and user")

// ErrConnRefused is returned when the target host is not accepting connections.
var ErrConnRefused = errors.New("SSH connection refused: check target host and port")

// ErrConnTimeout is returned when the connection attempt times out.
var ErrConnTimeout = errors.New("SSH connection timed out: check network and firewall")

const (
	defaultDialTimeout    = 10 * time.Second
	defaultCommandTimeout = 5 * time.Minute
)

// Client manages a persistent SSH connection to a target host.
type Client struct {
	host           string
	user           string
	keyPath        string
	knownHostsPath string
	client         *ssh.Client
}

// New creates a new SSH client using key-based authentication only.
func New(host, user, keyPath, knownHostsPath string) (*Client, error) {
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("reading SSH key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("parsing SSH private key: %w", err)
	}

	hostKeyCallback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		return nil, fmt.Errorf("loading SSH trust store: %w", err)
	}

	cfg := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: hostKeyCallback,
		Timeout:         defaultDialTimeout,
	}

	addr := host
	if _, _, err := net.SplitHostPort(host); err != nil {
		// No port specified; add default SSH port.
		addr = net.JoinHostPort(host, "22")
	}

	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, classifyDialError(addr, err)
	}

	return &Client{
		host:           host,
		user:           user,
		keyPath:        keyPath,
		knownHostsPath: knownHostsPath,
		client:         client,
	}, nil
}

func (c *Client) Host() string           { return c.host }
func (c *Client) User() string           { return c.user }
func (c *Client) KeyPath() string        { return c.keyPath }
func (c *Client) KnownHostsPath() string { return c.knownHostsPath }

// Run executes a command on the remote host with the default 5-minute timeout.
// Returns stdout, stderr, and any error.
func (c *Client) Run(cmd string) (stdout, stderr string, err error) {
	return c.RunWithTimeout(cmd, defaultCommandTimeout)
}

// RunWithTimeout executes a command with the given timeout.
func (c *Client) RunWithTimeout(cmd string, timeout time.Duration) (stdout, stderr string, err error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", "", fmt.Errorf("creating SSH session: %w", err)
	}
	defer session.Close()

	var outBuf, errBuf bytes.Buffer
	session.Stdout = &outBuf
	session.Stderr = &errBuf

	done := make(chan error, 1)
	go func() {
		done <- session.Run(cmd)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case runErr := <-done:
		return outBuf.String(), errBuf.String(), runErr
	case <-timer.C:
		session.Close() //nolint:errcheck
		return outBuf.String(), errBuf.String(), fmt.Errorf("command timed out after %s", timeout)
	}
}

// WriteFile streams protected content over the existing authenticated SSH
// session without placing it in a command argument or temporary controller
// file. The destination is atomically replaced.
func (c *Client) WriteFile(path string, content []byte, mode os.FileMode) error {
	session, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("creating SSH upload session: %w", err)
	}
	defer session.Close()

	session.Stdin = bytes.NewReader(content)
	command := fmt.Sprintf(
		"set -e; destination=%q; temporary=\"${destination}.tmp.$$\"; umask 077; cat > \"$temporary\"; chmod %04o \"$temporary\"; mv -f \"$temporary\" \"$destination\"",
		path,
		mode.Perm(),
	)
	if output, err := session.CombinedOutput(command); err != nil {
		return fmt.Errorf("uploading protected file: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// RunWithStdin executes a remote command while streaming sensitive input over
// the SSH channel rather than embedding it in the remote command line.
func (c *Client) RunWithStdin(command string, input []byte) (stdout, stderr string, err error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", "", fmt.Errorf("creating SSH stdin session: %w", err)
	}
	defer session.Close()
	session.Stdin = bytes.NewReader(input)
	var stdoutBuffer, stderrBuffer bytes.Buffer
	session.Stdout = &stdoutBuffer
	session.Stderr = &stderrBuffer
	if err := session.Run(command); err != nil {
		return stdoutBuffer.String(), stderrBuffer.String(), err
	}
	return stdoutBuffer.String(), stderrBuffer.String(), nil
}

// Close closes the underlying SSH connection.
func (c *Client) Close() error {
	return c.client.Close()
}

// classifyDialError maps raw dial errors to the typed sentinel errors above.
func classifyDialError(addr string, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("%w (host: %s)", ErrConnRefused, addr)
	case strings.Contains(msg, "unable to authenticate"),
		strings.Contains(msg, "ssh: handshake failed"),
		strings.Contains(msg, "no supported methods remain"),
		strings.Contains(msg, "publickey"):
		return fmt.Errorf("%w (host: %s)", ErrAuthFailed, addr)
	case strings.Contains(msg, "timeout"), strings.Contains(msg, "timed out"),
		strings.Contains(msg, "deadline exceeded"):
		return fmt.Errorf("%w (host: %s)", ErrConnTimeout, addr)
	default:
		return fmt.Errorf("dialing %s: %w", addr, err)
	}
}

// DetectArch runs uname -m on the remote host and returns the result.
func (c *Client) DetectArch() (string, error) {
	stdout, _, err := c.Run("uname -m")
	if err != nil {
		return "", fmt.Errorf("detecting arch: %w", err)
	}
	return stdout, nil
}

// DetectOS runs lsb_release on the remote host and returns OS name and version.
func (c *Client) DetectOS() (name, version string, err error) {
	nameOut, _, err := c.Run("lsb_release -is")
	if err != nil {
		return "", "", fmt.Errorf("detecting OS name: %w", err)
	}
	versionOut, _, err := c.Run("lsb_release -rs")
	if err != nil {
		return "", "", fmt.Errorf("detecting OS version: %w", err)
	}
	return nameOut, versionOut, nil
}

// ListBlockDevices runs lsblk on the remote host and returns JSON output.
// Returns the raw JSON string from: lsblk -d -o NAME,SIZE,TYPE,TRAN -J
func (c *Client) ListBlockDevices() (string, error) {
	stdout, _, err := c.Run("lsblk -d -o NAME,SIZE,TYPE,TRAN -J")
	if err != nil {
		return "", fmt.Errorf("listing block devices: %w", err)
	}
	return stdout, nil
}

// FindBootPartition returns the device path of the root filesystem mount source.
// Example: "/dev/mmcblk0p1" or "/dev/sda1"
func (c *Client) FindBootPartition() (string, error) {
	stdout, _, err := c.Run("findmnt / -o SOURCE -n")
	if err != nil {
		return "", fmt.Errorf("finding boot partition: %w", err)
	}
	return strings.TrimSpace(stdout), nil
}

// validPartitionRE matches device partition paths produced by findmnt on Linux.
// Accepts: /dev/sdXN, /dev/nvmeXnXpX, /dev/mmcblkXpX, /dev/vdXN
var validPartitionRE = regexp.MustCompile(`^/dev/(sd[a-z]+[0-9]+|nvme[0-9]+n[0-9]+p[0-9]+|mmcblk[0-9]+p[0-9]+|vd[a-z]+[0-9]+)$`)

// FindParentDisk returns the parent disk name for a partition device.
// e.g., "/dev/mmcblk0p1" -> "mmcblk0", "/dev/sda1" -> "sda"
// Uses lsblk -no pkname for architecture-agnostic detection.
// Returns an error if the partition path does not match a known device pattern.
func (c *Client) FindParentDisk(partition string) (string, error) {
	if !validPartitionRE.MatchString(partition) {
		return "", fmt.Errorf("unrecognised partition path %q (not a standard block device)", partition)
	}
	stdout, _, err := c.Run(fmt.Sprintf("lsblk -no pkname %s", partition))
	if err != nil {
		return "", fmt.Errorf("finding parent disk for %s: %w", partition, err)
	}
	return strings.TrimSpace(stdout), nil
}
