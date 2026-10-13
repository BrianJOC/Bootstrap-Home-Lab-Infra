# Provider credentials remain inside Vault. This private project is operated by
# one trusted administrator; provider API credentials are not safe to expose to
# arbitrary/untrusted merge-request authors. JWT roles reject fork projects.
path "secret/data/bootstrap/gitops/providers" { capabilities = ["read"] }
path "secret/data/infrastructure/clusters/*" { capabilities = ["read"] }
path "sys/mounts" { capabilities = ["read"] }
path "sys/mounts/*" { capabilities = ["read"] }
path "sys/auth" { capabilities = ["read"] }
path "sys/auth/*" { capabilities = ["read"] }
path "sys/audit" { capabilities = ["read"] }
path "sys/policies/acl/*" { capabilities = ["read", "list"] }
path "auth/*" { capabilities = ["read", "list"] }
path "auth/token/create" { capabilities = ["update"] }
path "auth/token/lookup-self" { capabilities = ["read"] }
path "auth/token/renew-self" { capabilities = ["update"] }
path "auth/token/revoke-self" { capabilities = ["update"] }
