//go:build !windows

// These tests run a POSIX shell script over symbolic links and file modes, as the probes do; the tool is for
// the systems that script runs on, so they are not built for Windows.

package probe

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// repoRoot is the root of the repository, from the directory of this package.
const repoRoot = "../../.."

// call is one call of the fake runner.
type call struct {
	dir  string
	env  []string
	name string
	args []string
}

// fakeRunner stands in for the programs the harness runs. git always succeeds unless gitFails; the n-th run
// of the script (counted from 0) is answered by script.
type fakeRunner struct {
	calls    []call
	scripts  int
	gitErr   error
	gitState int
	script   func(n int, c call) (string, int, error)
}

func (f *fakeRunner) run(_ context.Context, dir string, env []string, name string, args ...string) (string, int, error) {
	c := call{dir: dir, env: env, name: name, args: args}
	f.calls = append(f.calls, c)
	if name == gitCommand {
		return "git says no", f.gitState, f.gitErr
	}
	n := f.scripts
	f.scripts++
	return f.script(n, c)
}

// testEnvironment is an environment over the real repository's .github/ with a fake runner and a fake zizmor
// on the PATH of the process.
func testEnvironment(t *testing.T, vars map[string]string, f *fakeRunner) (Environment, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	var out, errOut bytes.Buffer
	return Environment{
		Stdout: &out, Stderr: &errOut,
		Getenv: func(name string) string { return vars[name] },
		Environ: func() []string {
			return []string{"KEPT=1", "GH_TOKEN=secret", "GITHUB_TOKEN=secret", "GH_TOKEN_X=kept"}
		},
		LookPath:   func(string) (string, error) { return "/bin/zizmor", nil },
		Executable: func() (string, error) { return "/bin/probe", nil },
		MkdirTemp:  func(string) (string, error) { return t.TempDir(), nil },
		Runner:     f.run,
	}, &out, &errOut
}

// writeRecord is what the stub does: it records the arguments and the environment in STUB_OUT.
func writeRecord(t *testing.T, env []string, args, vars []string) {
	t.Helper()
	var dir string
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, stubOutVar+"="); ok {
			dir = v
		}
	}
	if err := os.WriteFile(filepath.Join(dir, recordedArgs), []byte(strings.Join(args, "\n")+"\n"), fileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, recordedEnv), []byte(strings.Join(vars, "\n")+"\n"), fileMode); err != nil {
		t.Fatal(err)
	}
}

// idealScript answers each run as the script does when every probe holds: a pass, or a failure that says
// what the case wants, and for a stub run the call the assertions describe. deviate, when set, is given
// the case and the ideal answer and returns what to answer instead (it may change the recorded call).
func idealScript(t *testing.T, cases []probeCase, deviate func(i int, c probeCase, args, vars *[]string) (string, int)) func(int, call) (string, int, error) {
	t.Helper()
	return func(n int, c call) (string, int, error) {
		pc := cases[n]
		out, status := "", 0
		if !pc.want.pass {
			out, status = "refused: "+pc.want.marker+"\n", 1
		}
		for _, text := range pc.outputHas {
			out += text + "\n"
		}
		var args, vars []string
		for _, a := range pc.recorded {
			switch a.kind {
			case hasArg:
				args = append(args, a.value)
			case hasEnv:
				vars = append(vars, a.value)
			case lacksArg, lacksEnv:
			}
		}
		if deviate != nil {
			if o, s := deviate(n, pc, &args, &vars); o != "" || s >= 0 {
				out, status = o, s
			}
		}
		if pc.stub && status == 0 {
			writeRecord(t, c.env, args, vars)
		}
		return out, status, nil
	}
}

// noDeviation leaves the ideal answer as it is.
func noDeviation(int, probeCase, *[]string, *[]string) (string, int) { return "", -1 }

