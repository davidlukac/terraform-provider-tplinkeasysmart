data "tplinkeasysmart_vlan_8021q" "this" {}

output "pvids" {
  value = { for p in data.tplinkeasysmart_vlan_8021q.this.pvids : p.port => p.pvid }
}
