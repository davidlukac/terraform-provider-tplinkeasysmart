package provider

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"
)

// capturingFactories returns provider factories whose client constructor records
// the configuration the provider derived from HCL and the environment.
func capturingFactories(got *escp.Config, mu *sync.Mutex, fail error) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"tplinkeasysmart": providerserver.NewProtocol6WithError(&tplinkProvider{
			version: "test",
			newClient: func(c escp.Config) (SwitchClient, error) {
				mu.Lock()
				*got = c
				mu.Unlock()
				if fail != nil {
					return nil, fail
				}
				return &fakeClient{}, nil
			},
		}),
	}
}

const infoDataSource = `data "tplinkeasysmart_system_info" "this" {}`

func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"TPLINK_EASYSMART_HOST", "TPLINK_EASYSMART_USERNAME", "TPLINK_EASYSMART_PASSWORD"} {
		t.Setenv(k, "")
	}
}

func TestProviderConfigurationFromTheEnvironment(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("TPLINK_EASYSMART_HOST", "10.1.2.3")
	t.Setenv("TPLINK_EASYSMART_USERNAME", "env-admin")
	t.Setenv("TPLINK_EASYSMART_PASSWORD", "env-secret")
	var got escp.Config
	var mu sync.Mutex
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: capturingFactories(&got, &mu, nil),
		Steps:                    []resource.TestStep{{Config: `provider "tplinkeasysmart" {}` + "\n" + infoDataSource}},
	})
	want := escp.Config{Host: "10.1.2.3", Username: "env-admin", Password: "env-secret", Timeout: 3 * time.Second}
	if got != want {
		t.Fatalf("client configured as %+v, want %+v", got, want)
	}
}

func TestExplicitProviderAttributesOverrideTheEnvironment(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("TPLINK_EASYSMART_HOST", "10.1.2.3")
	t.Setenv("TPLINK_EASYSMART_USERNAME", "env-admin")
	t.Setenv("TPLINK_EASYSMART_PASSWORD", "env-secret")
	var got escp.Config
	var mu sync.Mutex
	cfg := `provider "tplinkeasysmart" {
  host            = "192.0.2.120"
  username        = "admin"
  password        = "hcl-secret"
  timeout_seconds = 7
}
` + infoDataSource
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: capturingFactories(&got, &mu, nil),
		Steps:                    []resource.TestStep{{Config: cfg}},
	})
	want := escp.Config{Host: "192.0.2.120", Username: "admin", Password: "hcl-secret", Timeout: 7 * time.Second}
	if got != want {
		t.Fatalf("client configured as %+v, want %+v", got, want)
	}
}

func TestNonPositiveTimeoutFallsBackToTheDefault(t *testing.T) {
	clearProviderEnv(t)
	for _, v := range []string{"0", "-5"} {
		t.Run(v, func(t *testing.T) {
			var got escp.Config
			var mu sync.Mutex
			cfg := `provider "tplinkeasysmart" {
  host            = "192.0.2.120"
  username        = "admin"
  password        = "x"
  timeout_seconds = ` + v + `
}
` + infoDataSource
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: capturingFactories(&got, &mu, nil),
				Steps:                    []resource.TestStep{{Config: cfg}},
			})
			if got.Timeout != 3*time.Second {
				t.Fatalf("timeout_seconds = %s must fall back to 3s, got %v", v, got.Timeout)
			}
		})
	}
}

func TestProviderRejectsIncompleteConfiguration(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		hcl  string
		want string
	}{
		{"no host", map[string]string{"TPLINK_EASYSMART_USERNAME": "u", "TPLINK_EASYSMART_PASSWORD": "p"},
			`provider "tplinkeasysmart" {}`, `Missing switch host`},
		{"no username", map[string]string{"TPLINK_EASYSMART_HOST": "h", "TPLINK_EASYSMART_PASSWORD": "p"},
			`provider "tplinkeasysmart" {}`, `Missing credentials`},
		{"no password", map[string]string{"TPLINK_EASYSMART_HOST": "h", "TPLINK_EASYSMART_USERNAME": "u"},
			`provider "tplinkeasysmart" {}`, `Missing credentials`},
		{"nothing at all", nil, `provider "tplinkeasysmart" {}`, `Missing switch host`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearProviderEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			var got escp.Config
			var mu sync.Mutex
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: capturingFactories(&got, &mu, nil),
				Steps:                    []resource.TestStep{{Config: tc.hcl + "\n" + infoDataSource, ExpectError: regexp.MustCompile(tc.want)}},
			})
			if got != (escp.Config{}) {
				t.Fatalf("no client may be built from an incomplete configuration, got %+v", got)
			}
		})
	}
}

func TestProviderReportsAClientThatCannotBeBuilt(t *testing.T) {
	clearProviderEnv(t)
	var got escp.Config
	var mu sync.Mutex
	cfg := providerBlock + infoDataSource
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: capturingFactories(&got, &mu, errors.New("resolving host failed")),
		Steps:                    []resource.TestStep{{Config: cfg, ExpectError: regexp.MustCompile(`Cannot configure switch`)}},
	})
}

