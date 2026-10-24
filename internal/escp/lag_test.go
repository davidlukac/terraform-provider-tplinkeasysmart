package escp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestParseLAGs(t *testing.T) {
	got, err := parseLAGs([]wire.TLV{tlvHex(t, wire.TLVLAG, "0100000003"), tlvHex(t, wire.TLVLAG, "0200000000"), tlvHex(t, wire.TLVLAG, "0380000005")})
	if err != nil {
		t.Fatal(err)
	}
	// 0x80000005 -> bits 0,2,31 -> ports 1,3,32
	want := []LAG{{ID: 1, Ports: []int{1, 2}}, {ID: 2, Ports: []int{}}, {ID: 3, Ports: []int{1, 3, 32}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if _, err := parseLAGs([]wire.TLV{{Type: wire.TLVLAG, Value: []byte{1}}}); err == nil {
		t.Fatal("expected error")
	}
}

func TestSetLAGWire(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	ctx := context.Background()
	if err := c.SetLAG(ctx, 2, []int{6, 7}); err != nil {
		t.Fatal(err)
	}
	if got := f.LastSet(); got[0].Type != wire.TLVLAG || !reflect.DeepEqual(got[0].Value, []byte{2, 0, 0, 0, 0x60}) {
		t.Fatalf("wire %+v", got)
	}
	if err := c.SetLAG(ctx, 2, nil); err != nil {
		t.Fatal(err)
	}
	if got := f.LastSet(); !reflect.DeepEqual(got[0].Value, []byte{2, 0, 0, 0, 0}) {
		t.Fatalf("delete wire %v", got[0].Value)
	}
	n := f.WriteCount()
	for _, bad := range []struct {
		group int
		ports []int
	}{{0, []int{1, 2}}, {2, []int{40}}, {3, []int{5, 6}}, {1, []int{4, 5}}, {2, []int{2, 3}}, {2, []int{6}}, {1, []int{1, 2, 3, 4, 5}}} {
		if c.SetLAG(ctx, bad.group, bad.ports) == nil {
			t.Errorf("LAG %d with ports %v must be rejected", bad.group, bad.ports)
		}
	}
	if f.WriteCount() != n {
		t.Fatal("rejected requests must not reach the switch")
	}
}

func TestValidateLAG(t *testing.T) {
	for _, tc := range []struct {
		name  string
		group int
		ports []int
		want  string // empty: valid
	}{
		{"group 1 low ports", 1, []int{1, 2}, ""},
		{"group 1 all four", 1, []int{1, 2, 3, 4}, ""},
		{"group 2 high ports", 2, []int{5, 8}, ""},
		{"group 2 all four", 2, []int{5, 6, 7, 8}, ""},
		{"remove group 1", 1, nil, ""},
		{"remove group 2", 2, []int{}, ""},
		{"group 0", 0, []int{1, 2}, "invalid LAG group"},
		{"group 3", 3, []int{1, 2}, "invalid LAG group"},
		{"remove group 3", 3, nil, "invalid LAG group"},
		{"the same port twice", 1, []int{2, 2}, "port 2 is listed twice"},
		{"a repeat among four", 1, []int{1, 2, 3, 3}, "port 3 is listed twice"},
		{"one port", 1, []int{2}, "2 to 4 ports"},
		{"five ports", 1, []int{1, 2, 3, 4, 5}, "2 to 4 ports"},
		{"group 1 reaching into group 2", 1, []int{4, 5}, "port 5 cannot be in LAG 1 (LAG 1 takes ports 1-4)"},
		{"group 2 with low ports", 2, []int{2, 3}, "port 2 cannot be in LAG 2 (LAG 2 takes ports 5-8)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateLAG(tc.group, tc.ports)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// Every code below was provoked on a real TL-SG108E (firmware 20250710), the member rules
// (7405 to 7418) on free ports 3 and 4 of switch 03; the error must name the conflict instead
// of blaming the credentials.
func TestSetLAGRefusalNamesTheConflict(t *testing.T) {
	for _, tc := range []struct {
		code int32
		want string
	}{
		{4, "LAG 1 takes ports 1-4"},
		{7401, "port-based VLAN"},
		{7403, "bandwidth limit"},
		{7404, "storm control"},
		{7205, "destination of the port mirror"},
		{7206, "is mirrored"},
		{7415, "at least two ports"},
		{7405, "different QoS priorities"},
		{7406, "do not have the same speed"},
		{7407, "half duplex; LAG members must be full duplex"},
		{7410, "mix auto-negotiation with a fixed speed"},
		{7417, "same flow control setting"},
		{7418, "all enabled or all disabled"},
	} {
		t.Run(fmt.Sprint(tc.code), func(t *testing.T) {
			f := escptest.Start(t)
			f.Configure(func(f *escptest.Switch) { f.RejectSetsFrom, f.RejectCode = 1, tc.code })
			c := dialFake(t, f, "secret")
			err := c.SetLAG(context.Background(), 1, []int{2, 3})
			var rejected *RejectedError
			if !errors.Is(err, ErrRejected) || !errors.As(err, &rejected) || rejected.Code != tc.code {
				t.Fatalf("got %v, want a RejectedError with code %d that matches ErrRejected", err, tc.code)
			}
			if !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "wrong credentials") {
				t.Fatalf("message %q must contain %q and not blame the credentials", err, tc.want)
			}
		})
	}
	t.Run("an unknown code keeps the generic message", func(t *testing.T) {
		f := escptest.Start(t)
		f.Configure(func(f *escptest.Switch) { f.RejectSetsFrom = 1 })
		c := dialFake(t, f, "secret")
		err := c.SetLAG(context.Background(), 1, []int{2, 3})
		if !errors.Is(err, ErrRejected) || !strings.Contains(err.Error(), "error code 9") || !strings.Contains(err.Error(), "wrong credentials") {
			t.Fatalf("got %v", err)
		}
	})
}