func TestRunAllProbesHoldAgainstAnIdealScript(t *testing.T) {
	cases := slices.Concat(scriptCases(), stubCases())
	f := &fakeRunner{}
	f.script = idealScript(t, cases, noDeviation)
	env, out, errOut := testEnvironment(t, map[string]string{countVar: "13", "PATH": "/usr/bin", "HOME": "/home/x", "GOCACHE": ""}, f)
	if status := Run(context.Background(), []string{"zizmorprobe", "-root", repoRoot}, env); status != exitOK {
		t.Fatalf("status %d\n%s\n%s", status, out, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if want := len(cases) + 1; len(lines) != want {
		t.Fatalf("%d lines, want %d (every probe and the summary):\n%s", len(lines), want, out)
	}
	for i, c := range cases {
		if lines[i] != "ok    "+c.name {
			t.Errorf("line %d = %q, want the ok line of %q", i, lines[i], c.name)
		}
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr = %q", errOut)
	}
}

// runCases runs the cases through a harness over the real repository's .github/, with the fake script, and
// returns the status and what was printed.
func runCases(t *testing.T, cases []probeCase, script func(int, call) (string, int, error)) (int, string, string) {
	t.Helper()
	f := &fakeRunner{script: script}
	env, out, errOut := testEnvironment(t, nil, f)
	h, err := newHarness(env, repoRoot, "13", "/bin/probe")
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	return h.runAll(context.Background(), cases), out.String(), errOut.String()
}

// Every probe can fail: the run is red when the script behaves differently in that one case, whichever way
// (it passes where it must refuse, it refuses for another reason, it refuses where it must pass).
func TestRunReportsEveryProbeThatBreaks(t *testing.T) {
	for _, c := range slices.Concat(scriptCases(), stubCases()) {
		deviations := map[string]func(int, probeCase, *[]string, *[]string) (string, int){
			"it gives the other status": func(_ int, pc probeCase, _, _ *[]string) (string, int) {
				if pc.want.pass {
					return "refused: " + pc.want.marker + "\n", 1
				}
				return "", 0
			},
		}
		if !c.want.pass {
			deviations["it refuses for another reason"] = func(int, probeCase, *[]string, *[]string) (string, int) {
				return "some other failure\n", 1
			}
		}
		for name, deviate := range deviations {
			t.Run(c.name+": "+name, func(t *testing.T) {
				cases := []probeCase{c}
				status, out, _ := runCases(t, cases, idealScript(t, cases, deviate))
				if status != exitProblem || !strings.Contains(out, "FAIL  "+c.name+": ") {
					t.Errorf("status %d, want %d with a FAIL line for the case:\n%s", status, exitProblem, out)
				}
			})
		}
	}
}

// The probes on the log of the run are checked too: a line that starts a workflow command, or the escaped line
// that is missing, turns the probe red.
func TestRunChecksTheOutputOfTheCases(t *testing.T) {
	for _, c := range scriptCases() {
		if len(c.noLineStartsWith) == 0 && len(c.outputHas) == 0 {
			continue
		}
		for name, output := range map[string]string{
			"a line that starts a workflow command": c.outputHas[0] + "\n::warning::x.yml\n",
			"a line that starts one after spaces":   c.outputHas[0] + "\n  ::warning::x.yml\n",
			"a line that starts one after a tab":    c.outputHas[0] + "\n\t::warning::x.yml\n",
			"the escaped line missing":              "something else\n",
		} {
			t.Run(c.name+": "+name, func(t *testing.T) {
				cases := []probeCase{c}
				status, out, _ := runCases(t, cases, func(int, call) (string, int, error) { return output, 0, nil })
				if status != exitProblem || !strings.Contains(out, "FAIL  "+c.name+": ") {
					t.Errorf("status %d, want %d with a FAIL line:\n%s", status, exitProblem, out)
				}
			})
		}
	}
}

// A control that fails ends the run: the probes after it prove nothing.
func TestAFailedControlEndsTheRun(t *testing.T) {
	cases := []probeCase{{name: "control", want: passes, control: true}, {name: "after", want: passes}}
	f := &fakeRunner{script: func(int, call) (string, int, error) { return "broken\n", 1, nil }}
	env, out, errOut := testEnvironment(t, nil, f)
	h, err := newHarness(env, repoRoot, "13", "/bin/probe")
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	if status := h.runAll(context.Background(), cases); status != exitProblem || f.scripts != 1 {
		t.Errorf("status %d after %d runs, want %d after 1", status, f.scripts, exitProblem)
	}
	if !strings.Contains(errOut.String(), "a control failed") || strings.Contains(out.String(), "after") {
		t.Errorf("stdout %q, stderr %q", out, errOut)
	}
}

// Every assertion on the recorded call is checked: a call that breaks it turns the probe red.
func TestRunChecksEveryAssertionOnTheRecordedCall(t *testing.T) {
	for _, c := range stubCases() {
		for _, a := range c.recorded {
			t.Run(c.name+": "+describe(a), func(t *testing.T) {
				cases := []probeCase{c}
				status, out, _ := runCases(t, cases, idealScript(t, cases, func(_ int, _ probeCase, args, vars *[]string) (string, int) {
					breakAssertion(a, args, vars)
					return "", -1
				}))
				if want := "the stub recorded a call that breaks: " + describe(a); status != exitProblem || !strings.Contains(out, want) {
					t.Errorf("status %d, want %d with %q:\n%s", status, exitProblem, want, out)
				}
			})
		}
	}
}

// breakAssertion changes the recorded call so that the assertion is false.
func breakAssertion(a assertion, args, vars *[]string) {
	switch a.kind {
	case hasArg:
		*args = slices.DeleteFunc(*args, func(s string) bool { return s == a.value })
	case lacksArg:
		*args = append(*args, a.value)
	case hasEnv:
		*vars = slices.DeleteFunc(*vars, func(s string) bool { return s == a.value })
	case lacksEnv:
		*vars = append(*vars, a.value+"=x")
	}
}

func TestJudge(t *testing.T) {
	call := recorded{args: []string{"--offline", "."}, env: []string{"A=1", "GH_TOKEN=", "GH_TOKEN_X=y"}}
	for _, tc := range []struct {
		name string
		c    probeCase
		res  result
		want []string
	}{
		{name: "a pass that is wanted", c: probeCase{want: passes}, res: result{status: 0}},
		{name: "a failure that says it", c: probeCase{want: fails("because")}, res: result{status: 1, output: "xx because yy"}},
		{name: "a pass that is not wanted", c: probeCase{want: passes}, res: result{status: 1}, want: []string{"exit 1, wanted a pass"}},
		{name: "a failure that is not wanted", c: probeCase{want: fails("because")}, res: result{status: 0},
			want: []string{"wanted a failure that mentions because, the script passed"}},
		{name: "a failure for another reason", c: probeCase{want: fails("because")}, res: result{status: 2, output: "nope"},
			want: []string{"exit 2, wanted a failure that mentions because"}},
		{name: "assertions that hold", c: probeCase{want: passes, recorded: []assertion{
			{hasArg, "."}, {lacksArg, "--x"}, {hasEnv, "A=1"}, {hasEnv, "GH_TOKEN="}, {lacksEnv, "GITHUB_TOKEN"}, {lacksEnv, "GH_TOKE"}, {lacksEnv, "GH_TOKEN_Y"},
		}}, res: result{call: call}},
		{name: "an argument that is only part of a line", c: probeCase{want: passes, recorded: []assertion{{hasArg, "--off"}}}, res: result{call: call},
			want: []string{"the stub recorded a call that breaks: has the argument --off"}},
		{name: "an argument that is there but must not be", c: probeCase{want: passes, recorded: []assertion{{lacksArg, "--offline"}}}, res: result{call: call},
			want: []string{"the stub recorded a call that breaks: lacks the argument --offline"}},
		{name: "a variable whose value differs", c: probeCase{want: passes, recorded: []assertion{{hasEnv, "A=2"}}}, res: result{call: call},
			want: []string{"the stub recorded a call that breaks: has the variable A=2"}},
		{name: "a variable that is set and must not be, even empty", c: probeCase{want: passes, recorded: []assertion{{lacksEnv, "GH_TOKEN"}}}, res: result{call: call},
			want: []string{"the stub recorded a call that breaks: lacks the variable GH_TOKEN"}},
		{name: "assertions are not checked on a refusal", c: probeCase{want: fails("x"), recorded: []assertion{{hasArg, "zzz"}}}, res: result{status: 1, output: "x"}},
		{name: "two problems at once", c: probeCase{want: passes, recorded: []assertion{{hasArg, "a"}}}, res: result{call: call},
			want: []string{"the stub recorded a call that breaks: has the argument a"}},
		{name: "an assertion of a kind that does not exist", c: probeCase{want: passes, recorded: []assertion{{kind(99), "x"}}}, res: result{call: call},
			want: []string{"the stub recorded a call that breaks: unknown assertion"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := judge(tc.c, tc.res); !slices.Equal(got, tc.want) {
				t.Errorf("judge = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStubEnvironment(t *testing.T) {
	f := &fakeRunner{}
	env, _, _ := testEnvironment(t, map[string]string{"PATH": "/usr/bin", "HOME": "/home/x", "GOCACHE": "/cache", "GOFLAGS": "", "SECRET": "no"}, f)
	h := &harness{env: env, scratch: "/scratch"}
	for _, tc := range []struct {
		name string
		c    probeCase
		has  []string
		not  []string
	}{
		{name: "the defaults", c: probeCase{},
			has: []string{"PATH=/scratch/stub:/usr/bin", "HOME=/home/x", "GOCACHE=/cache", "STUB_OUT=/rec", "ZIZMOR_SELF_REPOSITORY_FINDINGS=0",
				"ZIZMOR_OFFLINE=true", "ZIZMOR_NO_ONLINE_AUDITS=true", "ZIZMOR_CONFIG=/nonexistent", "GH_HOST=example.invalid", "ZIZMOR_GITHUB_TOKEN=z"},
			not: []string{"GOFLAGS=", "SECRET=no", "STUB_JSON=", "GH_TOKEN="}},
		{name: "an answer", c: probeCase{answer: "{}"}, has: []string{"STUB_JSON={}"}},
		{name: "a count", c: probeCase{count: "1x"}, has: []string{"ZIZMOR_SELF_REPOSITORY_FINDINGS=1x"}, not: []string{"ZIZMOR_SELF_REPOSITORY_FINDINGS=0"}},
		{name: "an empty count", c: probeCase{count: emptyCount}, has: []string{"ZIZMOR_SELF_REPOSITORY_FINDINGS="}, not: []string{"ZIZMOR_SELF_REPOSITORY_FINDINGS=0", "ZIZMOR_SELF_REPOSITORY_FINDINGS=" + emptyCount}},
		{name: "the variables of the case", c: probeCase{vars: []string{"GH_TOKEN=first", "GITHUB_TOKEN="}}, has: []string{"GH_TOKEN=first", "GITHUB_TOKEN="}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := h.stubEnv(tc.c, "/rec")
			for _, want := range tc.has {
				if !slices.Contains(got, want) {
					t.Errorf("environment lacks %q: %q", want, got)
				}
			}
			for _, bad := range tc.not {
				if slices.Contains(got, bad) {
					t.Errorf("environment has %q: %q", bad, got)
				}
			}
		})
	}
	// The case's own variables come last, so that they win over the hostile ones.
	got := h.stubEnv(probeCase{vars: []string{"ZIZMOR_OFFLINE=false"}}, "/rec")
	if got[len(got)-1] != "ZIZMOR_OFFLINE=false" {
		t.Errorf("the case's variable is not last: %q", got)
	}
	if got[0] != "PATH=/scratch/stub:/usr/bin" {
		t.Errorf("the stub is not first on the PATH: %q", got[0])
	}
}

func TestRealEnvironmentHasNoToken(t *testing.T) {
	env, _, _ := testEnvironment(t, nil, &fakeRunner{})
	h := &harness{env: env}
	got := h.realEnv()
	if want := []string{"KEPT=1", "GH_TOKEN_X=kept"}; !slices.Equal(got, want) {
		t.Errorf("realEnv = %q, want %q", got, want)
	}
}

// Run under the name zizmor, the program is the stub and nothing else.
func TestRunAsTheStub(t *testing.T) {
	dir := t.TempDir()
	env, out, _ := stubEnvironment(map[string]string{stubOutVar: dir}, []string{"X=1"})
	if status := Run(context.Background(), []string{"/somewhere/zizmor", "--offline", "."}, env); status != exitOK {
		t.Fatalf("status %d", status)
	}
	call, err := readRecorded(dir)
	if err != nil || !slices.Equal(call.args, []string{"--offline", "."}) || out.String() != "[]\n" {
		t.Errorf("recorded %v, %v; stdout %q", call, err, out)
	}
}

func TestRunUsage(t *testing.T) {
	for _, tc := range []struct {
		name string
		argv []string
		vars map[string]string
		want string
	}{
		{name: "no arguments at all", argv: nil, vars: map[string]string{countVar: "1"}},
		{name: "an unknown flag", argv: []string{"zizmorprobe", "-bogus"}, vars: map[string]string{countVar: "1"}, want: "flag provided but not defined"},
		{name: "a stray argument", argv: []string{"zizmorprobe", "extra"}, vars: map[string]string{countVar: "1"}},
		{name: "no expected count", argv: []string{"zizmorprobe"}, vars: nil, want: "set " + countVar},
		{name: "an empty expected count", argv: []string{"zizmorprobe"}, vars: map[string]string{countVar: ""}, want: "set " + countVar},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRunner{}
			env, _, errOut := testEnvironment(t, tc.vars, f)
			if status := Run(context.Background(), tc.argv, env); status != exitError || !strings.Contains(errOut.String(), tc.want) {
				t.Errorf("status %d, stderr %q; want %d and %q", status, errOut, exitError, tc.want)
			}
			if len(f.calls) != 0 {
				t.Errorf("a program was run: %v", f.calls)
			}
		})
	}
}

func TestRunWithoutZizmor(t *testing.T) {
	for _, tc := range []struct {
		name     string
		required string
		status   int
		out      string
		err      string
	}{
		{name: "not required: skipped with a message", status: exitOK, out: "skipped, zizmor is not installed"},
		{name: "required: a failure", required: "1", status: exitProblem, err: "zizmor is not installed and ZIZMOR_REQUIRED is set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRunner{}
			env, out, errOut := testEnvironment(t, map[string]string{countVar: "13", requiredVar: tc.required}, f)
			env.LookPath = func(name string) (string, error) {
				if name != zizmorName {
					t.Errorf("looked up %q", name)
				}
				return "", exec.ErrNotFound
			}
			if status := Run(context.Background(), []string{"zizmorprobe"}, env); status != tc.status {
				t.Errorf("status %d, want %d", status, tc.status)
			}
			if !strings.Contains(out.String(), tc.out) || !strings.Contains(errOut.String(), tc.err) {
				t.Errorf("stdout %q, stderr %q; want %q and %q", out, errOut, tc.out, tc.err)
			}
			if len(f.calls) != 0 {
				t.Errorf("a program was run: %v", f.calls)
			}
		})
	}
}

func TestRunErrors(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, env *Environment, f *fakeRunner)
		want   string
	}{
		{name: "the executable is not known", change: func(_ *testing.T, env *Environment, _ *fakeRunner) {
			env.Executable = func() (string, error) { return "", boom }
		}, want: "boom"},
		{name: "no scratch directory", change: func(_ *testing.T, env *Environment, _ *fakeRunner) {
			env.MkdirTemp = func(string) (string, error) { return "", boom }
		}, want: "boom"},
		{name: "a scratch directory whose stub directory cannot be made", change: func(t *testing.T, env *Environment, _ *fakeRunner) {
			t.Helper()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "stub"), nil, fileMode); err != nil {
				t.Fatal(err)
			}
			env.MkdirTemp = func(string) (string, error) { return dir, nil }
		}, want: "not a directory"},
		{name: "a scratch directory that has the stub already", change: func(t *testing.T, env *Environment, _ *fakeRunner) {
			t.Helper()
			dir := t.TempDir()
			if err := installStub(filepath.Join(dir, "stub"), "/x"); err != nil {
				t.Fatal(err)
			}
			env.MkdirTemp = func(string) (string, error) { return dir, nil }
		}, want: "file exists"},
		{name: "a scratch tree that is in the way", change: func(t *testing.T, env *Environment, _ *fakeRunner) {
			t.Helper()
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "1"), nil, fileMode); err != nil {
				t.Fatal(err)
			}
			env.MkdirTemp = func(string) (string, error) { return dir, nil }
		}, want: "file exists"},
		{name: "a git that cannot run", change: func(_ *testing.T, _ *Environment, f *fakeRunner) { f.gitErr = boom }, want: "git init in the scratch tree: boom"},
		{name: "a git that fails", change: func(_ *testing.T, _ *Environment, f *fakeRunner) { f.gitState = 128 }, want: "exit 128: git says no"},
		{name: "a script that cannot run", change: func(_ *testing.T, _ *Environment, f *fakeRunner) {
			f.script = func(int, call) (string, int, error) { return "", 0, boom }
		}, want: "control: the copy of .github/ alone is clean: boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRunner{script: func(int, call) (string, int, error) { return "", 0, nil }}
			env, _, errOut := testEnvironment(t, map[string]string{countVar: "13"}, f)
			tc.change(t, &env, f)
			if status := Run(context.Background(), []string{"zizmorprobe", "-root", repoRoot}, env); status != exitError || !strings.Contains(errOut.String(), tc.want) {
				t.Errorf("status %d, stderr %q; want %d and %q", status, errOut, exitError, tc.want)
			}
		})
	}
}

