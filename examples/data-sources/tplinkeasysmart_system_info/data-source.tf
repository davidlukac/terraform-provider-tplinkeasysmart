data "tplinkeasysmart_system_info" "this" {}

output "switch" {
  value = {
    model    = data.tplinkeasysmart_system_info.this.model
    firmware = data.tplinkeasysmart_system_info.this.firmware_version
    mac      = data.tplinkeasysmart_system_info.this.mac_address
  }
}
