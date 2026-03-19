output "record_names" {
  description = "Fully qualified DNS names of all managed records"
  value       = [for r in var.records : "${r.name}.${var.zone}"]
}
