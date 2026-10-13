package gitops

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ProtectedKubeconfig emits only the certificate identity and reviewed endpoint.
// It cannot carry a command plugin, filesystem credential, proxy, or context
// override into Helm or the root-owned scheduled monitor.
func ProtectedKubeconfig(data []byte, address string) ([]byte, error) {
	if net.ParseIP(address) == nil {
		return nil, fmt.Errorf("invalid cluster address")
	}
	client, _, err := ApplicationClient(data)
	if err != nil {
		return nil, err
	}
	client.CloseIdleConnections()
	var source map[string]any
	if err = yaml.Unmarshal(data, &source); err != nil {
		return nil, err
	}
	cluster := source["clusters"].([]any)[0].(map[string]any)["cluster"].(map[string]any)
	user := source["users"].([]any)[0].(map[string]any)["user"].(map[string]any)
	for _, field := range []string{"exec", "auth-provider", "tokenFile", "client-certificate", "client-key", "token", "username", "password"} {
		if user[field] != nil {
			return nil, fmt.Errorf("Kubernetes credentials must be embedded certificates")
		}
	}
	return yaml.Marshal(map[string]any{
		"apiVersion": "v1", "kind": "Config", "current-context": "default",
		"clusters": []any{map[string]any{"name": "default", "cluster": map[string]any{"server": "https://" + net.JoinHostPort(address, "6443"), "certificate-authority-data": cluster["certificate-authority-data"]}}},
		"users":    []any{map[string]any{"name": "default", "user": map[string]any{"client-certificate-data": user["client-certificate-data"], "client-key-data": user["client-key-data"]}}},
		"contexts": []any{map[string]any{"name": "default", "context": map[string]any{"cluster": "default", "user": "default"}}},
	})
}

// ApplicationClient uses the protected cluster identity directly. Exec plugins,
// external certificate paths and insecure trust are not accepted in Vault data.
func ApplicationClient(data []byte) (*http.Client, string, error) {
	var kube struct {
		Clusters []struct {
			Cluster struct {
				Server   string `yaml:"server"`
				CA       string `yaml:"certificate-authority-data"`
				Insecure bool   `yaml:"insecure-skip-tls-verify"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
		Users []struct {
			User struct {
				Certificate string `yaml:"client-certificate-data"`
				Key         string `yaml:"client-key-data"`
			} `yaml:"user"`
		} `yaml:"users"`
	}
	if err := yaml.Unmarshal(data, &kube); err != nil || len(kube.Clusters) != 1 || len(kube.Users) != 1 {
		return nil, "", fmt.Errorf("expected one protected Kubernetes client identity")
	}
	c, u := kube.Clusters[0].Cluster, kube.Users[0].User
	ca, err := base64.StdEncoding.DecodeString(c.CA)
	if err != nil || c.Insecure {
		return nil, "", fmt.Errorf("invalid Kubernetes CA trust")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, "", fmt.Errorf("missing Kubernetes CA certificate")
	}
	cert, err := base64.StdEncoding.DecodeString(u.Certificate)
	if err != nil {
		return nil, "", fmt.Errorf("invalid Kubernetes client certificate")
	}
	key, err := base64.StdEncoding.DecodeString(u.Key)
	if err != nil {
		return nil, "", fmt.Errorf("invalid Kubernetes client key")
	}
	identity, err := tls.X509KeyPair(cert, key)
	if err != nil {
		return nil, "", fmt.Errorf("invalid Kubernetes client identity")
	}
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, Certificates: []tls.Certificate{identity}}}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client, c.Server, nil
}

func checkApplication(ctx context.Context, client *http.Client, server string, s Service) error {
	path := strings.TrimPrefix(s.HealthCheck, "http:")
	if s.HealthCheck == "http" {
		path = "/"
	}
	address := fmt.Sprintf("%s/api/v1/namespaces/%s/services/http:%s:%d/proxy%s", server, s.Target.Namespace, s.Target.Service, s.Target.Port, path)
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("service %s application health request failed", s.Name)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("service %s application health returned HTTP %d", s.Name, response.StatusCode)
	}
	return nil
}
