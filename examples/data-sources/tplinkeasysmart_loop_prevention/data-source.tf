data "tplinkeasysmart_loop_prevention" "this" {}

output "loop_prevention_enabled" {
  value = data.tplinkeasysmart_loop_prevention.this.enabled
}
