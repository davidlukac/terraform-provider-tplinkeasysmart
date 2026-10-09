data "tplinkeasysmart_ports" "this" {}

output "linked_ports" {
  value = [for p in data.tplinkeasysmart_ports.this.ports : p.id if p.actual_speed != "link-down"]
}
