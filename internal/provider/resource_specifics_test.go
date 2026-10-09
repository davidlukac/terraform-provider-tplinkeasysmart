package provider

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/local-infra/terraform-provider-tplinkeasysmart/internal/escp"
)

func TestQoSResourceRejectsInvalidConfiguration(t *testing.T) {
	cases := []struct{ name, cfg, want string }{
		{"unknown mode", qosCfg("  mode = \"turbo\"\n"), `must be one of`},
		{"priority key that is not a port", qosCfg("  mode = \"port-based\"\n  port_priorities = { \"seven\" = 2 }\n"), `Invalid port key`},
		{"priorities outside port-based mode", qosCfg("  mode = \"802.1p\"\n  port_priorities = { \"7\" = 2 }\n"), `only take effect`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			inner := newFakeQoS()
			fc := newFaulty(inner)
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: factories(fc),
				Steps:                    []tfresource.TestStep{{Config: tc.cfg, ExpectError: regexp.MustCompile(tc.want)}},
			})
			if inner.qos.Mode != 2 {
				t.Fatalf("invalid configuration must not change the switch, mode is now %d", inner.qos.Mode)
			}
		})
	}
}

func TestQoSResourceReportsPriorityWritesThatFail(t *testing.T) {
	portBased := qosCfg("  mode = \"port-based\"\n  port_priorities = { \"7\" = 3 }\n")
	t.Run("the priority write is rejected", func(t *testing.T) {
		t.Parallel()
		fc := newFaulty(newFakeQoS())
		fc.failAlways("SetPortPriorities")
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(fc),
			Steps:                    []tfresource.TestStep{{Config: portBased, ExpectError: regexp.MustCompile(`Setting port priorities failed`)}},
		})
	})
	t.Run("the switch accepts the priority write but ignores it", func(t *testing.T) {
		t.Parallel()
		fc := newFaulty(newFakeQoS())
		fc.ignoreOnly("SetPortPriorities")
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(fc),
			Steps:                    []tfresource.TestStep{{Config: portBased, ExpectError: regexp.MustCompile(`Port priority did not change`)}},
		})
	})
}

