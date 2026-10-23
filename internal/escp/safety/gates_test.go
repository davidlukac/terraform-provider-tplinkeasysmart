package safety

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

var errFirst, errSecond = errors.New("first gate"), errors.New("second gate")

func allow(calls *[]string, name string) escp.WriteGate {
	return func(context.Context, escp.InfoReader, escp.Operation) error {
		*calls = append(*calls, name)
		return nil
	}
}

func refuse(calls *[]string, name string, err error) escp.WriteGate {
	return func(context.Context, escp.InfoReader, escp.Operation) error {
		*calls = append(*calls, name)
		return err
	}
}

func TestChain(t *testing.T) {
	ctx, op := context.Background(), escp.Operation{Name: "SetLED", Impact: escp.ImpactCosmetic}
	t.Run("nothing, or only nil gates, gates nothing", func(t *testing.T) {
		if Chain() != nil || Chain(nil, nil) != nil {
			t.Fatal("a chain of no gates must be nil, which the client reads as no gate")
		}
	})
	t.Run("one gate is that gate", func(t *testing.T) {
		var calls []string
		g := Chain(nil, refuse(&calls, "a", errFirst), nil)
		if err := g(ctx, nil, op); !errors.Is(err, errFirst) || !reflect.DeepEqual(calls, []string{"a"}) {
			t.Fatalf("err=%v calls=%v", err, calls)
		}
	})
	t.Run("all allow: all are asked, in order", func(t *testing.T) {
		var calls []string
		g := Chain(allow(&calls, "a"), nil, allow(&calls, "b"), allow(&calls, "c"))
		if err := g(ctx, nil, op); err != nil || !reflect.DeepEqual(calls, []string{"a", "b", "c"}) {
			t.Fatalf("err=%v calls=%v", err, calls)
		}
	})
	t.Run("the first refusal wins and the later gates are not asked", func(t *testing.T) {
		var calls []string
		g := Chain(allow(&calls, "a"), refuse(&calls, "b", errFirst), refuse(&calls, "c", errSecond))
		if err := g(ctx, nil, op); !errors.Is(err, errFirst) || !reflect.DeepEqual(calls, []string{"a", "b"}) {
			t.Fatalf("err=%v calls=%v", err, calls)
		}
	})
	t.Run("the operation reaches every gate unchanged", func(t *testing.T) {
		var seen []escp.Operation
		spy := func(_ context.Context, _ escp.InfoReader, o escp.Operation) error { seen = append(seen, o); return nil }
		want := escp.Operation{Name: "SetLAG", Ports: []int{5, 6}, Impact: escp.ImpactManagement}
		if err := Chain(spy, spy)(ctx, nil, want); err != nil || len(seen) != 2 || !reflect.DeepEqual(seen[0], want) || !reflect.DeepEqual(seen[1], want) {
			t.Fatalf("err=%v seen=%+v", err, seen)
		}
	})
}

func TestMaxImpact(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		max    escp.Impact
		impact escp.Impact
		refuse bool
	}{
		{"cosmetic under cosmetic", escp.ImpactCosmetic, escp.ImpactCosmetic, false},
		{"traffic over cosmetic", escp.ImpactCosmetic, escp.ImpactTraffic, true},
		{"management over cosmetic", escp.ImpactCosmetic, escp.ImpactManagement, true},
		{"cosmetic under traffic", escp.ImpactTraffic, escp.ImpactCosmetic, false},
		{"traffic under traffic", escp.ImpactTraffic, escp.ImpactTraffic, false},
		{"management over traffic", escp.ImpactTraffic, escp.ImpactManagement, true},
		{"management under management", escp.ImpactManagement, escp.ImpactManagement, false},
		{"an operation that declares no impact is refused", escp.ImpactManagement, 0, true},
		{"an operation with an impossible impact is refused", escp.ImpactManagement, escp.Impact(9), true},
		{"a max that is no class refuses everything", 0, escp.ImpactCosmetic, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			op := escp.Operation{Name: "SetX", Impact: tc.impact}
			err := MaxImpact(tc.max)(ctx, nil, op)
			var got *ImpactError
			switch {
			case !tc.refuse && err != nil:
				t.Fatalf("want it allowed, got %v", err)
			case tc.refuse && !errors.As(err, &got):
				t.Fatalf("want an *ImpactError, got %v", err)
			case tc.refuse && (got.Op.Name != "SetX" || got.Max != tc.max):
				t.Fatalf("the refusal must carry the operation and the limit: %+v", got)
			}
		})
	}
	t.Run("message", func(t *testing.T) {
		err := MaxImpact(escp.ImpactTraffic)(ctx, nil, escp.Operation{Name: "SetLAG", Impact: escp.ImpactManagement})
		if want := "SetLAG has impact management, above the allowed traffic"; err == nil || err.Error() != want {
			t.Fatalf("got %v, want %q", err, want)
		}
	})
}

