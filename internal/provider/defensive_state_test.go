package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"
)

// State written by an older provider version has no allow_disruptive; Read must
// supply the safe default instead of leaving it null.
func TestPortMirrorReadDefaultsAllowDisruptiveToFalse(t *testing.T) {
	ctx := context.Background()
	r := &portMirrorResource{src: &switchRegistry{def: newFaulty(&fakeClient{ports: testPorts(), mirror: &escp.Mirror{}})}}
	var sch resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sch)
	objType := sch.Schema.Type().TerraformType(ctx).(tftypes.Object)
	raw := tftypes.NewValue(objType, map[string]tftypes.Value{
		"id":               tftypes.NewValue(tftypes.String, "port_mirror"),
		"enabled":          tftypes.NewValue(tftypes.Bool, false),
		"mirror_port":      tftypes.NewValue(tftypes.Number, 0),
		"ingress_ports":    tftypes.NewValue(objType.AttributeTypes["ingress_ports"], []tftypes.Value{}),
		"egress_ports":     tftypes.NewValue(objType.AttributeTypes["egress_ports"], []tftypes.Value{}),
		"allow_disruptive": tftypes.NewValue(tftypes.Bool, nil),
		"switch":           tftypes.NewValue(tftypes.String, nil),
	})
	resp := resource.ReadResponse{State: tfsdk.State{Schema: sch.Schema, Raw: raw}}
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: sch.Schema, Raw: raw}}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var got portMirrorResourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if got.AllowDisruptive.IsNull() || got.AllowDisruptive.ValueBool() {
		t.Fatalf("allow_disruptive must default to false, got %v", got.AllowDisruptive)
	}
}

func TestProviderConfigureReportsUndecodableConfiguration(t *testing.T) {
	p := &tplinkProvider{newClient: func(escp.Config) (SwitchClient, error) {
		t.Fatal("no client may be built")
		return nil, nil
	}}
	schemaResp := &provider.SchemaResponse{}
	p.Schema(context.Background(), provider.SchemaRequest{}, schemaResp)
	resp := &provider.ConfigureResponse{}
	p.Configure(context.Background(), provider.ConfigureRequest{
		Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(tftypes.String, "not an object")},
	}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("a configuration that does not match the schema must be reported")
	}
	if resp.ResourceData != nil || resp.DataSourceData != nil {
		t.Fatal("no client data may be handed out")
	}
}
