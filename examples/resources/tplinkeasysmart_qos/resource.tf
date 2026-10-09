# Per-port priorities only take effect in port-based mode; the provider rejects
# port_priorities in any other mode.
resource "tplinkeasysmart_qos" "this" {
  mode = "dscp-802.1p"
}

# resource "tplinkeasysmart_qos" "port_based" {
#   mode            = "port-based"
#   port_priorities = { "7" = 2 } # 1 (lowest) .. 4 (highest)
# }
