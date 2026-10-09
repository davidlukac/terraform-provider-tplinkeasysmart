package provider

import "github.com/hashicorp/terraform-plugin-framework/path"

func pathRoot(name string) path.Path { return path.Root(name) }