func TestQoSResourceOnlyManagesTheListedPorts(t *testing.T) {
	inner := newFakeQoS()
	inner.qos.Mode = 0
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: factories(inner),
		Steps: []tfresource.TestStep{
			{
				Config: qosCfg("  mode = \"port-based\"\n  port_priorities = { \"7\" = 4 }\n"),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr("tplinkeasysmart_qos.this", "port_priorities.%", "1"),
					tfresource.TestCheckResourceAttr("tplinkeasysmart_qos.this", "port_priorities.7", "4"),
				),
			},
			{ // a change made behind Terraform's back to an unmanaged port is not drift ...
				PreConfig: func() { inner.mutate(func(f *fakeClient) { f.qos.Priorities[0].Priority = 3 }) },
				Config:    qosCfg("  mode = \"port-based\"\n  port_priorities = { \"7\" = 4 }\n"), PlanOnly: true,
			},
			{ // ... but one to a managed port is
				PreConfig:          func() { inner.mutate(func(f *fakeClient) { f.qos.Priorities[6].Priority = 1 }) },
				Config:             qosCfg("  mode = \"port-based\"\n  port_priorities = { \"7\" = 4 }\n"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestLAGResourceSpecifics(t *testing.T) {
	newFake := func() *fakeClient {
		return &fakeClient{ports: lagPorts(), lags: map[int][]int{1: {1, 2}}}
	}
	t.Run("a LAG needs at least one member", func(t *testing.T) {
		t.Parallel()
		inner := newFake()
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(inner),
			Steps:                    []tfresource.TestStep{{Config: lagConfig(2, "  ports = []\n"), ExpectError: regexp.MustCompile(`at least 1`)}},
		})
		if inner.sets != 0 {
			t.Fatal("nothing may be written")
		}
	})
	t.Run("the group must exist on the switch", func(t *testing.T) {
		t.Parallel()
		inner := newFake()
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(inner),
			Steps:                    []tfresource.TestStep{{Config: lagConfig(9, "  ports = [6]\n"), ExpectError: regexp.MustCompile(`LAG9 does not exist`)}},
		})
	})
	t.Run("reading the current group fails", func(t *testing.T) {
		t.Parallel()
		fc := newFaulty(newFake())
		fc.failAlways("LAGs")
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(fc),
			Steps:                    []tfresource.TestStep{{Config: lagConfig(2, "  ports = [6, 7]\n"), ExpectError: regexp.MustCompile(`Reading LAG failed`)}},
		})
	})
	t.Run("a port that already belongs to another LAG", func(t *testing.T) {
		t.Parallel()
		inner := &fakeClient{lags: map[int][]int{1: {1, 2}},
			ports: append(lagPorts(), escp.Port{ID: 5, Enabled: true, LAG: 3, ConfiguredSpeed: "auto", ActualSpeed: "link-down"})}
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(inner),
			Steps:                    []tfresource.TestStep{{Config: lagConfig(2, "  ports = [5]\n"), ExpectError: regexp.MustCompile(`already belongs to LAG3`)}},
		})
		if inner.sets != 0 {
			t.Fatal("nothing may be written")
		}
	})
	t.Run("a port that does not exist", func(t *testing.T) {
		t.Parallel()
		inner := newFake()
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(inner),
			Steps:                    []tfresource.TestStep{{Config: lagConfig(2, "  ports = [9]\n"), ExpectError: regexp.MustCompile(`port 9 does not exist`)}},
		})
	})
	t.Run("destroying a group whose ports now carry a link is refused", func(t *testing.T) {
		t.Parallel()
		inner := newFake()
		fc := newFaulty(inner)
		cfg := lagConfig(2, "  ports = [6, 7]\n")
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(fc),
			Steps: []tfresource.TestStep{
				{Config: cfg},
				{PreConfig: func() { inner.mutate(func(f *fakeClient) { f.ports[3].ActualSpeed = "1000Mbps-full" }) },
					Config: cfg, Destroy: true, ExpectError: regexp.MustCompile(`Refusing to remove LAG`)},
				{PreConfig: func() { inner.mutate(func(f *fakeClient) { f.ports[3].ActualSpeed = "link-down" }) }, Config: cfg}, // cleanup destroys
			},
		})
		if len(inner.lags[2]) != 0 {
			t.Fatalf("the group must be gone after the final destroy, got %v", inner.lags[2])
		}
	})
	t.Run("destroying fails when the group cannot be read", func(t *testing.T) {
		t.Parallel()
		fc := newFaulty(newFake())
		cfg := lagConfig(2, "  ports = [6, 7]\n")
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(fc),
			Steps: []tfresource.TestStep{
				{Config: cfg},
				{PreConfig: func() { fc.heal(); fc.failCalls("LAGs", 2, 2) }, Config: cfg, Destroy: true, ExpectError: regexp.MustCompile(`Reading LAG failed`)},
				{PreConfig: fc.heal, Config: cfg},
			},
		})
	})
	t.Run("destroying fails when the removal is rejected", func(t *testing.T) {
		t.Parallel()
		fc := newFaulty(newFake())
		cfg := lagConfig(2, "  ports = [6, 7]\n")
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(fc),
			Steps: []tfresource.TestStep{
				{Config: cfg},
				{PreConfig: func() { fc.failAlways("SetLAG") }, Config: cfg, Destroy: true, ExpectError: regexp.MustCompile(`Removing LAG failed`)},
				{PreConfig: fc.heal, Config: cfg},
			},
		})
	})
}

