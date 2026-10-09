resource "tplinkeasysmart_bandwidth_limit" "unused" {
  port         = 7
  ingress_kbps = 10240 # multiple of 64, 0 = unlimited
  egress_kbps  = 0

  # Safety: by default the provider refuses to change a port that has a link up
  # or belongs to a LAG, because that can cut the uplink or the management path.
  # Only set this after checking the port is not needed for connectivity.
  # allow_disruptive = true
}
