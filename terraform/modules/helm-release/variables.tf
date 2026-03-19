variable "cluster_name" {
  type        = string
  description = "Name of the target k3s cluster (informational; used in resource names)"
}

variable "kubeconfig_path" {
  type        = string
  description = "Absolute path to the kubeconfig file for the target cluster"
}

variable "release_name" {
  type        = string
  description = "Helm release name"
}

variable "chart" {
  type        = string
  description = "Helm chart name or OCI chart reference"
}

variable "chart_version" {
  type        = string
  description = "Helm chart version to deploy"
}

variable "repository" {
  type        = string
  default     = ""
  description = "Helm chart repository URL; omit for OCI charts or charts already in the default repo"
}

variable "namespace" {
  type        = string
  default     = "default"
  description = "Kubernetes namespace to deploy the Helm release into"
}

variable "values_file" {
  type        = string
  default     = ""
  description = "Absolute path to a values.yaml file; empty string means no additional values"
}

variable "set_values" {
  type        = map(string)
  default     = {}
  description = "Map of --set key=value overrides applied on top of any values file"
}

variable "wait" {
  type        = bool
  default     = true
  description = "Whether to wait for all resources to be ready before marking the release complete"
}

variable "timeout" {
  type        = number
  default     = 300
  description = "Timeout in seconds for Helm operations"
}
