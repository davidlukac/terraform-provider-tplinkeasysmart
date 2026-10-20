# Untagged traffic entering port 6 belongs to VLAN 20. The VLAN must exist
# (see tplinkeasysmart_vlan_8021q_group, for example by referencing its `vlan`
# attribute) and the switch must be in 802.1Q mode.
resource "tplinkeasysmart_vlan_8021q_pvid" "media_port" {
  port = 6
  pvid = 20
}
