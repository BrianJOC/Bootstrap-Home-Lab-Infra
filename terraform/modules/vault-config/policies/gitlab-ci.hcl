# gitlab-ci policy: read-only access to service and infrastructure secrets
# Used by GitLab CI pipelines authenticating via JWT
path "secret/data/services/*" {
  capabilities = ["read"]
}

path "secret/data/infrastructure/*" {
  capabilities = ["read"]
}

path "secret/metadata/services/*" {
  capabilities = ["list", "read"]
}

path "secret/metadata/infrastructure/*" {
  capabilities = ["list", "read"]
}
