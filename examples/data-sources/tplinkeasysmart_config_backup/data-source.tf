# Size and SHA-256 detect configuration changes made outside Terraform.
data "tplinkeasysmart_config_backup" "this" {}

output "config_sha256" {
  value = data.tplinkeasysmart_config_backup.this.sha256
}

# The file itself contains an obfuscated credential block: only request it when needed
# and treat the Terraform state as secret.
# data "tplinkeasysmart_config_backup" "full" {
#   include_content = true
# }
