# Port 7 is not cabled in this example.
resource "tplinkeasysmart_port" "unused" {
  port         = 7
  speed        = "auto"
  flow_control = false

  # Safety: by default the provider refuses to change a port that has a link up
  # or belongs to a LAG, because that can cut the uplink or the management path.
  # Only set this after checking the port is not needed for connectivity.
  # allow_disruptive = true
}
