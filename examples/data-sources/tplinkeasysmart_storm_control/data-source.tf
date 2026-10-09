data "tplinkeasysmart_storm_control" "this" {}

output "storm_enabled_ports" {
  value = [for p in data.tplinkeasysmart_storm_control.this.ports : p.port if p.enabled]
}
