package provider

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	pschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

// attrName is the HashiCorp convention for attribute and block names: lower-case words joined by
// underscores.
var attrName = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// schemaAttr is what the three schema packages share about an attribute.
type schemaAttr interface {
	GetDescription() string
	GetMarkdownDescription() string
}

// childrenOf returns the attributes inside a nested attribute, by name.
func childrenOf(a schemaAttr) map[string]schemaAttr {
	out := map[string]schemaAttr{}
	switch n := a.(type) {
	case dschema.ListNestedAttribute:
		for name, child := range n.NestedObject.Attributes {
			out[name] = child
		}
	case rschema.ListNestedAttribute:
		for name, child := range n.NestedObject.Attributes {
			out[name] = child
		}
	case pschema.MapNestedAttribute:
		for name, child := range n.NestedObject.Attributes {
			out[name] = child
		}
	}
	return out
}

// checkAttr applies the conventions to one attribute and to what is inside it.
func checkAttr(t *testing.T, where, name string, a schemaAttr) {
	t.Helper()
	if !attrName.MatchString(name) {
		t.Errorf("%s: attribute %q is not snake_case", where, name)
	}
	if a.GetDescription() == "" && a.GetMarkdownDescription() == "" {
		t.Errorf("%s: attribute %q has no description (the generated documentation would show it blank)", where, name)
	}
	for child, ca := range childrenOf(a) {
		checkAttr(t, where+"."+name, child, ca)
	}
}

// The Registry renders the descriptions and Terraform users read them in their editors: every
// attribute of the provider, every resource and every data source has one, and the names follow the
// naming convention. (dev-go and provider-dev: "every attribute has a Description".)
func TestEverySchemaAttributeHasADescriptionAndASnakeCaseName(t *testing.T) {
	ctx := context.Background()
	p := &tplinkProvider{}

	var ps provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &ps)
	if ps.Schema.Description == "" {
		t.Error("the provider has no description")
	}
	for name, a := range ps.Schema.Attributes {
		checkAttr(t, "provider", name, a)
	}

	for _, newResource := range p.Resources(ctx) {
		r := newResource()
		var meta resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "tplinkeasysmart"}, &meta)
		t.Run(meta.TypeName, func(t *testing.T) {
			var s resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &s)
			if s.Schema.Description == "" {
				t.Error("the resource has no description")
			}
			for name, a := range s.Schema.Attributes {
				checkAttr(t, meta.TypeName, name, a)
			}
		})
	}
	for _, newSource := range p.DataSources(ctx) {
		d := newSource()
		var meta datasource.MetadataResponse
		d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "tplinkeasysmart"}, &meta)
		t.Run(meta.TypeName, func(t *testing.T) {
			var s datasource.SchemaResponse
			d.Schema(ctx, datasource.SchemaRequest{}, &s)
			if s.Schema.Description == "" {
				t.Error("the data source has no description")
			}
			for name, a := range s.Schema.Attributes {
				checkAttr(t, meta.TypeName, name, a)
			}
		})
	}
}

// A port number is `port` and the link state of a port is `actual_speed` everywhere; `id` inside a
// list is the id of the element (a LAG group, a VLAN), never a port. The names freeze at 1.0.
func TestAPortIsNamedPortAndItsLinkStateActualSpeedInEveryDataSource(t *testing.T) {
	ctx := context.Background()
	for _, newSource := range (&tplinkProvider{}).DataSources(ctx) {
		d := newSource()
		var meta datasource.MetadataResponse
		d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "tplinkeasysmart"}, &meta)
		var s datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &s)
		t.Run(meta.TypeName, func(t *testing.T) {
			for name, a := range s.Schema.Attributes {
				for child, ca := range childrenOf(a) {
					if child == "id" && regexp.MustCompile(`(?i)^port number`).MatchString(ca.GetDescription()) {
						t.Errorf("%s.%s.id is a port number: name it `port`", meta.TypeName, name)
					}
					if child == "link" {
						t.Errorf("%s.%s.link: the link state of a port is `actual_speed`", meta.TypeName, name)
					}
				}
			}
		})
	}
}
