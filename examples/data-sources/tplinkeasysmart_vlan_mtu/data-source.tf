data "tplinkeasysmart_vlan_mtu" "this" {}

output "mtu_vlan" {
  value = data.tplinkeasysmart_vlan_mtu.this.enabled
}
