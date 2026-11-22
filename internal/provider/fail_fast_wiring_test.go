package provider

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/davidlukac/escp-go"
	"github.com/davidlukac/escp-go/escptest"
)

// These tests build the clients the way the provider does, through registryBuilder.add, and talk
// to real escp clients over UDP to fake switches: they prove that the fail-fast latch (see
// fail_fast.go) is part of what a run uses, which the tests of the latch itself, with a client they
// wrap by hand, cannot (DC-P06).

const latchedErrorText = "did not answer earlier in this run"

// wiringSwitches maps the hosts the tests give to the switch specs to the fake switch behind each.
type wiringSwitches map[string]*escptest.Switch

// build runs the production path for each spec: registryBuilder.add with a constructor that dials a
// real client (see dialFake) at the fake that stands for the host.
func (w wiringSwitches) build(t *testing.T, specs ...switchSpec) *switchRegistry {
	t.Helper()
	var diags diag.Diagnostics
	b := &registryBuilder{
		reg:   &switchRegistry{named: map[string]SwitchClient{}},
		diags: &diags,
		seen:  map[string]string{},
		newClient: func(cfg escp.Config) (SwitchClient, error) {
			return dialFake(t, w[cfg.Host], cfg)
		},
	}
	for _, s := range specs {
		s.username, s.password = escptest.Username, escptest.Password
		b.add(s)
	}
	if diags.HasError() {
		t.Fatalf("the clients were not built: %v", diags)
	}
	t.Cleanup(func() { _ = b.reg.closeAll() })
	return b.reg
}

func clientOf(t *testing.T, reg *switchRegistry, name string) SwitchClient {
	t.Helper()
	c, err := reg.Client(switchName(name))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A silent switch is asked once: the first request takes the timeout, every later request of every
// kind (the configuration read included) fails at once with the documented error and sends
// nothing, for the provider's own switch and for a switch of the `switches` map alike.
func TestTheRegistryLatchesASilentSwitch(t *testing.T) {
	failIfStuck(t, hangBound)
	for _, tt := range []struct {
		name  string
		spec  switchSpec
		which string
	}{
		{"the provider's own switch", switchSpec{host: "192.0.2.1"}, defaultSwitchName},
		{"a switch of the map", switchSpec{name: "office", host: "192.0.2.1"}, "office"},
		{"the provider's own switch, any firmware", switchSpec{host: "192.0.2.1", allowUnverified: true}, defaultSwitchName},
		{"a switch of the map, any firmware", switchSpec{name: "office", host: "192.0.2.1", allowUnverified: true}, "office"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sw := escptest.Start(t)
			sw.Mute()
			c := clientOf(t, wiringSwitches{"192.0.2.1": sw}.build(t, tt.spec), tt.which)
			if _, err := c.Info(context.Background()); !errors.Is(err, escp.ErrNoReply) || strings.Contains(err.Error(), latchedErrorText) {
				t.Fatalf("the first request: got %v, want the timeout itself", err)
			}
			sent := sw.Received()
			if sent == 0 {
				t.Fatal("the first request never reached the switch: this proves nothing")
			}
			for name, err := range callEveryRequest(t, c) {
				if err == nil || !strings.Contains(err.Error(), latchedErrorText) || !errors.Is(err, escp.ErrNoReply) {
					t.Errorf("%s: got %v, want the latched error", name, err)
				}
			}
			if got := sw.Received(); got != sent {
				t.Fatalf("%d datagram(s) went to a switch that did not answer", got-sent)
			}
		})
	}
}

// A switch that answered is never latched: a request it does not answer afterwards is asked again,
// and the next one too.
func TestTheRegistryNeverLatchesASwitchThatAnswered(t *testing.T) {
	failIfStuck(t, hangBound)
	sw := escptest.Start(t)
	c := clientOf(t, wiringSwitches{"192.0.2.1": sw}.build(t, switchSpec{host: "192.0.2.1"}), defaultSwitchName)
	if _, err := c.LED(context.Background()); err != nil {
		t.Fatalf("the switch answers: %v", err)
	}
	sw.Mute()
	for i := range 2 {
		before := sw.Received()
		if _, err := c.LED(context.Background()); !errors.Is(err, escp.ErrNoReply) || strings.Contains(err.Error(), latchedErrorText) {
			t.Fatalf("request %d after the switch went silent: got %v, want the timeout itself", i, err)
		}
		if sw.Received() == before {
			t.Fatalf("request %d was not sent: a switch that answered once must not be latched", i)
		}
	}
}

