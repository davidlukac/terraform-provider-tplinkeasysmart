---
page_title: "Upgrading to 0.35.0"
subcategory: ""
description: |-
  What changes when you move from 0.34.x to 0.35.0: two new data sources, and a switch that does not answer is asked once, as the 0.34.0 notes said.
---

# Upgrading to 0.35.0

Version 0.35.0 has no breaking change: no attribute of a resource or a data source is renamed, removed or retyped, and no id changes (the reference pages of every resource and every data source that 0.34.0 had are the same). Pin `version = "~> 0.35.0"`, run `terraform init -upgrade` and `terraform plan`.

## New

- The data sources `tplinkeasysmart_led` and `tplinkeasysmart_loop_prevention` read the same two settings as their resources (`enabled`), so a configuration can look at a value before it manages it, or at a switch it does not manage. They only read.

## Behavior you may notice

- **A switch that does not answer is asked once.** The 0.34.0 notes said this, but it was not active in that version: each request to a silent switch cost the full timeout with its retries. It is active from 0.35.0: after the first request to a switch that gets no answer, every other request to that switch in the same run, one that is already waiting for its turn included, fails at once with an error that says it was not sent. What the first request and the refused ones report, and how long the latch lasts, is in "Safety by design" of the [README](https://github.com/davidlukac/terraform-provider-tplinkeasysmart#safety-by-design).
- **The library** is version 0.11.0 (0.34.0 had 0.9.0). The provider checked each behavior change of the library's 0.10.0, 0.10.1 and 0.11.0 against its own code and found none that changes what a configuration or a state does; an interrupted `management_ip` apply can end with another error text. Programs that embed the library read its changelog.

## If something goes wrong

Nothing in this release writes to the switch by itself. To go back, remove the two new data sources from your configuration if you use them, pin `version = "0.34.0"` and run `terraform init -upgrade`.