// During planning a value that comes from another resource is unknown. The
// provider cannot build a client from it; it used to return silently, leaving
// every resource and data source with a nil client, and the first data source
// read crashed the plugin. It must fail with an error that names the attribute.
func TestProviderNamesEveryAttributeThatIsUnknownDuringPlanning(t *testing.T) {
	clearProviderEnv(t)
	for _, attr := range []string{"host", "username", "password"} {
		t.Run(attr, func(t *testing.T) {
			var got escp.Config
			var mu sync.Mutex
			values := map[string]string{"host": `"192.0.2.120"`, "username": `"admin"`, "password": `"x"`}
			values[attr] = "terraform_data.src.output"
			cfg := `resource "terraform_data" "src" {
  input = "from-another-resource"
}

provider "tplinkeasysmart" {
  host     = ` + values["host"] + `
  username = ` + values["username"] + `
  password = ` + values["password"] + `
}
` + infoDataSource
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: capturingFactories(&got, &mu, nil),
				Steps:                    []resource.TestStep{{Config: cfg, ExpectError: regexp.MustCompile(`Unknown switch ` + attr)}},
			})
			if got != (escp.Config{}) {
				t.Fatalf("no client may be built while %s is unknown, got %+v", attr, got)
			}
		})
	}
}

func TestProviderExplainsHowToResolveAnUnknownAttribute(t *testing.T) {
	var diags diag.Diagnostics
	p := &tplinkProvider{newClient: func(escp.Config) (SwitchClient, error) {
		t.Fatal("no client may be built")
		return nil, nil
	}}
	schemaResp := &provider.SchemaResponse{}
	p.Schema(context.Background(), provider.SchemaRequest{}, schemaResp)
	raw := tftypes.NewValue(schemaResp.Schema.Type().TerraformType(context.Background()), map[string]tftypes.Value{
		"host":            tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"username":        tftypes.NewValue(tftypes.String, "admin"),
		"password":        tftypes.NewValue(tftypes.String, "x"),
		"timeout_seconds": tftypes.NewValue(tftypes.Number, nil),
	})
	resp := &provider.ConfigureResponse{}
	p.Configure(context.Background(), provider.ConfigureRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: raw}}, resp)
	diags = resp.Diagnostics
	if len(diags) != 1 {
		t.Fatalf("exactly one diagnostic expected, got %v", diags)
	}
	d := diags[0]
	if d.Severity() != diag.SeverityError || d.Summary() != "Unknown switch host" {
		t.Fatalf("unexpected diagnostic %v", d)
	}
	for _, hint := range []string{"terraform apply -target", "TPLINK_EASYSMART_HOST"} {
		if !strings.Contains(d.Detail(), hint) {
			t.Errorf("the detail must mention %q: %s", hint, d.Detail())
		}
	}
	if resp.ResourceData != nil || resp.DataSourceData != nil {
		t.Fatal("no client data may be handed out")
	}
}

func TestNewBuildsTheProductionProvider(t *testing.T) {
	p := New("9.9.9")()
	var meta provider.MetadataResponse
	p.Metadata(context.Background(), provider.MetadataRequest{}, &meta)
	if meta.TypeName != "tplinkeasysmart" || meta.Version != "9.9.9" {
		t.Fatalf("metadata %+v", meta)
	}
	tp := p.(*tplinkProvider)
	c, err := tp.newClient(escp.Config{Host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	real, ok := c.(*escp.Client)
	if !ok {
		t.Fatalf("production wiring must use the ESCP client, got %T", c)
	}
	real.Close()
	if _, err := tp.newClient(escp.Config{Host: "::1"}); err == nil {
		t.Fatal("an unresolvable host must be reported when the client is built")
	}
	if got := len(p.Resources(context.Background())); got != 10 {
		t.Fatalf("expected 10 resources, got %d", got)
	}
	if got := len(p.DataSources(context.Background())); got != 11 {
		t.Fatalf("expected 11 data sources, got %d", got)
	}
}

func TestClientFromRefusesForeignProviderData(t *testing.T) {
	var diags diag.Diagnostics
	if c := clientFrom(nil, &diags); c != nil || diags.HasError() {
		t.Fatal("no provider data (provider not configured yet) is not an error")
	}
	want := &fakeClient{}
	if c := clientFrom(want, &diags); c != want || diags.HasError() {
		t.Fatalf("a SwitchClient must be passed through, got %v / %v", c, diags)
	}
	if c := clientFrom("not a client", &diags); c != nil {
		t.Fatalf("foreign data must not be accepted, got %v", c)
	}
	if !diags.HasError() || diags[0].Summary() != "Unexpected provider data" {
		t.Fatalf("foreign data must produce a diagnostic, got %v", diags)
	}
}
