package regression

import (
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/davidlukac/escp-go/model"
)

// The sample file an operator copies to .test.env. These tests hold it to the code that loads it and to
// what the scenarios need from the target (target_needs.go): a changed sample can pass the loader, which
// checks syntax and overlaps, and still fail a scenario on a real switch.
const (
	exampleEnvPath = "../../.test.env.example"
	moduleFilePath = "../../go.mod"
	moduleLine     = "module github.com/davidlukac/terraform-provider-tplinkeasysmart\n"
)

// The sentinels of target_needs.go, in the order a failure lists them.
var allNeeds = []error{errTooFewTestPorts, errDuplicateTestPorts, errTestPortsLAG, errTooFewProtectedPorts, errUplinkLAG, errNotDevicePort}

// needsFailed returns the sentinels that err matches, in the order of allNeeds.
func needsFailed(err error) []error {
	var out []error
	for _, n := range allNeeds {
		if errors.Is(err, n) {
			out = append(out, n)
		}
	}
	return out
}

// readExampleEnv reads the sample from the repository root, after checking that the directory two levels
// up is the module root (a moved package would otherwise read nothing, or another file).
func readExampleEnv(t *testing.T) string {
	t.Helper()
	mod, err := os.ReadFile(moduleFilePath)
	if err != nil || !strings.HasPrefix(string(mod), moduleLine) {
		t.Fatalf("%s is not this module's go.mod (%v): the sample is looked for relative to it", moduleFilePath, err)
	}
	sample, err := os.ReadFile(exampleEnvPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(sample)
}

// optionalLine is an assignment the sample offers commented out, as "# TPLINK_REGRESSION_X=value".
var optionalLine = regexp.MustCompile(`(?m)^# (TPLINK_REGRESSION_[A-Z_]+=)`)

// uncommentOptional turns every optional assignment of the sample into a live one.
func uncommentOptional(sample string) string { return optionalLine.ReplaceAllString(sample, "$1") }

// setLine gives KEY the value in the text, whether the sample has the line live or commented out; the
// sample must have the line.
func setLine(t *testing.T, text, key, value string) string {
	t.Helper()
	line := regexp.MustCompile(`(?m)^(# )?` + regexp.QuoteMeta(key) + `=.*$`)
	if !line.MatchString(text) {
		t.Fatalf("the sample has no %s line", key)
	}
	return line.ReplaceAllLiteralString(text, key+"="+value)
}

// loadSample reads the text the way run does: ParseEnvFile, then LoadTarget. The password is the one value
// the sample leaves for the operator to fill in; the test asserts that it is blank and fills it.
func loadSample(t *testing.T, text string) (Target, map[string]string, error) {
	t.Helper()
	env, err := ParseEnvFile(strings.NewReader(text))
	if err != nil {
		t.Fatalf("ParseEnvFile: %v", err)
	}
	if pw := env[envPassword]; pw != "" {
		t.Fatalf("the sample carries a password (%d characters); it must stay blank", len(pw))
	}
	env[envPassword] = "filled-in-by-the-operator"
	target, err := LoadTarget(env)
	return target, env, err
}

func TestExampleEnvFileMeetsTheScenarios(t *testing.T) {
	sample := readExampleEnv(t)
	for _, tc := range []struct {
		name   string
		text   string
		optIns bool
	}{
		{"as it is", sample, false},
		{"every optional line uncommented", uncommentOptional(sample), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target, env, err := loadSample(t, tc.text)
			if err != nil {
				t.Fatalf("LoadTarget: %v", err)
			}
			if !target.Real() || target.Name != TargetSW03 {
				t.Errorf("target = %+v, want the real target %s", target, TargetSW03)
			}
			if err := target.ScenarioNeeds(); err != nil {
				t.Errorf("the scenarios need more than the sample gives: %v", err)
			}
			if !target.HasGuardPorts() {
				t.Errorf("protected ports %v: the guard-refusal scenario would skip", target.ProtectedPorts)
			}
			// each opt-in is on exactly when its lines are live, so the plain sample never moves a switch
			if got := target.CanMoveManagement(); got != tc.optIns {
				t.Errorf("CanMoveManagement = %t, want %t", got, tc.optIns)
			}
			if got := target.CanUseDHCP(); got != tc.optIns {
				t.Errorf("CanUseDHCP = %t, want %t", got, tc.optIns)
			}
			if got := target.CanDisruptDevicePort(); got != tc.optIns {
				t.Errorf("CanDisruptDevicePort = %t, want %t", got, tc.optIns)
			}
			if got := target.CanDisrupt(); got != tc.optIns {
				t.Errorf("CanDisrupt = %t, want %t", got, tc.optIns)
			}
			if tc.optIns {
				if len(target.DisruptiveLAGPairs()) == 0 {
					t.Errorf("disruptive ports %v: the disruptive LAG scenario would skip, they fit no LAG group together", target.DisruptivePorts)
				}
				if target.MoveIP == target.Host {
					t.Errorf("the spare address %s is the switch's own", target.MoveIP)
				}
				if slices.Contains(strings.Split(env[envReadOnlyHosts], ","), target.MoveIP) {
					t.Errorf("the spare address %s is a read-only switch", target.MoveIP)
				}
			}
		})
	}
}

