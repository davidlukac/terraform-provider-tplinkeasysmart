package regression

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/internal/escp"
)

// Reader reads one setting of the switch into a stable string (nothing volatile: no counters, no
// learned tables).
type Reader struct {
	Name string
	Read func(ctx context.Context) (string, error)
}

// Readout is the state of every setting the readers cover, by name.
type Readout map[string]string

// CoreReaders are the settings every target has. Later groups of the suite add theirs here, so
// the same comparison covers more with each addition. Only what a person configures is read: no
// counters, no learned tables, no actual link state (a device may come and go).
func CoreReaders(c *escp.Client) []Reader {
	return []Reader{
		{"led", func(ctx context.Context) (string, error) {
			v, err := c.LED(ctx)
			return fmt.Sprint(v), err
		}},
		{"igmp", func(ctx context.Context) (string, error) {
			v, err := c.IGMP(ctx)
			return fmt.Sprintf("enabled=%t suppression=%t", v.Enabled, v.ReportSuppression), err
		}},
		{"loop-prevention", func(ctx context.Context) (string, error) {
			v, err := c.LoopPrevention(ctx)
			return fmt.Sprint(v), err
		}},
		{"hostname", func(ctx context.Context) (string, error) {
			v, err := c.Info(ctx)
			return v.Hostname, err
		}},
		{"management-address", func(ctx context.Context) (string, error) {
			v, err := c.Info(ctx)
			return fmt.Sprintf("ip=%s mask=%s gateway=%s dhcp=%t", v.IP, v.Netmask, v.Gateway, v.DHCP), err
		}},
		{"qos", func(ctx context.Context) (string, error) {
			v, err := c.QoS(ctx)
			return fmt.Sprintf("mode=%v priorities=%v", v.Mode, v.Priorities), err
		}},
		{"ports", func(ctx context.Context) (string, error) {
			v, err := c.Ports(ctx)
			var b strings.Builder
			for _, p := range v {
				fmt.Fprintf(&b, "%d:enabled=%t,lag=%d,speed=%s,flow=%t ", p.ID, p.Enabled, p.LAG, p.ConfiguredSpeed, p.ConfiguredFlowControl)
			}
			return b.String(), err
		}},
		{"lags", func(ctx context.Context) (string, error) {
			v, err := c.LAGs(ctx)
			return fmt.Sprint(v), err
		}},
		{"port-mirror", func(ctx context.Context) (string, error) {
			v, err := c.PortMirror(ctx)
			return fmt.Sprintf("%+v", v), err
		}},
		{"port-based-vlan", func(ctx context.Context) (string, error) {
			v, err := c.PortBasedVLAN(ctx)
			return fmt.Sprintf("enabled=%t vlans=%v", v.Enabled, v.VLANs), err
		}},
		{"vlan-mode", func(ctx context.Context) (string, error) {
			v, err := c.VLANMode(ctx)
			return fmt.Sprintf("%+v", v), err
		}},
		{"vlan-8021q", func(ctx context.Context) (string, error) {
			v, err := c.Dot1Q(ctx)
			return fmt.Sprintf("enabled=%t vlans=%v pvids=%v", v.Enabled, v.VLANs, v.PVIDs), err
		}},
		{"bandwidth", func(ctx context.Context) (string, error) {
			v, err := c.Bandwidth(ctx)
			return fmt.Sprint(v), err
		}},
		{"storm", func(ctx context.Context) (string, error) {
			v, err := c.StormControl(ctx)
			return fmt.Sprint(v), err
		}},
	}
}

// Take reads everything the readers cover. A failing reader fails the readout: a state that
// cannot be read cannot be shown to be restored.
func Take(ctx context.Context, readers []Reader) (Readout, error) {
	out := Readout{}
	for _, r := range readers {
		v, err := r.Read(ctx)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", r.Name, err)
		}
		out[r.Name] = v
	}
	return out, nil
}

// Diff lists the settings that differ between a readout taken before and one taken after, one
// line each and sorted, naming the setting and both values. Empty means identical.
func Diff(before, after Readout) []string {
	names := map[string]bool{}
	for k := range before {
		names[k] = true
	}
	for k := range after {
		names[k] = true
	}
	var keys []string
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []string
	for _, k := range keys {
		b, bok := before[k]
		a, aok := after[k]
		if bok != aok || a != b {
			out = append(out, fmt.Sprintf("%s: before %q, after %q", k, b, a))
		}
	}
	return out
}
