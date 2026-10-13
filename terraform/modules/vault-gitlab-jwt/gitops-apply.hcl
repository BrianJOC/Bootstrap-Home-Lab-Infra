path "secret/data/bootstrap/gitops/runtime" { capabilities = ["read"] }
path "sys/mounts/*" { capabilities = ["create", "read", "update", "delete", "sudo"] }
path "sys/auth/*" { capabilities = ["create", "read", "update", "delete", "sudo"] }
path "sys/audit/*" { capabilities = ["create", "read", "update", "delete", "sudo"] }
path "sys/policies/acl/*" { capabilities = ["create", "read", "update", "delete", "list"] }
path "auth/*" { capabilities = ["create", "read", "update", "delete", "list"] }
