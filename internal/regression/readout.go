package regression

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/davidlukac/escp-go"
)

// Reader reads one setting of the switch into a stable string (nothing volatile: no counters, no
// learned tables).
type Reader struct {
	Name string
	Read func(ctx context.Context) (string, error)
}

// ClientSource hands out the client to read with, or the reason there is none.
type ClientSource func() (*escp.Client, error)

// readVia makes the reader of one setting: it takes the client from src and reads with it.
func readVia(src ClientSource, name string, fn func(ctx context.Context, c *escp.Client) (string, error)) Reader {
	return Reader{Name: name, Read: func(ctx context.Context) (string, error) {
		c, err := src()
		if err != nil {
			return "", err
		}
		return fn(ctx, c)
	}}
}

// Readout is the state of every setting the readers cover, by name.
type Readout map[string]string

// CoreReaders are the settings every target has. Later groups of the suite add theirs here, so
// the same comparison covers more with each addition. Only what a person configures is read: no
// counters, no learned tables, no actual link state (a device may come and go).
//
// The readers take the client when they read (from src), not when they are made: the suite's own
// client steps aside while the provider has the switch and is dialed again afterwards.
func CoreReaders(src ClientSource) []Reader {
	return []Reader{
		readVia(src, "led", func(ctx context.Context, c *escp.Client) (string, error) {
			v, err := c.LED(ctx)
			return strconv.FormatBool(v), err
		}),
		readVia(src, "igmp", func(ctx context.Context, c *escp.Client) (string, error) {
			v, err := c.IGMP(ctx)
			return fmt.Sprintf("enabled=%t suppression=%t", v.Enabled, v.ReportSuppression), err
		}),
		readVia(src, "loop-prevention", func(ctx context.Context, c *escp.Client) (string, error) {
			v, err := c.LoopPrevention(ctx)
			return strconv.FormatBool(v), err
		}),
		readVia(src, "mac", func(ctx context.Context, c *escp.Client) (string, error) {
			v, err := c.Info(ctx)
			return v.MAC.String(), err
		}),
		readVia(src, "hostname", func(ctx context.Context, c *escp.Client) (string, error) {
			v, err := c.Info(ctx)
			return v.Hostname.String(), err
		}),
		readVia(src, "management-address", func(ctx context.Context, c *escp.Client) (string, error) {
			v, err := c.Info(ctx)
			return fmt.Sprintf("ip=%s mask=%s gateway=%s dhcp=%t", v.IP, v.Netmask, v.Gateway, v.DHCP), err
		}),
		readVia(src, "qos", func(ctx context.Context, c *escp.Client) (string, error) {
			v, err := c.QoS(ctx)
			return fmt.Sprintf("mode=%v priorities=%v", v.Mode, v.Priorities), err
		}),
		readVia(src, "ports", func(ctx context.Context, c *escp.Client) (string, error) {
			v, err := c.Ports(ctx)
			var b strings.Builder
			for _, p := range v {
				fmt.Fprintf(&b, "%d:enabled=%t,lag=%d,speed=%s,flow=%t ", p.ID, p.Enabled, p.LAG, p.ConfiguredSpeed, p.ConfiguredFlowControl)
			}
			return b.String(), err
		}),
		readVia(src, "lags", func(ctx context.Context, c *escp.Client) (string, error) {
			v, err := c.LAGs(ctx)
			return fmt.Sprint(v), err
		}),
		readVia(src, "port-mirror", func(ctx context.Context, c *escp.Client) (string, error) {
			v, err := c.PortMirror(ctx)
			return fmt.Sprintf("%+v", v), err
		}),
		readVia(src, "port-based-vlan", func(ctx context.Context, c *escp.Client) (string, error) {
			v, err := c.PortBasedVLAN(ctx)
			return fmt.Sprintf("enabled=%t vlans=%v", v.Enabled, v.VLANs), err
		}),
		readVia(src, "vlan-mode", func(ctx context.Context, c *escp.Client) (string, error) {
			v, err := c.VLANMode(ctx)
			return fmt.Sprintf("%+v", v), err
		}),
		readVia(src, "vlan-8021q", func(ctx context.Context, c *escp.Client) (string, error) {
			v, err := c.Dot1Q(ctx)
			return fmt.Sprintf("enabled=%t vlans=%v pvids=%v", v.Enabled, v.VLANs, v.PVIDs), err
		}),
		readVia(src, "bandwidth", func(ctx context.Context, c *escp.Client) (string, error) {
			v, err := c.Bandwidth(ctx)
			return fmt.Sprint(v), err
		}),
		readVia(src, "storm", func(ctx context.Context, c *escp.Client) (string, error) {
			v, err := c.StormControl(ctx)
			return fmt.Sprint(v), err
		}),
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
	keys := make([]string, 0, len(names))
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
