data "tplinkeasysmart_port_mirror" "this" {}

output "mirroring_enabled" {
  value = data.tplinkeasysmart_port_mirror.this.enabled
}
