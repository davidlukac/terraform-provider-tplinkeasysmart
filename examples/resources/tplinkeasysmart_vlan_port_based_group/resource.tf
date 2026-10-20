# Only valid while the switch is in port-based VLAN mode (see tplinkeasysmart_vlan_mode). Membership is exclusive: the ports leave their previous group
# (VLAN 1) and return to it when this group is destroyed.
resource "tplinkeasysmart_vlan_port_based_group" "lab" {
  vlan  = 2
  ports = [6, 7]
}
