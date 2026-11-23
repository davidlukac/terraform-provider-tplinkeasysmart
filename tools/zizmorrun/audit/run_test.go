//go:build !windows

package audit

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/tools/zizmorverdict/verdict"
)

// call is one call of the fake runner.
type call struct {
	dir string
	cmd Command
}

// fakeRunner stands in for zizmor: it prints out, and returns status and err.
type fakeRunner struct {
	calls  []call
	out    string
	errOut string
	status int
	err    error
}

func (f *fakeRunner) run(_ context.Context, dir string, cmd Command, stdout, stderr io.Writer) (int, error) {
	f.calls = append(f.calls, call{dir: dir, cmd: cmd})
	_, _ = io.WriteString(stdout, f.out)
	_, _ = io.WriteString(stderr, f.errOut)
	return f.status, f.err
}

// repo makes a repository root with a .github/workflows directory and returns it.
func repo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

// run runs the command over a root with the fake and the environment given, and returns the status, the
// standard output and the standard error.
func run(t *testing.T, f *fakeRunner, vars map[string]string, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := Environment{
		Stdout: &out, Stderr: &errOut, Runner: f.run,
		Environ: func() []string { return []string{"KEPT=1"} },
		Getenv:  func(name string) string { return vars[name] },
	}
	status := Run(context.Background(), args, env)
	return status, out.String(), errOut.String()
}

const (
	noFindings   = "[]"
	oneAllowed   = `[{"ident":"self-repository"}]`
	otherFinding = `[{"ident":"template-injection","desc":"d"}]`
)

func TestRunJudgesTheAnswerAgainstTheExpectedNumber(t *testing.T) {
	for _, tc := range []struct {
		name     string
		answer   string
		expected string
		status   int
		out      string
		err      string
	}{
		{name: "none expected, none found", answer: noFindings, expected: "0", status: 0, out: "zizmor: no findings but the 0 allowed ones"},
		{name: "one expected, one found", answer: oneAllowed, expected: "1", status: 0, out: "zizmor: no findings but the 1 allowed ones"},
		{name: "the count is the one given, not a fixed one", answer: oneAllowed, expected: "13", status: 1, err: "1 of the allowed rule (self-repository), 0 of other rules; allowed exactly 13"},
		{name: "none expected, one found", answer: oneAllowed, expected: "0", status: 1, err: "allowed exactly 0"},
		{name: "another rule decides", answer: otherFinding, expected: "0", status: 1, err: "template-injection"},
		{name: "an answer that is not a list", answer: "{}", expected: "0", status: 1, err: "not a list"},
		{name: "no answer", answer: "", expected: "0", status: 1, err: "not a list"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRunner{out: tc.answer}
			status, out, errOut := run(t, f, nil, repo(t), tc.expected)
			if status != tc.status || !strings.Contains(out, tc.out) || !strings.Contains(errOut, tc.err) {
				t.Errorf("status %d, stdout %q, stderr %q; want %d, %q, %q", status, out, errOut, tc.status, tc.out, tc.err)
			}
			if tc.status == 0 && errOut != "" {
				t.Errorf("stderr on a pass: %q", errOut)
			}
			if len(f.calls) != 1 {
				t.Errorf("zizmor was run %d times, want once", len(f.calls))
			}
		})
	}
}

func TestRunRunsZizmorInTheRootWithTheBuiltCommand(t *testing.T) {
	for _, tc := range []struct {
		name    string
		vars    map[string]string
		token   string
		message bool
	}{
		{name: "no token: offline, and it says so", vars: nil, token: "", message: true},
		{name: "empty token variables count as no token", vars: map[string]string{"GH_TOKEN": "", "GITHUB_TOKEN": ""}, token: "", message: true},
		{name: "GH_TOKEN", vars: map[string]string{"GH_TOKEN": "first"}, token: "first"},
		{name: "GITHUB_TOKEN alone", vars: map[string]string{"GITHUB_TOKEN": "second"}, token: "second"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := repo(t)
			f := &fakeRunner{out: noFindings}
			status, out, _ := run(t, f, tc.vars, root, "0")
			if status != 0 || len(f.calls) != 1 {
				t.Fatalf("status %d after %d calls", status, len(f.calls))
			}
			want := Build(tc.token, []string{"KEPT=1"})
			if got := f.calls[0]; got.dir != root || got.cmd.Name != want.Name || !slices.Equal(got.cmd.Args, want.Args) || !slices.Equal(got.cmd.Env, want.Env) {
				t.Errorf("call = %+v, want dir %q and %+v", got, root, want)
			}
			if got := strings.Contains(out, "no GH_TOKEN: offline audits only\n"); got != tc.message {
				t.Errorf("stdout = %q, the offline message printed: %v, want %v", out, got, tc.message)
			}
		})
	}
}

