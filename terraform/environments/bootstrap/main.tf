terraform {
  required_version = ">= 1.6"

  required_providers {
    null = {
      source  = "hashicorp/null"
      version = "~> 3.0"
    }
    vault = {
      source  = "hashicorp/vault"
      version = "~> 3.0"
    }
    authentik = {
      source  = "goauthentik/authentik"
      version = "~> 2024.2.0"  # patch-pinned — see authentik-config/main.tf
    }
    gitlab = {
      source  = "gitlabhq/gitlab"
      version = "~> 16.0"
    }
    maas = {
      source  = "maas/maas"
      version = "~> 2.3"
    }
  }
}

# ── DNS Records ───────────────────────────────────────────────────────────────
# Creates A records for all bootstrap services in Technitium.
# Called during Stage 1 (DNS) and again whenever a new service is added.

module "dns_records" {
  source = "../../modules/dns-records"

  technitium_url       = var.technitium_url
  technitium_api_token = var.technitium_api_token
  zone                 = var.internal_domain

  # Default bootstrap service records; operator may extend via var.dns_records
  records = concat(
    [
      { name = "vault",      type = "A", value = var.bootstrap_server_ip, ttl = 300 },
      { name = "authentik",  type = "A", value = var.bootstrap_server_ip, ttl = 300 },
      { name = "gitlab",     type = "A", value = var.bootstrap_server_ip, ttl = 300 },
      { name = "technitium", type = "A", value = var.bootstrap_server_ip, ttl = 300 },
      { name = "maas",       type = "A", value = var.bootstrap_server_ip, ttl = 300 },
    ],
    var.dns_records,
  )
}

# ── Vault Configuration ───────────────────────────────────────────────────────
# Configures Vault post-initialization: secrets engines, policies, auth methods,
# TLS cert storage. Runs after Stage 3 (Vault) is operational.

module "vault_config" {
  source = "../../modules/vault-config"

  vault_address   = var.vault_address
  vault_token     = var.vault_token
  internal_domain = var.internal_domain
  cert_path       = var.cert_path
  key_path        = var.key_path
}

# ── Authentik Configuration ───────────────────────────────────────────────────
# Creates groups, OIDC providers, and applications for bootstrap services.
# Runs after Stage 4 (Authentik) is operational.

module "authentik_config" {
  source = "../../modules/authentik-config"

  authentik_url   = var.authentik_url
  authentik_token = var.authentik_token
  internal_domain = var.internal_domain

  services = length(var.authentik_services) > 0 ? var.authentik_services : [
    {
      name         = "vault"
      redirect_uri = "https://vault.${var.internal_domain}/ui/vault/auth/oidc/oidc/callback"
      groups       = ["adults"]
    },
    {
      name         = "gitlab"
      redirect_uri = "https://gitlab.${var.internal_domain}/users/auth/openid_connect/callback"
      groups       = ["adults"]
    },
  ]

  depends_on = [module.vault_config]
}

# ── GitLab Configuration ──────────────────────────────────────────────────────
# Creates top-level groups, the infrastructure/iac project, and CI/CD variables.
# Runs after Stage 6 (GitLab) is operational.

module "gitlab_config" {
  source = "../../modules/gitlab-config"

  gitlab_url      = var.gitlab_url
  gitlab_token    = var.gitlab_token
  vault_address   = var.vault_address
  internal_domain = var.internal_domain

  depends_on = [module.vault_config]
}

# ── MAAS Configuration ────────────────────────────────────────────────────────
# Creates the homelab fabric, lab VLAN, and lab subnet with DHCP.
# Runs after Stage 7 (MAAS) is operational.

module "maas_config" {
  source = "../../modules/maas-config"

  maas_api_url    = var.maas_api_url
  maas_api_key    = var.maas_api_key
  lab_subnet      = var.lab_subnet
  lab_vlan_id     = var.lab_vlan_id
  dns_server      = var.bootstrap_server_ip
  lab_gateway     = var.lab_gateway
  dhcp_range_low  = var.dhcp_range_low
  dhcp_range_high = var.dhcp_range_high
}
