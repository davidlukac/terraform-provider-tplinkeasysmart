# Only valid while the switch is in 802.1Q mode (see tplinkeasysmart_vlan_mode), so the
# VLAN waits for the mode resource: Terraform cannot see that dependency by itself.
resource "tplinkeasysmart_vlan_mode" "this" {
  mode             = "802.1q"
  allow_disruptive = true # needed for the change itself; remove it once the switch is in the mode
}

# A port can be a member of several 802.1Q VLANs, tagged in some and untagged in others.
resource "tplinkeasysmart_vlan_8021q_group" "media" {
  vlan           = 20
  name           = "media"
  tagged_ports   = [5]
  untagged_ports = [6, 7]

  depends_on = [tplinkeasysmart_vlan_mode.this]
}
