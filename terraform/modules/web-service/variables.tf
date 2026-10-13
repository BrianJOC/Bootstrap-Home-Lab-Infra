variable "name" { type = string }
variable "hostname" { type = string }
variable "namespace" { type = string }
variable "chart" { type = string }
variable "chart_version" { type = string }
variable "repository" { type = string }
variable "values_file" { type = string }
variable "upstream_service" { type = string }
variable "upstream_port" { type = number }
variable "node_port" { type = number }
variable "issuer" { type = string }
variable "allowed_groups" { type = list(string) }
variable "client_secret" {
  type      = string
  sensitive = true
}
