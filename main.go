package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/provider"
)

//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-name tplinkeasysmart --rendered-provider-name "TP-Link Easy Smart"

// version is set by the release build (-ldflags "-X main.version=...").
var version = "0.21.0"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with debugger support")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/local-infra/tplinkeasysmart",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
