# Keep loop prevention enabled on a production network.
resource "tplinkeasysmart_loop_prevention" "this" {
  enabled = true
}