// One latch per switch: a silent switch does not latch a responsive one, and the responsive one
// keeps answering while the silent one is not asked again.
func TestASilentSwitchDoesNotLatchAResponsiveOne(t *testing.T) {
	failIfStuck(t, hangBound)
	silent, live := escptest.Start(t), escptest.Start(t)
	silent.Mute()
	reg := wiringSwitches{"192.0.2.1": silent, "192.0.2.2": live}.build(t,
		switchSpec{name: "silent", host: "192.0.2.1"}, switchSpec{name: "live", host: "192.0.2.2"})
	if _, err := clientOf(t, reg, "silent").LED(context.Background()); !errors.Is(err, escp.ErrNoReply) {
		t.Fatalf("the silent switch: got %v, want ErrNoReply", err)
	}
	sent := silent.Received()
	if _, err := clientOf(t, reg, "silent").LED(context.Background()); err == nil || !strings.Contains(err.Error(), latchedErrorText) {
		t.Fatalf("the silent switch, asked again: got %v, want the latched error", err)
	}
	for i := range 2 {
		if _, err := clientOf(t, reg, "live").LED(context.Background()); err != nil {
			t.Fatalf("request %d to the responsive switch: %v", i, err)
		}
	}
	if got := silent.Received(); got != sent {
		t.Fatalf("%d datagram(s) went to the switch that did not answer", got-sent)
	}
}

// The firmware read of the configuration gate goes through the latch: it counts as an answer, so a
// switch that told its firmware is not latched by what it leaves unanswered afterwards (the gate
// outside the latch, in the other order, would never be seen by it).
func TestTheFirmwareReadOfTheBackupGateCountsAsAnAnswer(t *testing.T) {
	failIfStuck(t, hangBound)
	sw := escptest.Start(t)
	sw.SetFirmware(unverifiedFirmware)
	c := clientOf(t, wiringSwitches{"192.0.2.1": sw}.build(t, switchSpec{host: "192.0.2.1"}), defaultSwitchName)
	if _, err := c.ConfigBackup(context.Background()); err == nil || !strings.Contains(err.Error(), "refusing to read the configuration file") {
		t.Fatalf("the gate must refuse the configuration read on this firmware: %v", err)
	}
	if sets := len(sw.AllSets()); sets != 0 {
		t.Fatalf("the refused configuration read sent %d write-like request(s)", sets)
	}
	sw.Mute()
	before := sw.Received()
	if _, err := c.LED(context.Background()); !errors.Is(err, escp.ErrNoReply) {
		t.Fatalf("got %v, want the timeout itself", err)
	}
	if _, err := c.LED(context.Background()); !errors.Is(err, escp.ErrNoReply) || strings.Contains(err.Error(), latchedErrorText) {
		t.Fatalf("the second request: got %v, want it asked again and the timeout itself", err)
	}
	if got := sw.Received() - before; got < 2 {
		t.Fatalf("only %d datagram(s) after the switch went silent: the firmware answer must keep it from being latched", got)
	}
}

// A latched switch refuses the configuration read before anything is sent, by the gate's wording
// (it could not read the firmware) and with the latched error underneath.
func TestALatchedSwitchRefusesTheConfigurationReadAtTheGate(t *testing.T) {
	failIfStuck(t, hangBound)
	sw := escptest.Start(t)
	sw.Mute()
	c := clientOf(t, wiringSwitches{"192.0.2.1": sw}.build(t, switchSpec{host: "192.0.2.1"}), defaultSwitchName)
	_, _ = c.Info(context.Background())
	sent := sw.Received()
	_, err := c.ConfigBackup(context.Background())
	if err == nil || !strings.Contains(err.Error(), "refusing to read the configuration file") || !strings.Contains(err.Error(), latchedErrorText) {
		t.Fatalf("got %v, want the gate's refusal with the latched error", err)
	}
	if got := sw.Received(); got != sent {
		t.Fatalf("%d datagram(s) went to a switch that did not answer", got-sent)
	}
}

