# Only valid while the switch is in 802.1Q mode (see tplinkeasysmart_vlan_mode).
# A port can be a member of several 802.1Q VLANs, tagged in some and untagged in others.
resource "tplinkeasysmart_vlan_8021q_group" "media" {
  vlan           = 20
  name           = "media"
  tagged_ports   = [5]
  untagged_ports = [6, 7]
}
