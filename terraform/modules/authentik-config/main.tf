terraform {
  required_version = ">= 1.6"
  required_providers {
    authentik = {
      source  = "goauthentik/authentik"
      version = "~> 2024.2"
    }
  }
}

provider "authentik" {
  url   = var.authentik_url
  token = var.authentik_token
}

# ── Data Sources ──────────────────────────────────────────────────────────────

# Default signing certificate key pair (created by Authentik on first run)
data "authentik_certificate_key_pair" "default" {
  name = "authentik Self-signed Certificate"
}

# Default authorization flow
data "authentik_flow" "default_authorization" {
  slug = "default-provider-authorization-implicit-consent"
}

# ── Groups ────────────────────────────────────────────────────────────────────

# Collect all unique group names referenced across services
locals {
  all_groups = toset(flatten([for s in var.services : s.groups]))
}

resource "authentik_group" "managed" {
  for_each = local.all_groups
  name     = each.key
}

# ── OIDC Providers ────────────────────────────────────────────────────────────

resource "authentik_provider_oauth2" "service" {
  for_each = { for s in var.services : s.name => s }

  name               = each.value.name
  client_id          = each.value.name
  redirect_uris      = [each.value.redirect_uri]
  signing_key        = data.authentik_certificate_key_pair.default.id
  authorization_flow = data.authentik_flow.default_authorization.id

  # Scopes: openid, email, profile — minimum required by most services
  property_mappings = data.authentik_scope_mapping.standard.ids
}

# Standard OpenID scope mappings
data "authentik_scope_mapping" "standard" {
  managed_list = [
    "goauthentik.io/providers/oauth2/scope-openid",
    "goauthentik.io/providers/oauth2/scope-email",
    "goauthentik.io/providers/oauth2/scope-profile",
  ]
}

# ── Applications ──────────────────────────────────────────────────────────────

resource "authentik_application" "service" {
  for_each = { for s in var.services : s.name => s }

  name              = each.value.name
  slug              = each.value.name
  protocol_provider = authentik_provider_oauth2.service[each.key].id
  meta_launch_url   = "https://${each.value.name}.${var.internal_domain}"
}

# ── Policy Bindings: restrict each app to its declared groups ─────────────────

resource "authentik_policy_binding" "app_group" {
  for_each = {
    for entry in flatten([
      for s in var.services : [
        for g in s.groups : {
          key  = "${s.name}-${g}"
          app  = s.name
          group = g
        }
      ]
    ]) : entry.key => entry
  }

  target = authentik_application.service[each.value.app].uuid
  group  = authentik_group.managed[each.value.group].id
  order  = 0
}
