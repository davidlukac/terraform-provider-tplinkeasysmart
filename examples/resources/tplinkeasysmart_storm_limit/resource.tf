resource "tplinkeasysmart_storm_limit" "unused" {
  port      = 7
  broadcast = true
  multicast = true
  rate_kbps = 1024 # multiple of 64; required when a traffic type is selected

  # Safety: by default the provider refuses to change a port that has a link up
  # or belongs to a LAG, because that can cut the uplink or the management path.
  # Only set this after checking the port is not needed for connectivity.
  # allow_disruptive = true
}
