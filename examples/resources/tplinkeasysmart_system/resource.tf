# The device description: letters, digits and hyphens only (no dots or underscores).
# The management IP, DHCP flag and gateway are re-sent unchanged and never modified here
# (tplinkeasysmart_management_ip manages them).
resource "tplinkeasysmart_system" "this" {
  hostname = "sw01"
}
