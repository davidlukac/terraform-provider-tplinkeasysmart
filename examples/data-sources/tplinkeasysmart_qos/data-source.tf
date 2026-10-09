data "tplinkeasysmart_qos" "this" {}

output "qos_mode" {
  value = data.tplinkeasysmart_qos.this.mode_name
}
