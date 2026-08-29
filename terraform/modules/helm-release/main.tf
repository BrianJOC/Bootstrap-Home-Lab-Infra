terraform {
  required_version = ">= 1.6"
  required_providers {
    helm = {
      source  = "hashicorp/helm"
      version = "2.17.0"
    }
  }
}

provider "helm" {
  kubernetes {
    config_path = var.kubeconfig_path
  }
}

resource "helm_release" "this" {
  name             = var.release_name
  chart            = var.chart
  version          = var.chart_version
  repository       = var.repository != "" ? var.repository : null
  namespace        = var.namespace
  create_namespace = true
  wait             = var.wait
  timeout          = var.timeout

  values = var.values_file != "" ? [file(var.values_file)] : []

  dynamic "set" {
    for_each = var.set_values
    content {
      name  = set.key
      value = set.value
    }
  }
}