func TestExampleEnvFileThatBreaksANeed(t *testing.T) {
	sample := readExampleEnv(t)
	type edit struct{ key, value string }
	for _, tc := range []struct {
		name     string
		optIns   bool
		edits    []edit
		wantLoad string  // a fragment of the error LoadTarget returns; empty when the text loads
		want     []error // the needs ScenarioNeeds reports, in the order of allNeeds
		noPair   bool    // the disruptive ports fit no LAG group together
	}{
		{name: "the sample of the review of 232: test ports 4,5,6,7, protected 1,2,3,8",
			edits: []edit{{envTestPorts, "4,5,6,7"}, {envProtectedPorts, "1,2,3,8"}}, want: []error{errTestPortsLAG}},
		{name: "the second test port is outside the LAG group",
			edits: []edit{{envTestPorts, "2,5,3,4"}}, want: []error{errTestPortsLAG}},
		{name: "the third test port is outside the LAG group",
			edits: []edit{{envTestPorts, "2,3,5,4"}}, want: []error{errTestPortsLAG}},
		{name: "three test ports",
			edits: []edit{{envTestPorts, "2,3,4"}}, want: []error{errTooFewTestPorts}},
		{name: "a test port listed twice",
			edits: []edit{{envTestPorts, "2,3,4,2"}}, want: []error{errDuplicateTestPorts}},
		{name: "three protected ports",
			edits: []edit{{envProtectedPorts, "1,6,7"}}, want: []error{errTooFewProtectedPorts}},
		{name: "the management port is outside the LAG group",
			edits: []edit{{envProtectedPorts, "6,1,7,8"}}, want: []error{errUplinkLAG}},
		{name: "the first test port is outside the LAG group",
			edits: []edit{{envTestPorts, "5,2,3,4"}}, want: []error{errUplinkLAG}},
		{name: "one disruptive port fits no pair", optIns: true,
			edits: []edit{{envDisruptivePorts, "8"}}, noPair: true},
		{name: "the management port is a disruptive port", optIns: true,
			edits: []edit{{envDisruptivePorts, "1,8"}}, wantLoad: "not a device port"},
		{name: "a test port is the device port to disrupt", optIns: true,
			edits: []edit{{envDisruptPort, "2"}}, wantLoad: "not a device port"},
		{name: "the spare address is the switch's own", optIns: true,
			edits: []edit{{envHost, "192.0.2.50"}, {envMoveIP, "192.0.2.50"}}, wantLoad: "own address"},
		{name: "the spare address is a read-only switch", optIns: true,
			edits: []edit{{envHost, "192.0.2.50"}, {envReadOnlyHosts, "192.0.2.60"}, {envMoveIP, "192.0.2.60"}}, wantLoad: "read-only switch"},
		{name: "the move is on with no spare address", optIns: true,
			edits: []edit{{envMoveIP, ""}}, wantLoad: "not an IPv4 address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := sample
			if tc.optIns {
				text = uncommentOptional(text)
			}
			for _, e := range tc.edits {
				text = setLine(t, text, e.key, e.value)
			}
			target, _, err := loadSample(t, text)
			if tc.wantLoad != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantLoad) {
					t.Fatalf("LoadTarget error = %v, want one that says %q", err, tc.wantLoad)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadTarget: %v", err)
			}
			needs := target.ScenarioNeeds()
			if got := needsFailed(needs); !slices.Equal(got, tc.want) {
				t.Errorf("ScenarioNeeds failed %v, want %v (%v)", got, tc.want, needs)
			}
			if got := len(target.DisruptiveLAGPairs()) == 0; tc.optIns && got != tc.noPair {
				t.Errorf("no disruptive LAG pair = %t, want %t (disruptive ports %v)", got, tc.noPair, target.DisruptivePorts)
			}
		})
	}
}

