output "client_ids" {
  description = "Map of service name to OIDC client ID"
  value       = { for k, v in authentik_provider_oauth2.service : k => v.client_id }
}

output "client_secrets" {
  description = "Map of service name to OIDC client secret (sensitive)"
  sensitive   = true
  value       = { for k, v in authentik_provider_oauth2.service : k => v.client_secret }
}

output "application_slugs" {
  description = "Map of service name to Authentik application slug"
  value       = { for k, v in authentik_application.service : k => v.slug }
}

output "group_ids" {
  description = "Map of group name to Authentik group ID"
  value       = { for k, v in authentik_group.managed : k => v.id }
}
