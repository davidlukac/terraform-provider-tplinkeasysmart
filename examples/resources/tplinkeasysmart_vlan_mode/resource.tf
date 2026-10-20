# Exactly one VLAN mode is active: "mtu", "port-based" or "802.1q". Changing it
# discards the VLAN settings of the other modes and can cut every connection,
# including the management path, so the provider refuses unless it is allowed.
# Applying the mode the switch already has writes nothing.
resource "tplinkeasysmart_vlan_mode" "this" {
  mode = "802.1q"

  # Needed for the change itself; remove it again once the switch is in the mode.
  allow_disruptive = true
}

# The MTU VLAN needs its uplink port: every other port can only talk to it.
# resource "tplinkeasysmart_vlan_mode" "this" {
#   mode            = "mtu"
#   mtu_uplink_port = 8
# }
