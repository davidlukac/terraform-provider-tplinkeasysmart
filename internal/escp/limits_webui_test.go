package escp

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"testing"
)

// webUILimits is testdata/webui-limits.json: the limits the switch's own web UI enforces (field
// lengths, table sizes), read from its pages by the workspace's `scripts/webui-map.sh limits` over
// every mapped firmware build (a limit that differs between builds stops the generator, so each
// value here holds for all of them). The file is regenerated when a build is mapped.
type webUILimits struct {
	Builds []string       `json:"builds"`
	Limits map[string]int `json:"limits"`
}

func loadWebUILimits(t *testing.T) webUILimits {
	t.Helper()
	raw, err := os.ReadFile("testdata/webui-limits.json")
	if err != nil {
		t.Fatal(err)
	}
	var l webUILimits
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	if len(l.Builds) == 0 || len(l.Limits) == 0 {
		t.Fatalf("webui-limits.json has %d builds and %d limits", len(l.Builds), len(l.Limits))
	}
	return l
}

func digits(n int) int { return len(strconv.Itoa(n)) }

// The library validates what it sends before anything goes on the wire; where the web UI limits the
// same value, the two must agree, or one of them is wrong (a user would be refused by one and not by
// the other). Every limit in the file is classified: it matches a library constant, it is a known
// divergence with a reason, or the library does not model it yet. A limit that is none of these
// (a new one in a new firmware) fails the test until it is classified.
func TestLibraryLimitsMatchTheWebUI(t *testing.T) {
	web := loadWebUILimits(t)

	matches := map[string]int{
		"device_description_max_length": MaxHostnameLen,
		"vlan_name_max_length":          MaxVLANNameLen,
		"vlan_id_max_digits":            digits(int(MaxVLANID)),
		"pvid_max_digits":               digits(int(MaxVLANID)),
		"management_vlan_max_id":        int(MaxVLANID),
		"bandwidth_rate_max_digits":     digits(MaxBandwidthKbps),
		"storm_rate_max_digits":         digits(MaxBandwidthKbps),
		"lag_group_count":               LAGGroupCount,
	}
	// Known divergences: the library value, and why it is left as it is.
	diverges := map[string]struct {
		library int
		why     string
	}{
		"port_based_vlan_id_max_digits": {digits(MaxPortVLANID), "the web UI takes port-based VLAN ids 2 to the port count (page text and pvlan.js); the type allows the wire's 1 to 255, because it does not know the switch, and SetPortBasedVLAN and the provider refuse an id above the number of groups the switch reports (TLV 8450, 8 on a TL-SG108E), as the web UI does. Whether the switch itself would accept a higher id is not probed"},
	}
	// Limits the library does not model as constants: it does not write them (user account, CLI only; the
	// switch's own rule for those is not probed) or reads them from the switch (the table size, enforced by
	// the switch and checked through Dot1Q.CheckRoom; the port count).
	notModelled := map[string]bool{"user_name_max_length": true, "password_max_length": true, "dot1q_vlan_table_size": true, "port_count": true}

	for name, want := range matches {
		t.Run(name, func(t *testing.T) {
			got, ok := web.Limits[name]
			if !ok {
				t.Fatalf("webui-limits.json has no %s", name)
			}
			if got != want {
				t.Errorf("the web UI limit %s is %d (builds %v), the library's is %d: one of them is wrong; check both and the validators built on the constant", name, got, web.Builds, want)
			}
		})
	}
	for name, d := range diverges {
		t.Run(name, func(t *testing.T) {
			got, ok := web.Limits[name]
			if !ok {
				t.Fatalf("webui-limits.json has no %s", name)
			}
			if got == d.library {
				t.Errorf("%s now agrees (%d): move it to the matching limits", name, got)
			}
			t.Logf("known divergence: web UI %d, library %d: %s", got, d.library, d.why)
		})
	}
	var unclassified []string
	for name := range web.Limits {
		if _, ok := matches[name]; ok {
			continue
		}
		if _, ok := diverges[name]; ok {
			continue
		}
		if !notModelled[name] {
			unclassified = append(unclassified, name)
		}
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Errorf("limits in webui-limits.json that are not classified: %v; add each to matches, diverges or notModelled", unclassified)
	}
	for name := range notModelled {
		if _, ok := web.Limits[name]; !ok {
			t.Errorf("notModelled lists %s, which webui-limits.json no longer has", name)
		}
	}
}
