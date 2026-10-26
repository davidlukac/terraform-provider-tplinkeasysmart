# The static management address of the switch. Changing it moves the switch, which
# can cut the management path, so the provider refuses unless it is allowed.
# Applying the address the switch already has writes nothing.
resource "tplinkeasysmart_management_ip" "this" {
  ip_address = "192.0.2.13"
  netmask    = "255.255.255.0"
  gateway    = "192.0.2.1"

  # Needed for the change itself; remove it again once the switch is at its new address.
  allow_disruptive = true
}

# Or lease the address from the DHCP server instead. The provider needs the switch's `mac`
# (provider block or `switches` entry) to find it at the address it is given; the three
# address attributes then report the lease and must not be set.
#
# resource "tplinkeasysmart_management_ip" "this" {
#   dhcp             = true
#   allow_disruptive = true
# }
