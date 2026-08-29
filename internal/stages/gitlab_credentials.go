package stages

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/homelab/bootstrap-home-infra/internal/ssh"
)

type gitLabResourceMetadata struct {
	ID        int  `json:"id"`
	EmptyRepo bool `json:"empty_repo"`
}

func findGitLabResource(baseURL, token, kind, path string, insecure bool) (gitLabResourceMetadata, bool, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure} //nolint:gosec -- explicit disposable-test override
	client := &http.Client{Timeout: 30 * time.Second, Transport: transport}
	endpoint := strings.TrimRight(baseURL, "/") + "/" + kind + "/" + url.PathEscape(path)
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return gitLabResourceMetadata{}, false, err
	}
	request.Header.Set("PRIVATE-TOKEN", token)
	response, err := client.Do(request)
	if err != nil {
		return gitLabResourceMetadata{}, false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return gitLabResourceMetadata{}, false, nil
	}
	if response.StatusCode != http.StatusOK {
		return gitLabResourceMetadata{}, false, fmt.Errorf("finding GitLab %s %q returned HTTP %d", kind, path, response.StatusCode)
	}
	var metadata gitLabResourceMetadata
	if err := json.NewDecoder(response.Body).Decode(&metadata); err != nil {
		return gitLabResourceMetadata{}, false, fmt.Errorf("decoding GitLab %s metadata: %w", kind, err)
	}
	if metadata.ID <= 0 {
		return gitLabResourceMetadata{}, false, fmt.Errorf("GitLab %s %q returned no resource ID", kind, path)
	}
	return metadata, true, nil
}

func initializeEmptyGitLabProject(baseURL, token string, projectID int, insecure bool) error {
	payload := map[string]any{
		"branch":         "main",
		"commit_message": "chore: initialize IaC repository [skip ci]",
		"actions": []map[string]string{{
			"action":    "create",
			"file_path": "README.md",
			"content":   "# Infrastructure as Code\n",
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure} //nolint:gosec -- explicit disposable-test override
	client := &http.Client{Timeout: 30 * time.Second, Transport: transport}
	endpoint := fmt.Sprintf("%s/projects/%d/repository/commits", strings.TrimRight(baseURL, "/"), projectID)
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("PRIVATE-TOKEN", token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		return fmt.Errorf("initializing interrupted GitLab project returned HTTP %d", response.StatusCode)
	}
	return nil
}

func storeMAASCredentialInVault(client *ssh.Client, rootToken, maasKey string) error {
	script := `set -Eeuo pipefail
IFS= read -r VAULT_TOKEN
IFS= read -r MAAS_API_KEY
secret_file="$(mktemp)"
trap 'rm -f "$secret_file"' EXIT
chmod 0600 "$secret_file"
printf '%s' "$MAAS_API_KEY" >"$secret_file"
export VAULT_TOKEN VAULT_ADDR=https://127.0.0.1:8200 VAULT_SKIP_VERIFY=true
vault kv put secret/infrastructure/maas/api key=@"$secret_file" >/dev/null
`
	_, stderr, err := client.RunWithStdin(script, []byte(rootToken+"\n"+maasKey+"\n"))
	if err != nil {
		return fmt.Errorf("storing MAAS API credential in Vault: %w: %s", err, strings.TrimSpace(stderr))
	}
	return nil
}

func upsertGitLabProjectVariable(baseURL, projectID, token, key, value string, insecure bool) error {
	payload := map[string]any{
		"key":       key,
		"value":     value,
		"masked":    true,
		"protected": false,
		"raw":       true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: insecure} //nolint:gosec -- explicit disposable-test override
	client := &http.Client{Timeout: 30 * time.Second, Transport: transport}
	variableURL := strings.TrimRight(baseURL, "/") + "/api/v4/projects/" + url.PathEscape(projectID) + "/variables/" + url.PathEscape(key)
	status, err := sendGitLabVariableRequest(client, http.MethodPut, variableURL, token, body)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		collectionURL := strings.TrimSuffix(variableURL, "/"+url.PathEscape(key))
		status, err = sendGitLabVariableRequest(client, http.MethodPost, collectionURL, token, body)
		if err != nil {
			return err
		}
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return fmt.Errorf("upserting masked GitLab variable returned HTTP %d", status)
	}
	return nil
}

func sendGitLabVariableRequest(client *http.Client, method, endpoint, token string, body []byte) (int, error) {
	request, err := http.NewRequest(method, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	request.Header.Set("PRIVATE-TOKEN", token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	return response.StatusCode, nil
}
