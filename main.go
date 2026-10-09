package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/provider"
)

// version is set by the release build (-ldflags "-X main.version=...").
var version = "0.16.0"

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
