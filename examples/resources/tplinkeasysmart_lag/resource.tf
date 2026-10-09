# A second group from two free ports. Never change the group that carries the
# uplink to the router without a way back to the switch.
resource "tplinkeasysmart_lag" "spare" {
  group = 2
  ports = [6, 7]
}

# Import the existing uplink and protect it.
resource "tplinkeasysmart_lag" "uplink" {
  group = 1
  ports = [1, 2]

  lifecycle {
    prevent_destroy = true
  }
}
