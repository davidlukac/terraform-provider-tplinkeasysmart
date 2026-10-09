# Mirror port 4's received traffic to the free port 7.
resource "tplinkeasysmart_port_mirror" "this" {
  enabled       = true
  mirror_port   = 7
  ingress_ports = [4]

  # Safety: the destination port stops carrying normal traffic. The provider
  # refuses a destination that has a link up or is in a LAG unless overridden.
  # allow_disruptive = true
}
