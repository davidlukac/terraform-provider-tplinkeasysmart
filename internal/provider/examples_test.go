package provider

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// The documentation examples are what users copy first. These tests keep them honest: every
// resource and data source has one, none is left behind for a type that no longer exists, and
// each one plans against the fake switch (so the plan-time validators and guards accept it, which
// `terraform validate` alone does not prove).

const (
	examplesDir      = "../../examples"
	providerTypeName = "tplinkeasysmart"
)

// exampleKind describes one family of examples under examples/.
type exampleKind struct {
	dir      string   // under examples/
	config   string   // the example configuration
	required []string // files every example of this kind has
}

var (
	resourceExamples   = exampleKind{dir: "resources", config: "resource.tf", required: []string{"resource.tf", "import.sh"}}
	dataSourceExamples = exampleKind{dir: "data-sources", config: "data-source.tf", required: []string{"data-source.tf"}}
)

// registeredTypeNames lists the type names the provider registers, sorted.
func registeredTypeNames(t *testing.T) (resources, dataSources []string) {
	t.Helper()
	ctx := context.Background()
	p := &tplinkProvider{version: "test"}
	for _, newResource := range p.Resources(ctx) {
		var resp resource.MetadataResponse
		newResource().Metadata(ctx, resource.MetadataRequest{ProviderTypeName: providerTypeName}, &resp)
		resources = append(resources, resp.TypeName)
	}
	for _, newDataSource := range p.DataSources(ctx) {
		var resp datasource.MetadataResponse
		newDataSource().Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: providerTypeName}, &resp)
		dataSources = append(dataSources, resp.TypeName)
	}
	sort.Strings(resources)
	sort.Strings(dataSources)
	return resources, dataSources
}

// exampleDirs lists the directory names under examples/<kind>, sorted.
func exampleDirs(t *testing.T, kind exampleKind) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(examplesDir, kind.dir))
	if err != nil {
		t.Fatalf("reading the %s examples: %v", kind.dir, err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func TestEveryTypeHasItsExample(t *testing.T) {
	resources, dataSources := registeredTypeNames(t)
	if len(resources) == 0 || len(dataSources) == 0 {
		t.Fatalf("the provider registers %d resources and %d data sources; the listing is broken", len(resources), len(dataSources))
	}
	for _, c := range []struct {
		kind  exampleKind
		types []string
	}{{resourceExamples, resources}, {dataSourceExamples, dataSources}} {
		for _, name := range c.types {
			for _, file := range c.kind.required {
				path := filepath.Join(examplesDir, c.kind.dir, name, file)
				info, err := os.Stat(path)
				if err != nil {
					t.Errorf("%s has no example file %s: add it (the generated documentation is built from it)", name, path)
					continue
				}
				if info.Size() == 0 {
					t.Errorf("%s is empty", path)
				}
			}
		}
	}
}

func TestNoExampleWithoutAType(t *testing.T) {
	resources, dataSources := registeredTypeNames(t)
	for _, c := range []struct {
		kind  exampleKind
		types []string
	}{{resourceExamples, resources}, {dataSourceExamples, dataSources}} {
		known := map[string]bool{}
		for _, n := range c.types {
			known[n] = true
		}
		for _, dir := range exampleDirs(t, c.kind) {
			if !known[dir] {
				t.Errorf("examples/%s/%s belongs to no registered type: remove it or register the type", c.kind.dir, dir)
			}
		}
	}
}

// TestExamplesPlan runs every resource and data source example through a Terraform plan against
// the fake switch. The examples are meant to be copied to a real configuration, so the provider's
// plan-time checks must accept them as written. The plan of every example is not empty: a
// resource example plans a creation, a data source example plans its outputs.
func TestExamplesPlan(t *testing.T) {
	for _, kind := range []exampleKind{resourceExamples, dataSourceExamples} {
		for _, name := range exampleDirs(t, kind) {
			config, err := os.ReadFile(filepath.Join(examplesDir, kind.dir, name, kind.config))
			if err != nil {
				t.Fatalf("reading the example of %s: %v", name, err)
			}
			t.Run(kind.dir+"/"+name, func(t *testing.T) {
				t.Parallel()
				f := &fakeClient{}
				tfresource.UnitTest(t, tfresource.TestCase{
					ProtoV6ProviderFactories: factories(f),
					Steps: []tfresource.TestStep{{
						Config:             providerBlock + string(config),
						PlanOnly:           true,
						ExpectNonEmptyPlan: true,
					}},
				})
				f.mu.Lock()
				writes := f.sets
				f.mu.Unlock()
				if writes != 0 {
					t.Errorf("planning the example of %s wrote to the switch %d times", name, writes)
				}
			})
		}
	}
}
