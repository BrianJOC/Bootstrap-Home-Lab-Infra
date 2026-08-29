locals {
  node_files = fileset("${path.module}/../infrastructure/nodes", "*.yaml")
  nodes = {
    for node_file in local.node_files : trimsuffix(node_file, ".yaml") =>
    yamldecode(file("${path.module}/../infrastructure/nodes/${node_file}"))
  }
  deploy_nodes = {
    for name, node in local.nodes : name => node
    if try(node.deploy, true)
  }
}

resource "maas_machine" "adopted" {
  for_each = local.nodes

  hostname         = each.value.hostname
  architecture     = each.value.architecture
  pxe_mac_address  = each.value.pxe_mac_address
  power_type       = each.value.power_type
  power_parameters = jsonencode(each.value.power_parameters)
}

# maas_machine registers and commissions the reviewed hardware. maas_instance
# is the provider's separate allocation/deployment lifecycle, so retaining both
# resources prevents a merged adoption definition from stopping at Ready.
resource "maas_instance" "adopted" {
  for_each = local.deploy_nodes

  allocate_params {
    hostname     = maas_machine.adopted[each.key].hostname
    architecture = each.value.architecture
  }

  deploy_params {
    distro_series = try(each.value.distro_series, "noble")
  }

  release_params {
    erase = false
    force = false
  }

  timeouts {
    create = "60m"
    delete = "30m"
  }
}

output "adopted_nodes" {
  description = "MAAS-deployed nodes awaiting deliberate SSH identity confirmation before cluster join."
  value = {
    for name, instance in maas_instance.adopted : name => {
      fqdn         = instance.fqdn
      ip_addresses = instance.ip_addresses
      cluster      = try(local.nodes[name].cluster, null)
    }
  }
}