func TestHarnessErrors(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name  string
		root  string
		cases []probeCase
		setup func(t *testing.T, h *harness)
		want  string
	}{
		{name: "a root without .github", root: t.TempDir(), cases: []probeCase{{name: "c", want: passes}}, want: "no such file"},
		{name: "a fixture that fails", root: repoRoot, cases: []probeCase{{name: "c", setup: []fixture{addFinding, func(tree) error { return boom }}, want: passes}},
			want: "c: fixture 2: boom"},
		{name: "a record directory that is in the way", root: repoRoot, cases: []probeCase{{name: "c", stub: true, want: passes}}, setup: func(t *testing.T, h *harness) {
			t.Helper()
			if err := os.WriteFile(h.recordDir(1), nil, fileMode); err != nil {
				t.Fatal(err)
			}
		}, want: "file exists"},
		{name: "a stub run whose script cannot run", root: repoRoot, cases: []probeCase{{name: "c", stub: true, want: passes}}, setup: func(_ *testing.T, h *harness) {
			h.env.Runner = func(_ context.Context, _ string, _ []string, name string, _ ...string) (string, int, error) {
				if name == gitCommand {
					return "", 0, nil
				}
				return "", 0, boom
			}
		}, want: "c: boom"},
		{name: "a stub run that records nothing", root: repoRoot, cases: []probeCase{{name: "c", stub: true, want: passes}}, want: "no such file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRunner{script: func(int, call) (string, int, error) { return "", 0, nil }}
			env, _, errOut := testEnvironment(t, nil, f)
			h, err := newHarness(env, tc.root, "13", "/bin/probe")
			if err != nil {
				t.Fatal(err)
			}
			defer h.close()
			if tc.setup != nil {
				tc.setup(t, h)
			}
			if status := h.runAll(context.Background(), tc.cases); status != exitError || !strings.Contains(errOut.String(), tc.want) {
				t.Errorf("status %d, stderr %q; want %d and %q", status, errOut, exitError, tc.want)
			}
		})
	}
}