func TestRunRefusesSymbolicLinksUnderGithub(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root string)
		list  []string
	}{
		{name: "a link to a workflow", setup: func(t *testing.T, root string) {
			t.Helper()
			mustSymlink(t, "../../elsewhere.yml", filepath.Join(root, ".github", "workflows", "zz.yml"))
		}, list: []string{`".github/workflows/zz.yml"`}},
		{name: "a link that leads nowhere", setup: func(t *testing.T, root string) {
			t.Helper()
			mustSymlink(t, "/nonexistent", filepath.Join(root, ".github", "dangling"))
		}, list: []string{`".github/dangling"`}},
		{name: "a link to a directory, not followed", setup: func(t *testing.T, root string) {
			t.Helper()
			mustSymlink(t, "../ci", filepath.Join(root, ".github", "actions-linked"))
		}, list: []string{`".github/actions-linked"`}},
		{name: ".github itself a link", setup: func(t *testing.T, root string) {
			t.Helper()
			if err := os.RemoveAll(filepath.Join(root, ".github")); err != nil {
				t.Fatal(err)
			}
			mustSymlink(t, "elsewhere", filepath.Join(root, ".github"))
		}, list: []string{`".github"`}},
		{name: "a name that tries to start a workflow command", setup: func(t *testing.T, root string) {
			t.Helper()
			mustSymlink(t, "x", filepath.Join(root, ".github", "a\n::error::b"))
		}, list: []string{`".github/a\n::error::b"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := repo(t)
			tc.setup(t, root)
			f := &fakeRunner{out: noFindings}
			status, out, errOut := run(t, f, nil, root, "0")
			if status != 1 || len(f.calls) != 0 {
				t.Fatalf("status %d after %d runs, want a refusal before zizmor is run", status, len(f.calls))
			}
			if !strings.HasPrefix(errOut, "symbolic links under .github/ are not allowed:\n") || out != "" {
				t.Errorf("stdout %q, stderr %q", out, errOut)
			}
			for _, want := range tc.list {
				if !strings.Contains(errOut, want+"\n") {
					t.Errorf("stderr lacks the line %s: %q", want, errOut)
				}
			}
			for line := range strings.SplitSeq(errOut, "\n") {
				if strings.HasPrefix(line, "::") {
					t.Errorf("a line starts a workflow command: %q", line)
				}
			}
		})
	}
}

func mustSymlink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func TestRunAllowsLinksOutsideGithubAndRegularFiles(t *testing.T) {
	root := repo(t)
	mustSymlink(t, ".github", filepath.Join(root, "elsewhere"))
	if err := os.WriteFile(filepath.Join(root, ".github", "workflows", "a.yml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	f := &fakeRunner{out: noFindings}
	if status, _, errOut := run(t, f, nil, root, "0"); status != 0 {
		t.Errorf("status %d: %s", status, errOut)
	}
}

func TestRunListsAtMostTwentyLinks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		more  string
	}{
		{name: "at the limit", count: maxListedLinks},
		{name: "one over", count: maxListedLinks + 1, more: "and 1 more\n"},
		{name: "many over", count: 50, more: "and 30 more\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := repo(t)
			for i := range tc.count {
				mustSymlink(t, "x", filepath.Join(root, ".github", "l"+string(rune('a'+i%26))+string(rune('a'+i/26))))
			}
			_, _, errOut := run(t, &fakeRunner{}, nil, root, "0")
			lines := strings.Count(errOut, "\n") - 1
			if want := min(tc.count, maxListedLinks) + btoi(tc.more != ""); lines != want {
				t.Errorf("%d lines listed, want %d:\n%s", lines, want, errOut)
			}
			if tc.more != "" && !strings.HasSuffix(errOut, tc.more) {
				t.Errorf("stderr does not end with %q: %q", tc.more, errOut)
			}
		})
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestRunFailsWithoutAGithubDirectory(t *testing.T) {
	f := &fakeRunner{out: noFindings}
	status, _, errOut := run(t, f, nil, t.TempDir(), "0")
	if status != 1 || len(f.calls) != 0 || !strings.Contains(errOut, "could not be listed") {
		t.Errorf("status %d, %d runs, stderr %q", status, len(f.calls), errOut)
	}
}

func TestRunRefusesBadArgumentsBeforeRunningAnything(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		status int
		err    string
	}{
		{name: "no arguments", args: nil, status: 2, err: "usage: zizmorrun ROOT EXPECTED"},
		{name: "one argument", args: []string{"."}, status: 2, err: "usage: zizmorrun ROOT EXPECTED"},
		{name: "three arguments", args: []string{".", "0", "x"}, status: 2, err: "usage: zizmorrun ROOT EXPECTED"},
		{name: "an empty count", args: []string{".", ""}, status: 1, err: "the expected count is empty"},
		{name: "a negative count", args: []string{".", "-1"}, status: 1, err: "not a plain non-negative integer"},
		{name: "a count with a space", args: []string{".", " 1"}, status: 1, err: "not a plain non-negative integer"},
		{name: "a count that is not a number", args: []string{".", "1x"}, status: 1, err: "not a plain non-negative integer"},
		{name: "a leading zero", args: []string{".", "01"}, status: 1, err: "leading zero"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeRunner{out: noFindings}
			status, out, errOut := run(t, f, nil, tc.args...)
			if status != tc.status || out != "" || !strings.Contains(errOut, tc.err) || len(f.calls) != 0 {
				t.Errorf("status %d, stdout %q, stderr %q, %d runs; want %d and %q before any run", status, out, errOut, len(f.calls), tc.status, tc.err)
			}
		})
	}
}

// A zizmor that fails is a failure, whatever it printed: a tool that could not do its work does not get to
// have its answer read. The cases below print an answer the verdict would accept for the count 0 (`[]`),
// so that only the exit status can fail them, and one that prints nothing, which the verdict fails by itself:
// for the real zizmor, which prints no JSON when it fails, the two checks agree.
func TestRunFailsWhenZizmorFails(t *testing.T) {
	for _, tc := range []struct {
		name   string
		f      *fakeRunner
		stderr string
	}{
		{name: "an exit status and an answer the verdict would accept", f: &fakeRunner{out: noFindings, status: 1, errOut: "failed to load x.yml\n"}, stderr: "exit status 1"},
		{name: "an exit status and no answer", f: &fakeRunner{status: 2, errOut: "failed to load x.yml\n"}, stderr: "exit status 2"},
		{name: "a program that cannot be run", f: &fakeRunner{err: errors.New("not found\n::error::x")}, stderr: `could not be run: "not found\n::error::x"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, out, errOut := run(t, tc.f, nil, repo(t), "0")
			if status != 1 || out != offlineMessage+"\n" || !strings.Contains(errOut, tc.stderr) {
				t.Errorf("status %d, stdout %q, stderr %q; want a failure that says %q", status, out, errOut, tc.stderr)
			}
			if strings.Contains(errOut, "no findings") || tc.f.errOut != "" && !strings.Contains(errOut, tc.f.errOut) {
				t.Errorf("stderr = %q: the verdict was given, or the output of zizmor was lost", errOut)
			}
		})
	}
}

