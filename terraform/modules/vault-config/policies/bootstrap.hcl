# bootstrap policy: full access to bootstrap secrets namespace
path "secret/data/bootstrap/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}

path "secret/metadata/bootstrap/*" {
  capabilities = ["list", "read"]
}

path "secret/delete/bootstrap/*" {
  capabilities = ["update"]
}

path "secret/destroy/bootstrap/*" {
  capabilities = ["update"]
}

# Allow reading PKI CA cert
path "pki/cert/ca" {
  capabilities = ["read"]
}

# Allow managing PKI roles and issuing certs
path "pki/roles/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}

path "pki/issue/*" {
  capabilities = ["create", "update"]
}
