data "tplinkeasysmart_lags" "this" {}

output "lags" {
  value = data.tplinkeasysmart_lags.this.lags
}
