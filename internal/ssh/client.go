package ssh

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	defaultDialTimeout    = 10 * time.Second
	defaultCommandTimeout = 5 * time.Minute
)

// Client manages a persistent SSH connection to a target host.
type Client struct {
	host   string
	user   string
	client *ssh.Client
}

// New creates a new SSH client using key-based authentication only.
func New(host, user, keyPath string) (*Client, error) {
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("reading SSH key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("parsing SSH private key: %w", err)
	}

	cfg := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		// Accept any host key during bootstrap; operators validate host identity via other means.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec
		Timeout:         defaultDialTimeout,
	}

	addr := host
	if _, _, err := net.SplitHostPort(host); err != nil {
		// No port specified; add default SSH port.
		addr = net.JoinHostPort(host, "22")
	}

	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("dialing %s: %w", addr, err)
	}

	return &Client{
		host:   host,
		user:   user,
		client: client,
	}, nil
}

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

	select {
	case runErr := <-done:
		return outBuf.String(), errBuf.String(), runErr
	case <-time.After(timeout):
		session.Close() //nolint:errcheck
		return outBuf.String(), errBuf.String(), fmt.Errorf("command timed out after %s", timeout)
	}
}

// Close closes the underlying SSH connection.
func (c *Client) Close() error {
	return c.client.Close()
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
