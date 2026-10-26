// Package gitlab is a minimal GitLab REST client for bootstrap stages that need to read
// project content (Stage 8 looks for cluster definitions in infrastructure/iac).
//
// TLS verification is deliberately disabled for this internal-only endpoint: the internal CA
// is not distributed to the bootstrap controller, and the gitlab_install Ansible role talks
// to the same endpoint with `validate_certs: false`. Requests carry the short-lived bootstrap
// PAT (read-only use) across the lab network. Revisit if the internal CA ever reaches the
// controllers.
package gitlab

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrPathNotFound reports that the project, revision or tree path does not exist.
var ErrPathNotFound = errors.New("gitlab: path not found")

// TokenFile is the controller-side PAT file written by the gitlab_install role.
// It is a variable so tests can point at a fixture.
var TokenFile = defaultTokenFile()

func defaultTokenFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".bootstrap", "gitlab-api-token")
	}
	return filepath.Join(home, ".bootstrap", "gitlab-api-token")
}

// TokenFromHome reads the bootstrap PAT from TokenFile.
func TokenFromHome() (string, error) {
	data, err := os.ReadFile(TokenFile)
	if err != nil {
		return "", fmt.Errorf("reading GitLab token %q: %w", TokenFile, err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("GitLab token file %q is empty", TokenFile)
	}
	return token, nil
}

// TreeEntry is one item from the repository tree API.
type TreeEntry struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
}

// Client is a minimal GitLab API client.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient returns a client for baseURL (e.g. "https://10.0.1.238").
func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP: &http.Client{
			Timeout: 20 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					//nolint:gosec // internal-only endpoint; see package comment
					InsecureSkipVerify: true,
				},
			},
		},
	}
}

// ListTree lists the entries under treePath in projectPath at ref.
// It returns ErrPathNotFound when the project path or tree path does not exist.
func (c *Client) ListTree(ctx context.Context, projectPath, treePath, ref string) ([]TreeEntry, error) {
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/repository/tree?ref=%s&path=%s",
		c.BaseURL,
		url.PathEscape(projectPath),
		url.QueryEscape(ref),
		url.QueryEscape(treePath),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("building GitLab tree request: %w", err)
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GitLab tree request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch resp.StatusCode {
	case http.StatusOK:
		var entries []TreeEntry
		if err := json.Unmarshal(body, &entries); err != nil {
			return nil, fmt.Errorf("parsing GitLab tree response: %w", err)
		}
		return entries, nil
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s/%s (ref %s)", ErrPathNotFound, projectPath, treePath, ref)
	default:
		return nil, fmt.Errorf("GitLab tree request returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
}