func TestRunFailsOnAnAnswerOverTheLimit(t *testing.T) {
	f := &fakeRunner{out: "[]" + strings.Repeat(" ", verdict.MaxAnswer)}
	status, _, errOut := run(t, f, nil, repo(t), "0")
	if status != 1 || !strings.Contains(errOut, "larger than") {
		t.Errorf("status %d, stderr %q", status, errOut)
	}
}

func TestCappedKeepsTheFirstBytesAndReportsAllWritten(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limit  int
		writes []string
		want   string
	}{
		{name: "under the limit", limit: 10, writes: []string{"ab", "cd"}, want: "abcd"},
		{name: "exactly the limit", limit: 4, writes: []string{"ab", "cd"}, want: "abcd"},
		{name: "one over", limit: 4, writes: []string{"abcde"}, want: "abcd"},
		{name: "a write that crosses the limit", limit: 3, writes: []string{"ab", "cd", "ef"}, want: "abc"},
		{name: "nothing", limit: 3, writes: nil, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &capped{limit: tc.limit}
			for _, w := range tc.writes {
				if n, err := c.Write([]byte(w)); n != len(w) || err != nil {
					t.Fatalf("Write(%q) = %d, %v; want all written", w, n, err)
				}
			}
			if string(c.data) != tc.want {
				t.Errorf("data = %q, want %q", c.data, tc.want)
			}
		})
	}
}

