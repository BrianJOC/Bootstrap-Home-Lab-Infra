output "iac_project_id" {
  description = "GitLab project ID for infrastructure/iac"
  value       = gitlab_project.iac.id
}

output "iac_project_url" {
  description = "HTTP clone URL for the infrastructure/iac project"
  value       = gitlab_project.iac.http_url_to_repo
}

output "infrastructure_group_id" {
  description = "GitLab group ID for the infrastructure top-level group"
  value       = gitlab_group.infrastructure.id
}

output "services_group_id" {
  description = "GitLab group ID for the services top-level group"
  value       = gitlab_group.services.id
}

output "runner_registration_token" {
  description = "Registration token for the bootstrap GitLab Runner (sensitive)"
  sensitive   = true
  value       = gitlab_project.iac.runners_token
}