func TestScenarioNeedsOfTheBuiltInFake(t *testing.T) {
	target, err := LoadTarget(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if err := target.ScenarioNeeds(); err != nil {
		t.Errorf("the fake does not meet the needs of the scenarios: %v", err)
	}
	if !target.HasGuardPorts() || !target.CanDisrupt() || len(target.DisruptiveLAGPairs()) == 0 {
		t.Errorf("the fake would skip a scenario that the default regression runs: %+v", target)
	}
}

func TestScenarioNeedsOfAHandBuiltTarget(t *testing.T) {
	ok := func() Target {
		return Target{TestPorts: []model.PortID{2, 3, 4, 5}, ProtectedPorts: []model.PortID{1, 6, 7, 8}}
	}
	for _, tc := range []struct {
		name   string
		target Target
		want   []error
	}{
		{"the defaults", ok(), nil},
		{"no port at all", Target{}, []error{errTooFewTestPorts, errTooFewProtectedPorts}},
		{"no protected port, four test ports", Target{TestPorts: []model.PortID{2, 3, 4, 5}}, []error{errTooFewProtectedPorts}},
		{"no test port, four protected ports", Target{ProtectedPorts: []model.PortID{1, 6, 7, 8}}, []error{errTooFewTestPorts}},
		{"three test ports, the first outside the uplink's group", Target{TestPorts: []model.PortID{5, 2, 3}, ProtectedPorts: []model.PortID{1, 6, 7, 8}},
			[]error{errTooFewTestPorts}},
		{"three protected ports, the first outside the test port's group", Target{TestPorts: []model.PortID{2, 3, 4, 5}, ProtectedPorts: []model.PortID{6, 7, 8}},
			[]error{errTooFewProtectedPorts}},
		{"too few of both, with a device port named", Target{TestPorts: []model.PortID{2}, ProtectedPorts: []model.PortID{1, 8}, DisruptPort: 8},
			[]error{errTooFewTestPorts, errTooFewProtectedPorts}},
		{"the same port twice in the LAG pair", Target{TestPorts: []model.PortID{2, 3, 3, 5}, ProtectedPorts: []model.PortID{1, 6, 7, 8}},
			[]error{errDuplicateTestPorts, errTestPortsLAG}},
		{"a port no mask can hold in the LAG pair", Target{TestPorts: []model.PortID{2, 3, 0, 5}, ProtectedPorts: []model.PortID{1, 6, 7, 8}},
			[]error{errTestPortsLAG}},
		{"a device port that is the management port", func() Target { t := ok(); t.DisruptPort = 1; return t }(), []error{errNotDevicePort}},
		{"a device port that is a test port", func() Target { t := ok(); t.DisruptPort = 2; return t }(), []error{errNotDevicePort}},
		{"a disruptive port that is a test port", func() Target { t := ok(); t.DisruptivePorts = []model.PortID{7, 2}; return t }(), []error{errNotDevicePort}},
		{"device ports named", func() Target {
			t := ok()
			t.DisruptPort, t.DisruptivePorts = 8, []model.PortID{7, 8}
			return t
		}(), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.target.ScenarioNeeds()
			if got := needsFailed(err); !slices.Equal(got, tc.want) {
				t.Errorf("ScenarioNeeds failed %v, want %v (%v)", got, tc.want, err)
			}
			if (err == nil) != (len(tc.want) == 0) {
				t.Errorf("ScenarioNeeds = %v, want an error exactly when a need fails", err)
			}
		})
	}
}

