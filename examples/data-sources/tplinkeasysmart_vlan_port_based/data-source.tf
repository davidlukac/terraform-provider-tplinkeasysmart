data "tplinkeasysmart_vlan_port_based" "this" {}

output "port_vlans" {
  value = data.tplinkeasysmart_vlan_port_based.this.vlans
}
