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