func TestVLANGroupResourceSpecifics(t *testing.T) {
	newFake := func() *fakeClient {
		return &fakeClient{ports: lagPorts(), pvlans: map[int][]int{1: {1, 2, 4, 6, 7}}}
	}
	t.Run("a group needs at least one member", func(t *testing.T) {
		t.Parallel()
		inner := newFake()
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(inner),
			Steps:                    []tfresource.TestStep{{Config: vlanGroupConfig(2, "  ports = []\n"), ExpectError: regexp.MustCompile(`at least 1`)}},
		})
		if inner.sets != 0 {
			t.Fatal("nothing may be written")
		}
	})
	t.Run("groups are only managed while port-based mode is active", func(t *testing.T) {
		t.Parallel()
		inner := newFake()
		inner.pvlanOff = true
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(inner),
			Steps:                    []tfresource.TestStep{{Config: vlanGroupConfig(2, "  ports = [6, 7]\n"), ExpectError: regexp.MustCompile(`mode is not active`)}},
		})
		if inner.sets != 0 {
			t.Fatal("nothing may be written while another VLAN mode is active")
		}
	})
	t.Run("reading the current groups fails", func(t *testing.T) {
		t.Parallel()
		fc := newFaulty(newFake())
		fc.failAlways("PortBasedVLAN")
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(fc),
			Steps:                    []tfresource.TestStep{{Config: vlanGroupConfig(2, "  ports = [6, 7]\n"), ExpectError: regexp.MustCompile(`Reading VLAN group failed`)}},
		})
	})
	t.Run("a port in a LAG cannot be moved", func(t *testing.T) {
		t.Parallel()
		inner := &fakeClient{pvlans: map[int][]int{1: {1, 2, 4, 5, 6, 7}},
			ports: append(lagPorts(), escp.Port{ID: 5, Enabled: true, LAG: 2, ConfiguredSpeed: "auto", ActualSpeed: "link-down"})}
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(inner),
			Steps:                    []tfresource.TestStep{{Config: vlanGroupConfig(2, "  ports = [5]\n"), ExpectError: regexp.MustCompile(`member of LAG2`)}},
		})
		if inner.sets != 0 {
			t.Fatal("nothing may be written")
		}
	})
	t.Run("a port that does not exist", func(t *testing.T) {
		t.Parallel()
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(newFake()),
			Steps:                    []tfresource.TestStep{{Config: vlanGroupConfig(2, "  ports = [9]\n"), ExpectError: regexp.MustCompile(`port 9 does not exist`)}},
		})
	})
	t.Run("destroying a group whose ports now carry a link is refused", func(t *testing.T) {
		t.Parallel()
		inner := newFake()
		cfg := vlanGroupConfig(2, "  ports = [6, 7]\n")
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(inner),
			Steps: []tfresource.TestStep{
				{Config: cfg},
				{PreConfig: func() { inner.mutate(func(f *fakeClient) { f.ports[3].ActualSpeed = "1000Mbps-full" }) },
					Config: cfg, Destroy: true, ExpectError: regexp.MustCompile(`Refusing to remove VLAN group`)},
				{PreConfig: func() { inner.mutate(func(f *fakeClient) { f.ports[3].ActualSpeed = "link-down" }) }, Config: cfg},
			},
		})
		if _, still := inner.pvlans[2]; still {
			t.Fatal("the group must be gone after the final destroy")
		}
	})
	t.Run("destroying fails when the groups cannot be read", func(t *testing.T) {
		t.Parallel()
		fc := newFaulty(newFake())
		cfg := vlanGroupConfig(2, "  ports = [6, 7]\n")
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(fc),
			Steps: []tfresource.TestStep{
				{Config: cfg},
				{PreConfig: func() { fc.heal(); fc.failCalls("PortBasedVLAN", 2, 2) }, Config: cfg, Destroy: true, ExpectError: regexp.MustCompile(`Reading VLAN group failed`)},
				{PreConfig: fc.heal, Config: cfg},
			},
		})
	})
	t.Run("destroying fails when the removal is rejected", func(t *testing.T) {
		t.Parallel()
		fc := newFaulty(newFake())
		cfg := vlanGroupConfig(2, "  ports = [6, 7]\n")
		tfresource.UnitTest(t, tfresource.TestCase{
			ProtoV6ProviderFactories: factories(fc),
			Steps: []tfresource.TestStep{
				{Config: cfg},
				{PreConfig: func() { fc.failAlways("SetPortBasedVLAN") }, Config: cfg, Destroy: true, ExpectError: regexp.MustCompile(`Removing VLAN group failed`)},
				{PreConfig: fc.heal, Config: cfg},
			},
		})
	})
}

