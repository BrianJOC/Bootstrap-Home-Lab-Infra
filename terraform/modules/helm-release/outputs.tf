output "release_name" {
  description = "Helm release name"
  value       = helm_release.this.name
}

output "namespace" {
  description = "Kubernetes namespace the release was deployed into"
  value       = helm_release.this.namespace
}

output "chart_version" {
  description = "Deployed Helm chart version"
  value       = helm_release.this.version
}

output "status" {
  description = "Status of the Helm release"
  value       = helm_release.this.status
}
