package adoption

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var ErrAlreadyAdopted = errors.New("server already has a node definition")

type GitLabClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewGitLabClient(baseURL, token string) *GitLabClient {
	return NewGitLabClientWithTLS(baseURL, token, false)
}

// NewGitLabClientWithTLS allows the explicit disposable-test TLS override used
// by the bootstrap integration environment. Real homelab operations validate
// the configured GitLab certificate.
func NewGitLabClientWithTLS(baseURL, token string, insecure bool) *GitLabClient {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure} //nolint:gosec -- explicit disposable-test override
	return &GitLabClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 30 * time.Second, Transport: transport},
	}
}

// ValidateCredential distinguishes an expired/revoked token from a GitLab
// connectivity or server failure so adoption can refresh only credentials.
func (c *GitLabClient) ValidateCredential() (bool, error) {
	status, _, err := c.request(http.MethodGet, "/api/v4/user", nil)
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusOK:
		return true, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return false, nil
	default:
		return false, fmt.Errorf("validating GitLab adoption credential returned HTTP %d", status)
	}
}

func (c *GitLabClient) OpenAdoptionMergeRequest(audit Audit, node []byte) (string, error) {
	project := url.PathEscape("infrastructure/iac")
	filePath := "infrastructure/nodes/" + audit.Hostname + ".yaml"
	status, _, err := c.request(http.MethodGet, "/api/v4/projects/"+project+"/repository/files/"+url.PathEscape(filePath)+"?ref=main", nil)
	if err != nil {
		return "", err
	}
	if status == http.StatusOK {
		return "", fmt.Errorf("%w: %s", ErrAlreadyAdopted, filePath)
	}
	if status != http.StatusNotFound {
		return "", fmt.Errorf("checking existing node definition returned HTTP %d", status)
	}
	status, response, err := c.request(http.MethodGet, "/api/v4/projects/"+project+"/merge_requests?state=opened&per_page=100", nil)
	if err != nil {
		return "", fmt.Errorf("checking open adoption merge requests: %w", err)
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("checking open adoption merge requests returned HTTP %d", status)
	}
	var openMergeRequests []struct {
		SourceBranch string `json:"source_branch"`
		WebURL       string `json:"web_url"`
	}
	if err = json.Unmarshal(response, &openMergeRequests); err != nil {
		return "", fmt.Errorf("decoding open adoption merge requests: %w", err)
	}
	for _, mergeRequest := range openMergeRequests {
		if strings.HasPrefix(mergeRequest.SourceBranch, "adopt/"+audit.Hostname+"-") {
			return "", fmt.Errorf("%w: an open draft already exists at %s", ErrAlreadyAdopted, mergeRequest.WebURL)
		}
	}

	branch := "adopt/" + audit.Hostname + "-" + time.Now().UTC().Format("20060102-150405")
	branchBody := url.Values{"branch": {branch}, "ref": {"main"}}
	if status, _, err = c.request(http.MethodPost, "/api/v4/projects/"+project+"/repository/branches", strings.NewReader(branchBody.Encode())); err != nil || status != http.StatusCreated {
		return "", fmt.Errorf("creating adoption branch returned HTTP %d: %w", status, err)
	}

	commit := map[string]any{
		"branch":         branch,
		"commit_message": "adopt: add node " + audit.Hostname,
		"actions": []map[string]string{{
			"action":    "create",
			"file_path": filePath,
			"content":   string(node),
		}},
	}
	if status, _, err = c.requestJSON(http.MethodPost, "/api/v4/projects/"+project+"/repository/commits", commit); err != nil || status != http.StatusCreated {
		return "", fmt.Errorf("committing adoption definition returned HTTP %d: %w", status, err)
	}

	mr := map[string]any{
		"source_branch":        branch,
		"target_branch":        "main",
		"title":                fmt.Sprintf("Draft: adopt: add node %s (%s)", audit.Hostname, audit.Target),
		"description":          MergeRequestDescription(audit),
		"remove_source_branch": true,
	}
	status, response, err = c.requestJSON(http.MethodPost, "/api/v4/projects/"+project+"/merge_requests", mr)
	if err != nil || status != http.StatusCreated {
		return "", fmt.Errorf("opening adoption merge request returned HTTP %d: %w", status, err)
	}
	var created struct {
		WebURL string `json:"web_url"`
	}
	if err := json.Unmarshal(response, &created); err != nil || created.WebURL == "" {
		return "", fmt.Errorf("GitLab response did not contain a merge request URL")
	}
	return created.WebURL, nil
}

func (c *GitLabClient) requestJSON(method, path string, value any) (int, []byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return 0, nil, err
	}
	return c.request(method, path, bytes.NewReader(payload))
}

func (c *GitLabClient) request(method, path string, body io.Reader) (int, []byte, error) {
	request, err := http.NewRequest(method, c.baseURL+path, body)
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("PRIVATE-TOKEN", c.token)
	if body != nil {
		if _, ok := body.(*strings.Reader); ok {
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		} else {
			request.Header.Set("Content-Type", "application/json")
		}
	}
	response, err := c.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return response.StatusCode, payload, err
}