// The framework hands CRUD methods a plan or state decoded from the schema. If
// that ever fails (a schema/model mismatch introduced by a refactoring) the
// resource must report it and must not touch the switch.
func TestResourcesReportUndecodablePlanAndState(t *testing.T) {
	ctors := map[string]func() resource.Resource{
		"led": NewLEDResource, "igmp snooping": NewIGMPSnoopingResource, "loop prevention": NewLoopPreventionResource,
		"port": NewPortResource, "qos": NewQoSResource, "bandwidth limit": NewBandwidthLimitResource,
		"storm limit": NewStormLimitResource, "port mirror": NewPortMirrorResource, "lag": NewLAGResource,
		"vlan group": NewVLANPortBasedGroupResource,
	}
	// resources whose Read/Delete decode the prior state (the singletons that only
	// re-read the switch ignore it)
	readsState := map[string]bool{"led": true, "port": true, "qos": true, "bandwidth limit": true, "storm limit": true,
		"port mirror": true, "lag": true, "vlan group": true}
	deletesState := map[string]bool{"lag": true, "vlan group": true}
	ctx := context.Background()
	for name, ctor := range ctors {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fc := newFaulty(&fakeClient{})
			r := ctor()
			r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: fc}, &resource.ConfigureResponse{})
			var sch resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &sch)
			objType := sch.Schema.Type().TerraformType(ctx)
			garbage := tftypes.NewValue(tftypes.String, "not an object")
			emptyState := func() tfsdk.State { return tfsdk.State{Schema: sch.Schema, Raw: tftypes.NewValue(objType, nil)} }

			create := resource.CreateResponse{State: emptyState()}
			r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: sch.Schema, Raw: garbage}}, &create)
			if !create.Diagnostics.HasError() {
				t.Error("Create must reject an undecodable plan")
			}
			update := resource.UpdateResponse{State: emptyState()}
			r.Update(ctx, resource.UpdateRequest{Plan: tfsdk.Plan{Schema: sch.Schema, Raw: garbage}}, &update)
			if !update.Diagnostics.HasError() {
				t.Error("Update must reject an undecodable plan")
			}
			if readsState[name] {
				read := resource.ReadResponse{State: emptyState()}
				r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: sch.Schema, Raw: garbage}}, &read)
				if !read.Diagnostics.HasError() {
					t.Error("Read must reject an undecodable state")
				}
			}
			if deletesState[name] {
				del := resource.DeleteResponse{}
				r.Delete(ctx, resource.DeleteRequest{State: tfsdk.State{Schema: sch.Schema, Raw: garbage}}, &del)
				if !del.Diagnostics.HasError() {
					t.Error("Delete must reject an undecodable state")
				}
			}
			if n := fc.totalCalls(); n != 0 {
				t.Errorf("an undecodable request must not reach the switch, %d call(s) did", n)
			}
		})
	}
}

func TestSetToIntsRejectsUnknownSets(t *testing.T) {
	if _, err := setToInts(context.Background(), types.SetUnknown(types.Int64Type)); err == nil {
		t.Fatal("an unknown set cannot be converted and must be reported")
	}
	got, err := setToInts(context.Background(), intsToSet([]int{7, 1, 4}))
	if err != nil || len(got) != 3 || got[0] != 1 || got[1] != 4 || got[2] != 7 {
		t.Fatalf("a known set is returned sorted, got %v, %v", got, err)
	}
}

func TestPortMirrorRejectsAnUnknownPortSet(t *testing.T) {
	fc := newFaulty(&fakeClient{ports: testPorts(), mirror: &escp.Mirror{}})
	r := &portMirrorResource{client: fc}
	m := portMirrorResourceModel{Enabled: types.BoolValue(true), MirrorPort: types.Int64Value(7),
		IngressPorts: types.SetUnknown(types.Int64Type), EgressPorts: emptyPortSet(), AllowDisruptive: types.BoolValue(false)}
	var summary, detail string
	ok := r.apply(context.Background(), &m, func(s, d string) { summary, detail = s, d })
	if ok || summary != "Invalid port set" || !strings.Contains(detail, "unknown") {
		t.Fatalf("got ok=%v %q %q", ok, summary, detail)
	}
	if fc.totalCalls() != 0 {
		t.Fatal("the switch must not be contacted")
	}
}
