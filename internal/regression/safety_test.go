package regression

import (
	"context"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp/escptest"
)

func TestCheckTestPorts(t *testing.T) {
	s := Open(t, noEnv)
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		ports []escp.PortID
		want  string
	}{
		{"free ports", []escp.PortID{2, 3, 4, 5}, ""},
		{"a port with a link", []escp.PortID{2, 6}, "port 6 has a link"},
		{"a port that does not exist", []escp.PortID{2, 9}, "port 9 does not exist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckTestPorts(ctx, s.Client, Target{TestPorts: tc.ports})
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
	// A free port that has become a LAG member is refused too.
	if err := s.Client.SetLAG(ctx, 1, escp.MustPortSet(3, 4)); err != nil {
		t.Fatal(err)
	}
	if err := CheckTestPorts(ctx, s.Client, Target{TestPorts: []escp.PortID{3}}); err == nil || !strings.Contains(err.Error(), "member of LAG 1") {
		t.Errorf("err = %v, want the LAG named", err)
	}
}

func TestCheckTestPortsUnreadable(t *testing.T) {
	s := Open(t, noEnv)
	s.Fake.Configure(func(f *escptest.Switch) {
		f.GetFn = func([]escptest.TLVType) ([]escptest.Setting, int32, bool) { return nil, 1, false }
	})
	if err := CheckTestPorts(context.Background(), s.Client, Target{TestPorts: []escp.PortID{2}}); err == nil || !strings.Contains(err.Error(), "cannot read the ports") {
		t.Errorf("err = %v", err)
	}
}
