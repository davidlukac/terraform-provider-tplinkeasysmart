data "tplinkeasysmart_igmp_snooping" "this" {}

output "multicast_groups" {
  value = { for g in data.tplinkeasysmart_igmp_snooping.this.multicast_groups : g.ip => g.ports }
}
