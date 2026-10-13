terraform {
  required_version = ">= 1.9"
  required_providers {
    helm = {
      source  = "hashicorp/helm"
      version = "2.17.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "3.6.3"
    }
  }
}

resource "helm_release" "workload" {
  name             = var.name
  namespace        = var.namespace
  create_namespace = true
  chart            = var.chart
  version          = var.chart_version
  repository       = var.repository == "" ? null : var.repository
  values           = var.values_file == "" ? [] : [file(var.values_file)]
  wait             = true
  atomic           = true
  timeout          = 300
  postrender {
    binary_path = "${path.module}/../../../.gitops/gitops"
    args        = ["validate-manifests", var.namespace]
  }
  depends_on = [helm_release.gateway]
}

resource "random_id" "cookie" { byte_length = 32 }

resource "helm_release" "gateway" {
  name             = "${var.name}-access"
  namespace        = var.namespace
  create_namespace = true
  chart            = "${path.module}/../../../charts/service-gateway"
  wait             = true
  atomic           = true
  timeout          = 300
  values = [yamlencode({
    hostname      = var.hostname
    issuer        = var.issuer
    clientID      = var.name
    clientSecret  = var.client_secret
    cookieSecret  = random_id.cookie.b64_std
    allowedGroups = var.allowed_groups
    upstream      = "http://${var.upstream_service}.${var.namespace}.svc.cluster.local:${var.upstream_port}"
    nodePort      = var.node_port
  })]
}