func TestDryRun(t *testing.T) {
	ctx := context.Background()
	d := NewDryRun()
	if len(d.Operations()) != 0 {
		t.Fatal("a new dry run has recorded nothing")
	}
	gate := d.Gate()
	a := escp.Operation{Name: "SetPort", Ports: []int{3}, Impact: escp.ImpactTraffic}
	b := escp.Operation{Name: "SetLED", Impact: escp.ImpactCosmetic}
	for _, op := range []escp.Operation{a, b} {
		if err := gate(ctx, nil, op); !errors.Is(err, ErrDryRun) {
			t.Fatalf("a dry run refuses every write with ErrDryRun, got %v", err)
		}
	}
	got := d.Operations()
	if !reflect.DeepEqual(got, []escp.Operation{a, b}) {
		t.Fatalf("recorded %+v, want %+v in order", got, []escp.Operation{a, b})
	}
	// what was handed in and what is handed out are copies
	a.Ports[0] = 99
	got[0].Ports[0] = 98
	if again := d.Operations(); again[0].Ports[0] != 3 {
		t.Fatalf("the record changed through a slice it does not own: %+v", again[0])
	}
}

// Gates are composed as the front end wishes; a refusal by an earlier gate keeps the dry run empty.
func TestDryRunRecordsOnlyWhatTheEarlierGatesAllowed(t *testing.T) {
	d := NewDryRun()
	gate := Chain(MaxImpact(escp.ImpactTraffic), d.Gate())
	ctx := context.Background()
	var tooMuch *ImpactError
	if err := gate(ctx, nil, escp.Operation{Name: "SetLAG", Impact: escp.ImpactManagement}); !errors.As(err, &tooMuch) {
		t.Fatalf("got %v", err)
	}
	if err := gate(ctx, nil, escp.Operation{Name: "SetPort", Impact: escp.ImpactTraffic}); !errors.Is(err, ErrDryRun) {
		t.Fatalf("got %v", err)
	}
	if ops := d.Operations(); len(ops) != 1 || ops[0].Name != "SetPort" {
		t.Fatalf("recorded %+v, want only SetPort", ops)
	}
}

// The firmware gate judges the firmware, whatever the operation is.
func TestFirmwareGateIgnoresTheOperation(t *testing.T) {
	for _, impact := range []escp.Impact{escp.ImpactCosmetic, escp.ImpactTraffic, escp.ImpactManagement, 0} {
		op := escp.Operation{Name: "SetX", Impact: impact}
		if err := FirmwareGate()(context.Background(), &infoSource{firmware: "1.0.0 Build 20250710 Rel.71093"}, op); err != nil {
			t.Errorf("verified build, impact %v: %v", impact, err)
		}
		var fe *FirmwareError
		if err := FirmwareGate()(context.Background(), &infoSource{firmware: "1.0.0 Build 20990101 Rel.1"}, op); !errors.As(err, &fe) {
			t.Errorf("unverified build, impact %v: %v", impact, err)
		}
	}
}
