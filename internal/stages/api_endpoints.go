package stages

import (
	"fmt"
	"strings"

	"github.com/homelab/bootstrap-home-infra/internal/config"
)

func technitiumAPIURL(cfg *config.Config) string {
	return endpointOr(cfg.TechnitiumAPIURL, "http://"+cfg.BootstrapServerIP+":5380")
}

func vaultAPIURL(cfg *config.Config) string {
	return endpointOr(cfg.VaultAPIURL, "https://"+cfg.BootstrapServerIP+":8200")
}

func authentikAPIURL(cfg *config.Config) string {
	return endpointOr(cfg.AuthentikAPIURL, "http://"+cfg.BootstrapServerIP+":9000")
}

func gitlabAPIURL(cfg *config.Config) string {
	return endpointOr(cfg.GitLabAPIURL, "https://gitlab."+cfg.InternalDomain+"/api/v4/")
}

func gitlabBaseURL(cfg *config.Config) string {
	return endpointOr(cfg.GitLabBaseURL, "https://gitlab."+cfg.InternalDomain)
}

func maasAPIURL(cfg *config.Config) string {
	return endpointOr(cfg.MAASAPIURL, "http://"+cfg.BootstrapServerIP+":5240/MAAS")
}

func endpointOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimRight(value, "/")
}

func boolString(value bool) string { return fmt.Sprintf("%t", value) }
