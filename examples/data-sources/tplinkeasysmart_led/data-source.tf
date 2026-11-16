data "tplinkeasysmart_led" "this" {}

output "leds_on" {
  value = data.tplinkeasysmart_led.this.enabled
}
