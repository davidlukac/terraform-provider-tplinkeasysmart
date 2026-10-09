data "tplinkeasysmart_bandwidth_control" "this" {}

output "limits" {
  value = [for p in data.tplinkeasysmart_bandwidth_control.this.ports : "${p.port}: in=${p.ingress_kbps} out=${p.egress_kbps}"]
}