// configureRegistry runs the provider's Configure with the provider's own switch at ownHost (none
// when it is empty) and one entry of the `switches` map per name, and returns the registry it hands
// to the resources and data sources. dial builds the client of each host.
func configureRegistry(t *testing.T, dial func(escp.Config) (SwitchClient, error), ownHost string, entries map[string]string) *switchRegistry {
	t.Helper()
	clearProviderEnv(t)
	ctx := context.Background()
	p := &tplinkProvider{version: "test", newClient: dial}
	schemaResp := &provider.SchemaResponse{}
	p.Schema(ctx, provider.SchemaRequest{}, schemaResp)
	objType := mustAs[tftypes.Object](t, schemaResp.Schema.Type().TerraformType(ctx))
	entryType := mustAs[tftypes.Map](t, objType.AttributeTypes["switches"]).ElementType
	str := func(v any) tftypes.Value { return tftypes.NewValue(tftypes.String, v) }
	var host any
	if ownHost != "" {
		host = ownHost
	}
	switches := map[string]tftypes.Value{}
	for name, h := range entries {
		attrs := map[string]tftypes.Value{}
		for attrName, attrType := range mustAs[tftypes.Object](t, entryType).AttributeTypes {
			attrs[attrName] = tftypes.NewValue(attrType, nil)
		}
		attrs["host"], attrs["username"], attrs["password"] = str(h), str(escptest.Username), str(escptest.Password)
		switches[name] = tftypes.NewValue(entryType, attrs)
	}
	var switchesValue tftypes.Value
	if len(entries) == 0 {
		switchesValue = tftypes.NewValue(objType.AttributeTypes["switches"], nil)
	} else {
		switchesValue = tftypes.NewValue(objType.AttributeTypes["switches"], switches)
	}
	raw := tftypes.NewValue(objType, map[string]tftypes.Value{
		"host": str(host), "mac": str(nil), "username": str(escptest.Username), "password": str(escptest.Password),
		"timeout_seconds":           tftypes.NewValue(tftypes.Number, nil),
		"allow_unverified_firmware": tftypes.NewValue(tftypes.Bool, nil),
		"relocate_by_mac":           tftypes.NewValue(tftypes.Bool, nil),
		"switches":                  switchesValue,
	})
	resp := &provider.ConfigureResponse{}
	p.Configure(ctx, provider.ConfigureRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: raw}}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure failed: %v", resp.Diagnostics)
	}
	reg := mustAs[*switchRegistry](t, resp.ResourceData)
	t.Cleanup(func() { _ = reg.closeAll() })
	return reg
}

// A run against a switch that is off costs one request, not one per resource: through Configure,
// the first request to a silent switch takes the timeout and the next ones, of any kind, are
// refused at once, for the provider's own switch and for each switch of the map, each with a latch
// of its own.
func TestConfigureLatchesEachSilentSwitchOnItsOwn(t *testing.T) {
	failIfStuck(t, hangBound)
	own, a, b := escptest.Start(t), escptest.Start(t), escptest.Start(t)
	own.Mute()
	a.Mute()
	fakes := map[string]*escptest.Switch{"192.0.2.1": own, "192.0.2.2": a, "192.0.2.3": b}
	reg := configureRegistry(t, func(cfg escp.Config) (SwitchClient, error) { return dialFake(t, fakes[cfg.Host], cfg) },
		"192.0.2.1", map[string]string{"silent": "192.0.2.2", "live": "192.0.2.3"})
	for _, tt := range []struct {
		name   string
		sw     *escptest.Switch
		silent bool
	}{{defaultSwitchName, own, true}, {"silent", a, true}, {"live", b, false}} {
		t.Run(tt.name, func(t *testing.T) {
			c := clientOf(t, reg, tt.name)
			for i := range 3 {
				before := tt.sw.Received()
				_, err := c.LED(context.Background())
				switch {
				case !tt.silent && err != nil:
					t.Fatalf("request %d: the responsive switch must answer: %v", i, err)
				case tt.silent && i == 0 && (!errors.Is(err, escp.ErrNoReply) || strings.Contains(err.Error(), latchedErrorText)):
					t.Fatalf("the first request: got %v, want the timeout itself", err)
				case tt.silent && i > 0 && (err == nil || !strings.Contains(err.Error(), latchedErrorText) || tt.sw.Received() != before):
					t.Fatalf("request %d: got %v after %d new datagram(s), want the latched error and nothing sent", i, err, tt.sw.Received()-before)
				}
			}
		})
	}
}

// dialFake dials a real client at the fake with the short settings of these tests.
func dialFake(t *testing.T, sw *escptest.Switch, cfg escp.Config) (SwitchClient, error) {
	t.Helper()
	cfg.SwitchPort = sw.UDPPort()
	cfg.HostPort = escptest.FreeUDPPort(t)
	cfg.BroadcastAddr = escptest.Loopback()
	cfg.Host = "127.0.0.1"
	cfg.Retries = escp.NoRetries
	cfg.Timeout = 60 * time.Millisecond
	c, err := escp.Dial(cfg)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { _ = c.Close() })
	return c, nil
}
