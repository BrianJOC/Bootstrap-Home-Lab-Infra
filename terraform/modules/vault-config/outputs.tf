output "kv_mount_path" {
  description = "Path of the KV v2 secrets engine mount"
  value       = vault_mount.kv.path
}

output "pki_mount_path" {
  description = "Path of the PKI secrets engine mount"
  value       = vault_mount.pki.path
}

output "bootstrap_policy_name" {
  description = "Name of the bootstrap Vault policy"
  value       = vault_policy.bootstrap.name
}
