# Several switches in one provider configuration. Each resource and data source
# picks a switch with `switch`; the names are the keys of the `switches` map.
#
# Ids and import ids of a named switch carry its name: `sw02:led`, `sw02:7`.
# To adopt an existing object: terraform import 'tplinkeasysmart_led.this["sw02"]' sw02:led

variable "switches" {
  description = "Switches by name. Each needs `host`, `mac` or both: the MAC pins the identity and finds the switch when its address changes."
  type = map(object({
    host     = optional(string)
    mac      = optional(string)
    username = string
    password = string
  }))
  sensitive = true
}

# Switch names are not secret; only the map's values are. for_each needs the names unmasked.
locals {
  names = toset(nonsensitive(keys(var.switches)))
}

provider "tplinkeasysmart" {
  switches = var.switches
}

# What every switch reports about itself.
data "tplinkeasysmart_system_info" "this" {
  for_each = local.names
  switch   = each.key
}

# The same setting on every switch.
resource "tplinkeasysmart_loop_prevention" "this" {
  for_each = local.names
  switch   = each.key
  enabled  = true
}

# A per-switch, per-port object: key it by "<switch>/<port>".
locals {
  free_ports = {
    for pair in setproduct(local.names, [6, 7]) :
    "${pair[0]}/${pair[1]}" => { switch = pair[0], port = pair[1] }
  }
}

resource "tplinkeasysmart_port" "free" {
  for_each = local.free_ports
  switch   = each.value.switch
  port     = each.value.port
}

output "models" {
  value = { for name, info in data.tplinkeasysmart_system_info.this : name => "${info.model} ${info.hardware_version}" }
}