func TestExecRunner(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	for _, tc := range []struct {
		name   string
		script string
		env    []string
		status int
		out    string
		errOut string
	}{
		{name: "the two streams apart, and a status", script: "echo out; echo err >&2; exit 3", status: 3, out: "out\n", errOut: "err\n"},
		{name: "a pass", script: "echo fine", status: 0, out: "fine\n"},
		{name: "only the environment given", script: `echo "[$ONLY][${HOME-unset}]"`, env: []string{"ONLY=1"}, status: 0, out: "[1][unset]\n"},
		{name: "the directory given", script: "pwd -P", status: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			status, err := execRunner(context.Background(), dir, Command{Name: sh, Args: []string{"-c", tc.script}, Env: tc.env}, &out, &errOut)
			if err != nil || status != tc.status || errOut.String() != tc.errOut {
				t.Fatalf("status %d, %v, stderr %q; want %d, stderr %q", status, err, &errOut, tc.status, tc.errOut)
			}
			if tc.out != "" && out.String() != tc.out {
				t.Errorf("stdout = %q, want %q", &out, tc.out)
			}
			if tc.name == "the directory given" {
				want, _ := filepath.EvalSymlinks(dir)
				if strings.TrimSpace(out.String()) != want {
					t.Errorf("ran in %q, want %q", out.String(), want)
				}
			}
		})
	}
	var sink bytes.Buffer
	if _, err := execRunner(context.Background(), dir, Command{Name: "/nonexistent/program"}, &sink, &sink); err == nil {
		t.Error("a program that does not exist is not an error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := execRunner(ctx, dir, Command{Name: sh, Args: []string{"-c", "true"}}, &sink, &sink); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}

func TestProcessEnvironmentHoldsTheProcess(t *testing.T) {
	env := ProcessEnvironment()
	if env.Stdout != os.Stdout || env.Stderr != os.Stderr || env.Runner == nil || env.Environ == nil || env.Getenv == nil {
		t.Errorf("ProcessEnvironment() = %+v", env)
	}
}

// zizmor's own output reaches the log only through the escaper: a file name with a new line and a workflow
// command in what zizmor wrote on its standard error does not start a line of ours with "::".
func TestRunEscapesWhatZizmorWritesOnItsStandardError(t *testing.T) {
	hostile := "INFO audit: completed ./.github/workflows/a\n::warning::x.yml\n\x1b[31m::error::y"
	for _, tc := range []struct {
		name   string
		f      *fakeRunner
		status int
	}{
		{name: "a run that passes", f: &fakeRunner{out: noFindings, errOut: hostile}, status: 0},
		{name: "a run that fails", f: &fakeRunner{out: noFindings, errOut: hostile, status: 1}, status: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, _, errOut := run(t, tc.f, nil, repo(t), "0")
			if status != tc.status {
				t.Fatalf("status %d, want %d", status, tc.status)
			}
			for line := range strings.SplitSeq(errOut, "\n") {
				if strings.HasPrefix(line, "::") || strings.ContainsRune(line, '\x1b') {
					t.Errorf("an unsafe line reached the log: %q", line)
				}
			}
			// subtests: the expectations are substrings of one run's output, not cases
			for _, want := range []string{"zizmor: INFO audit: completed ./.github/workflows/a\n", "zizmor: ::warning::x.yml\n", "zizmor: \\x1b[31m::error::y\n"} {
				if !strings.Contains(errOut, want) {
					t.Errorf("stderr lacks %q: %q", want, errOut)
				}
			}
		})
	}
}
