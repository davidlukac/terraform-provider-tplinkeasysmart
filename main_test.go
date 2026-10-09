package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	frameworkprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

// served records what run would hand to the plugin server.
type served struct {
	called  bool
	opts    providerserver.ServeOpts
	factory func() frameworkprovider.Provider
}

func (s *served) serve(_ context.Context, factory func() frameworkprovider.Provider, opts providerserver.ServeOpts) error {
	s.called, s.opts, s.factory = true, opts, factory
	return nil
}

func TestRunServesTheProviderUnderItsRegistryAddress(t *testing.T) {
	var s served
	if err := run(context.Background(), nil, &bytes.Buffer{}, s.serve); err != nil {
		t.Fatal(err)
	}
	if !s.called {
		t.Fatal("the provider must be served")
	}
	if s.opts.Address != "registry.terraform.io/local-infra/tplinkeasysmart" || s.opts.Debug {
		t.Fatalf("unexpected serve options %+v", s.opts)
	}
	p := s.factory()
	var meta frameworkprovider.MetadataResponse
	p.Metadata(context.Background(), frameworkprovider.MetadataRequest{}, &meta)
	if meta.TypeName != "tplinkeasysmart" || meta.Version != version {
		t.Fatalf("the served provider must carry the build version %q, got %+v", version, meta)
	}
}

func TestRunPassesTheDebugFlag(t *testing.T) {
	var s served
	if err := run(context.Background(), []string{"-debug"}, &bytes.Buffer{}, s.serve); err != nil {
		t.Fatal(err)
	}
	if !s.opts.Debug {
		t.Fatal("-debug must enable debugger support")
	}
}

func TestRunRejectsUnknownFlagsWithoutServing(t *testing.T) {
	var s served
	var out bytes.Buffer
	err := run(context.Background(), []string{"-verbose"}, &out, s.serve)
	if err == nil {
		t.Fatal("an unknown flag must be an error")
	}
	if s.called {
		t.Fatal("nothing may be served after a command-line error")
	}
	if !strings.Contains(out.String(), "-debug") {
		t.Fatalf("the usage text must list the supported flags, got %q", out.String())
	}
}

func TestRunReturnsTheServerError(t *testing.T) {
	boom := errors.New("plugin server failed")
	err := run(context.Background(), nil, &bytes.Buffer{}, func(context.Context, func() frameworkprovider.Provider, providerserver.ServeOpts) error {
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
}
