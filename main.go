package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"

	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/provider"
)

//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-name tplinkeasysmart --rendered-provider-name "TP-Link Easy Smart"

// version is set by the release build (-ldflags "-X main.version=...").
var version = "0.29.0"

// registryAddress is the provider's source address; Terraform matches it
// against `required_providers` (and `dev_overrides`).
const registryAddress = "registry.terraform.io/davidlukac/tplinkeasysmart"

// serveFunc is providerserver.Serve; it is a parameter of run so tests can
// observe what would be served without starting a plugin server.
type serveFunc func(ctx context.Context, factory func() frameworkprovider.Provider, opts providerserver.ServeOpts) error

// run parses the command line and serves the provider.
func run(ctx context.Context, args []string, output io.Writer, serve serveFunc) error {
	flags := flag.NewFlagSet("terraform-provider-tplinkeasysmart", flag.ContinueOnError)
	flags.SetOutput(output)
	debug := flags.Bool("debug", false, "run the provider with debugger support")
	if err := flags.Parse(args); err != nil {
		return err
	}
	return serve(ctx, provider.New(version), providerserver.ServeOpts{Address: registryAddress, Debug: *debug})
}

// coverage: ignore - process entry point; run holds all the logic and is tested.
func main() {
	if err := run(context.Background(), os.Args[1:], os.Stderr, providerserver.Serve); err != nil {
		log.Fatal(err)
	}
}