func TestHarnessKeepsGoingAfterAFailedProbeAndCountsThem(t *testing.T) {
	cases := []probeCase{
		{name: "first", want: passes},
		{name: "second", want: fails("marker")},
		{name: "third", want: passes},
		{name: "fourth", want: fails("marker")},
	}
	answers := []struct {
		out    string
		status int
	}{{"", 0}, {"line one\nline two\n", 1}, {"line one\nline two\n", 1}, {"marker\n", 1}}
	status, out, errOut := runCases(t, cases, func(n int, _ call) (string, int, error) { return answers[n].out, answers[n].status, nil })
	if status != exitProblem {
		t.Fatalf("status %d, want %d", status, exitProblem)
	}
	// subtests: the expectations are substrings of one run's output, not cases
	for _, want := range []string{
		"ok    first\n",
		"FAIL  second: exit 1, wanted a failure that mentions marker\n        line one\n        line two\n",
		"FAIL  third: exit 1, wanted a pass\n        line one\n        line two\n",
		"ok    fourth\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(errOut, "2 probe(s) failed") {
		t.Errorf("stderr = %q, want the number of failed probes", errOut)
	}
}

// The stub probes, run for real: the real script, the real stub (this test binary, run as zizmor) and the real
// go tool for the verdict. They need no zizmor and no network.
func TestStubProbesAgainstTheRealScript(t *testing.T) {
	// subtests: a precondition check, one skip per missing tool
	for _, tool := range []string{"git", "sh", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("no %s", tool)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	env := ProcessEnvironment()
	env.Stdout, env.Stderr = &out, &errOut
	h, err := newHarness(env, repoRoot, "13", exe)
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	if status := h.runAll(context.Background(), stubCases()); status != exitOK {
		t.Fatalf("status %d\n%s\n%s", status, &out, &errOut)
	}
	if got := strings.Count(out.String(), "ok    "); got != len(stubCases()) {
		t.Errorf("%d probes passed, want %d:\n%s", got, len(stubCases()), &out)
	}
}