func TestScenarioPortsOfATarget(t *testing.T) {
	target := Target{TestPorts: []model.PortID{2, 3, 4, 5}, ProtectedPorts: []model.PortID{1, 6, 7, 8}}
	if a, b := target.LAGTestPorts(); a != 3 || b != 4 {
		t.Errorf("LAGTestPorts = %d, %d, want 3, 4 (the second and the third test port)", a, b)
	}
	if uplink, test := target.UplinkLAGPorts(); uplink != 1 || test != 2 {
		t.Errorf("UplinkLAGPorts = %d, %d, want 1, 2 (the first protected port and the first test port)", uplink, test)
	}
	for _, tc := range []struct {
		name      string
		protected []model.PortID
		want      []model.PortID
	}{
		{"none", nil, nil},
		{"the management port only", []model.PortID{1}, nil},
		{"the management port and devices", []model.PortID{1, 6, 7, 8}, []model.PortID{6, 7, 8}},
	} {
		t.Run("device ports: "+tc.name, func(t *testing.T) {
			if got := (Target{ProtectedPorts: tc.protected}).DevicePorts(); !slices.Equal(got, tc.want) {
				t.Errorf("DevicePorts = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTargetSkipConditions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target Target
		guard  bool
		move   bool
		dhcp   bool
		device bool
		disr   bool
	}{
		{"nothing", Target{}, false, false, false, false, false},
		{"three protected ports", Target{ProtectedPorts: []model.PortID{1, 6, 7}}, false, false, false, false, false},
		{"four protected ports", Target{ProtectedPorts: []model.PortID{1, 6, 7, 8}}, true, false, false, false, false},
		{"a spare address", Target{MoveIP: "192.0.2.14"}, false, true, false, false, false},
		{"DHCP", Target{DHCP: true}, false, false, true, false, false},
		{"a device port", Target{DisruptPort: 8}, false, false, false, true, false},
		{"disruptive ports", Target{DisruptivePorts: []model.PortID{8}}, false, false, false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := [...]bool{tc.target.HasGuardPorts(), tc.target.CanMoveManagement(), tc.target.CanUseDHCP(), tc.target.CanDisruptDevicePort(), tc.target.CanDisrupt()}
			if want := [...]bool{tc.guard, tc.move, tc.dhcp, tc.device, tc.disr}; got != want {
				t.Errorf("guard, move, DHCP, device port, disruptive = %v, want %v", got, want)
			}
		})
	}
}

func TestDisruptiveLAGPairs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ports []model.PortID
		want  []LAGPair
	}{
		{"none", nil, nil},
		{"one port", []model.PortID{8}, nil},
		{"two ports of group 2", []model.PortID{7, 8}, []LAGPair{{A: 7, B: 8, Group: 2}}},
		{"two ports of group 1", []model.PortID{3, 4}, []LAGPair{{A: 3, B: 4, Group: 1}}},
		{"one port in each group", []model.PortID{4, 7}, nil},
		{"the same port twice is no pair", []model.PortID{7, 7}, nil},
		{"the first port of group 1 is never a member", []model.PortID{1, 2}, nil},
		{"the lower port comes first, whatever the order of the list", []model.PortID{8, 5}, []LAGPair{{A: 5, B: 8, Group: 2}}},
		{"every pair, in the order of the list", []model.PortID{5, 6, 7}, []LAGPair{{5, 6, 2}, {5, 7, 2}, {6, 7, 2}}},
		{"a pair that crosses the groups is left out", []model.PortID{2, 3, 4, 5},
			[]LAGPair{{A: 2, B: 3, Group: 1}, {A: 2, B: 4, Group: 1}, {A: 3, B: 4, Group: 1}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (Target{DisruptivePorts: tc.ports}).DisruptiveLAGPairs(); !slices.Equal(got, tc.want) {
				t.Errorf("DisruptiveLAGPairs(%v) = %v, want %v", tc.ports, got, tc.want)
			}
		})
	}
}
