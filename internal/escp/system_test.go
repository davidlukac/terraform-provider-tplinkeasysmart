package escp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/internal/wire"
)

func TestSetHostnameKeepsIPConfiguration(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "secret")
	if err := c.SetHostname(context.Background(), "new-name"); err != nil {
		t.Fatal(err)
	}
	ip := f.IPSent()
	if f.Hostname() != "new-name" || len(ip) != 4 || ip[0] != 127 || ip[3] != 1 {
		t.Fatalf("hostname=%q ip=%v: the current IP must be re-sent unchanged", f.Hostname(), ip)
	}
	if err := c.SetHostname(context.Background(), "bad name"); err == nil {
		t.Fatal("invalid name must be rejected before any packet is sent")
	}
}

func TestSetHostnameRefusesToWriteWithoutAUsableIPConfiguration(t *testing.T) {
	t.Run("switch does not answer discovery", func(t *testing.T) {
		f := escptest.Start(t)
		f.Mute()
		c := dialFast(t, f, "secret")
		if err := c.SetHostname(context.Background(), "new-name"); !errors.Is(err, ErrNoReply) {
			t.Fatalf("got %v", err)
		}
	})
	bad := map[string][]escptest.Setting{
		"missing IP":      {{Type: wire.TLVNetmask, Value: []byte{255, 255, 255, 0}}, {Type: wire.TLVGateway, Value: []byte{10, 0, 0, 1}}},
		"missing netmask": {{Type: wire.TLVIP, Value: []byte{10, 0, 0, 5}}, {Type: wire.TLVGateway, Value: []byte{10, 0, 0, 1}}},
		"missing gateway": {{Type: wire.TLVIP, Value: []byte{10, 0, 0, 5}}, {Type: wire.TLVNetmask, Value: []byte{255, 255, 255, 0}}},
		"malformed IP":    {{Type: wire.TLVIP, Value: []byte{10, 0, 5}}, {Type: wire.TLVNetmask, Value: []byte{255, 255, 255, 0}}, {Type: wire.TLVGateway, Value: []byte{10, 0, 0, 1}}},
	}
	for name, tlvs := range bad {
		t.Run(name, func(t *testing.T) {
			f := escptest.Start(t)
			f.Configure(func(f *escptest.Switch) { f.DiscoverReply = tlvs })
			c := dialFast(t, f, "secret")
			err := c.SetHostname(context.Background(), "new-name")
			if err == nil || !strings.Contains(err.Error(), "unusable IP configuration") {
				t.Fatalf("want a refusal, got %v", err)
			}
			if n := f.WriteCount(); n != 0 {
				t.Fatalf("the management IP must never be written from a half-known state, %d SET(s) sent", n)
			}
		})
	}
}

func TestSetHostnameRefusesUnusableIPConfiguration(t *testing.T) {
	f := escptest.Start(t)
	f.Configure(func(f *escptest.Switch) {
		f.DiscoverReply = []escptest.Setting{{Type: wire.TLVModel, Value: []byte("TL-SG108E\x00")}, {Type: wire.TLVHostname, Value: []byte("TL-SG108E-fake\x00")}, {Type: wire.TLVMAC, Value: []byte{0x00, 0x00, 0x5e, 0x00, 0x53, 0x02}}}
	})
	c := dialFake(t, f, "secret")
	err := c.SetHostname(context.Background(), "sw01")
	if err == nil || !strings.Contains(err.Error(), "unusable IP configuration") {
		t.Fatalf("want a refusal because the IP config is unusable, got %v", err)
	}
	if n := f.WriteCount(); n != 0 {
		t.Fatalf("nothing may be written when the IP config cannot be re-sent, got %d writes", n)
	}
}

func TestSetHostnameFailsWhenSwitchIsSilent(t *testing.T) {
	f := escptest.Start(t)
	f.Mute()
	c := dialFake(t, f, "secret")
	c.cfg.Retries = 0
	c.cfg.Timeout = 100 * time.Millisecond
	if err := c.SetHostname(context.Background(), "sw01"); !errors.Is(err, ErrNoReply) {
		t.Fatalf("want ErrNoReply from the discovery step, got %v", err)
	}
}

func TestSetHostnameRejectsWrongCredentials(t *testing.T) {
	f := escptest.Start(t)
	c := dialFake(t, f, "wrong")
	if err := c.SetHostname(context.Background(), "sw01"); !errors.Is(err, ErrRejected) {
		t.Fatalf("want ErrRejected, got %v", err)
	}
}

func TestParseSystem(t *testing.T) {
	s, err := parseSystem([]wire.TLV{tlvHex(t, wire.TLVPortCount, "08")}, []wire.TLV{tlvHex(t, wire.TLVAutoSave, "01")})
	if err != nil || s != (System{PortCount: 8, AutoSave: true}) {
		t.Fatalf("got %+v %v", s, err)
	}
	if _, err := parseSystem(nil, []wire.TLV{tlvHex(t, wire.TLVAutoSave, "01")}); err == nil {
		t.Fatal("expected incomplete error")
	}
}

func TestValidateHostname(t *testing.T) {
	for _, ok := range []string{"a", "TL-SG108E-01", "sw1"} {
		if err := ValidateHostname(ok); err != nil {
			t.Errorf("%q should be valid: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-a", "a-", "has space", "under_score", "this-name-is-way-too-long-for-the-switch-0"} {
		if err := ValidateHostname(bad); err == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
