terraform {
  required_providers {
    tplinkeasysmart = {
      # Replace the namespace with the one the provider is published under.
      source = "local-infra/tplinkeasysmart"
    }
  }
}

variable "switch_host" {
  type        = string
  description = "Management IP address of the switch (same layer-2 network as the machine running Terraform)."
}

variable "switch_username" {
  type = string
}

variable "switch_password" {
  type      = string
  sensitive = true
}

provider "tplinkeasysmart" {
  host     = var.switch_host
  username = var.switch_username
  password = var.switch_password

  # Seconds to wait for a reply before retrying a request (default 3).
  # timeout_seconds = 3
}

# Alternatively leave the provider block empty and export
# TPLINK_EASYSMART_HOST, TPLINK_EASYSMART_USERNAME and TPLINK_EASYSMART_PASSWORD.

# Several switches: use the `switches` map and select one per resource with
# `switch = "<name>"` (see examples/multi-switch), or declare one provider
# alias per switch.
