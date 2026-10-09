data "tplinkeasysmart_port_statistics" "this" {}

output "rx_bad" {
  value = { for p in data.tplinkeasysmart_port_statistics.this.ports : p.id => p.rx_bad_packets }
}
