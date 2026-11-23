// Package probe proves that scripts/zizmor.sh, what `make zizmor` runs, reports a deliberate finding next to
// each input of the audited tree that its cases name. zizmor reads inputs from the tree it audits: a
// configuration file, an ignore file, an inline suppression comment, the files it can load, the files that
// links lead to, and where the files are. For each of those inputs a probe copies the repository's .github/
// into a scratch git repository, adds a workflow with a template injection (the finding) and the input, runs
// the script offline, and requires a failing exit that says why (the finding, or the reason the script
// gives). It is the library part of the command tools/zizmorprobe, a build tool of this repository.
//
// The cases are data (cases.go), the changes to a scratch tree are fixtures (fixtures.go), and each case
// names the words its refusal must say, so that a run that fails for another reason does not count. Two
// controls come first: the copy alone is clean, and the finding alone is reported, so that a probe that
// passes cannot be a probe that never ran. A fixture that changes nothing is an error. The probes on the
// allowed findings (one fewer, one more) stand next to the one that shows an ignore comment is not read.
//
// The probes of the command line and the environment run the script with a stub in place of zizmor, in an
// environment that holds only what the go tool needs and the variables that could change the audit: the stub
// is this program, run under the name zizmor, and records its arguments and its environment. They need no
// network and no zizmor.
//
// The probes run a POSIX shell script over symbolic links, so the tests of this package are not built for
// Windows.
//
// The program needs zizmor on the PATH, the pinned version (`make zizmor` prints how to install it). Without
// it the probes that need it say so and pass, unless ZIZMOR_REQUIRED is set, as it is in the CI jobs that install
// zizmor. ZIZMOR_SELF_REPOSITORY_FINDINGS is the number the Makefile holds.
package probe

import (
	"cmp"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Exit statuses of Run.
const (
	exitOK      = 0
	exitProblem = 1
	exitError   = 2
)

// The variables of the environment the probes read.
const (
	countVar    = "ZIZMOR_SELF_REPOSITORY_FINDINGS"
	requiredVar = "ZIZMOR_REQUIRED"
)

// The scripts and programs the probes run.
const (
	scriptPath = "scripts/zizmor.sh"
	gitCommand = "git"
	zizmorName = "zizmor"
)

// stubCount is the number of findings the script is told to expect in the stub runs, which answer none.
const stubCount = "0"

// passThrough are the variables of this process that the stub runs keep, because the go tool, which the script
// runs for the verdict, needs its caches and its toolchain settings.
var passThrough = []string{"HOME", "GOCACHE", "GOMODCACHE", "GOPATH", "GOROOT", "GOTOOLCHAIN", "GOFLAGS", "GOENV", "TMPDIR"}

// tokenVariables are removed from the environment of the runs with the real zizmor: they are offline.
var tokenVariables = []string{"GH_TOKEN", "GITHUB_TOKEN"}

// Environment is what Run takes from the process, so that the tests can replace it.
type Environment struct {
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	Environ        func() []string
	LookPath       func(string) (string, error)
	Executable     func() (string, error)
	MkdirTemp      func(pattern string) (string, error)
	Runner         Runner
}

// ProcessEnvironment is the environment of this process.
func ProcessEnvironment() Environment {
	return Environment{
		Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv, Environ: os.Environ,
		LookPath: exec.LookPath, Executable: os.Executable, Runner: execRunner,
		MkdirTemp: func(pattern string) (string, error) { return os.MkdirTemp("", pattern) },
	}
}

// Run is the whole command: argv is the command line with the program name, which makes the program the stub
// zizmor when it is zizmor. Otherwise it takes -root, the root of the repository (default: the current
// directory), runs every probe and prints one line for each. It exits 0 when every probe holds or when
// zizmor is not installed and not required, 1 when a probe fails or zizmor is required and missing, and 2 for
// a command line it does not understand or a scratch tree it could not make.
func Run(ctx context.Context, argv []string, env Environment) int {
	if len(argv) > 0 && isStub(argv[0]) {
		return runStub(argv[1:], env)
	}
	flags := flag.NewFlagSet("zizmorprobe", flag.ContinueOnError)
	flags.SetOutput(env.Stderr)
	root := flags.String("root", ".", "the root of the repository")
	if len(argv) == 0 || flags.Parse(argv[1:]) != nil || flags.NArg() != 0 {
		return exitError
	}
	count := env.Getenv(countVar)
	if count == "" {
		_, _ = fmt.Fprintln(env.Stderr, "zizmorprobe: set "+countVar+" to the number of self-repository findings the workflows may carry")
		return exitError
	}
	if _, err := env.LookPath(zizmorName); err != nil {
		return noZizmor(env)
	}
	exe, err := env.Executable()
	if err != nil {
		return failure(env, err)
	}
	h, err := newHarness(env, *root, count, exe)
	if err != nil {
		return failure(env, err)
	}
	defer h.close()
	return h.runAll(ctx, slices.Concat(scriptCases(), stubCases()))
}

// noZizmor is the result when zizmor is not installed.
func noZizmor(env Environment) int {
	if env.Getenv(requiredVar) != "" {
		_, _ = fmt.Fprintln(env.Stderr, "zizmorprobe: zizmor is not installed and "+requiredVar+" is set")
		return exitProblem
	}
	_, _ = fmt.Fprintln(env.Stdout, "zizmorprobe: skipped, zizmor is not installed (CI installs the pinned one; see make zizmor)")
	return exitOK
}

// failure reports an error that is not a failed probe.
func failure(env Environment, err error) int {
	_, _ = fmt.Fprintln(env.Stderr, "zizmorprobe:", err)
	return exitError
}

// harness runs the probes in a scratch directory.
type harness struct {
	env     Environment
	root    string
	count   string
	scratch string
	// sequence numbers the scratch trees.
	sequence int
}

// newHarness makes the scratch directory, with the stub zizmor in it.
func newHarness(env Environment, root, count, executable string) (*harness, error) {
	scratch, err := env.MkdirTemp("zizmorprobe-")
	if err != nil {
		return nil, err
	}
	h := &harness{env: env, root: root, count: count, scratch: scratch}
	if err := installStub(h.stubDir(), executable); err != nil {
		h.close()
		return nil, err
	}
	return h, nil
}

// close removes the scratch directory.
func (h *harness) close() { _ = os.RemoveAll(h.scratch) }

// stubDir is where the stub zizmor is.
func (h *harness) stubDir() string { return filepath.Join(h.scratch, "stub") }

// recordDir is where the stub records the call of the run in tree number n.
func (h *harness) recordDir(n int) string { return filepath.Join(h.scratch, "record-"+strconv.Itoa(n)) }

// fresh makes a scratch git repository that holds a copy of the repository's .github/.
func (h *harness) fresh(ctx context.Context) (tree, error) {
	h.sequence++
	dir := filepath.Join(h.scratch, strconv.Itoa(h.sequence))
	if err := os.Mkdir(dir, dirMode); err != nil {
		return tree{}, err
	}
	if err := copyDir(filepath.Join(h.root, ".github"), filepath.Join(dir, ".github")); err != nil {
		return tree{}, err
	}
	out, status, err := h.env.Runner(ctx, dir, nil, gitCommand, "init", "-q")
	if err != nil {
		return tree{}, fmt.Errorf("git init in the scratch tree: %w", err)
	}
	if status != 0 {
		return tree{}, fmt.Errorf("git init in the scratch tree: exit %d: %w", status, gitError(out))
	}
	return tree{dir: dir}, nil
}

// gitError is the output of a git that failed, as an error.
type gitError string

func (e gitError) Error() string { return strings.TrimSpace(string(e)) }

// result is what one run of the script did.
type result struct {
	status int
	output string
	call   recorded
}

// execute runs one case: a fresh tree, the fixtures, the script.
func (h *harness) execute(ctx context.Context, c probeCase) (result, error) {
	t, err := h.fresh(ctx)
	if err != nil {
		return result{}, err
	}
	for i, f := range c.setup {
		if err := f(t); err != nil {
			return result{}, fmt.Errorf("fixture %d: %w", i+1, err)
		}
	}
	script := filepath.Join(h.root, scriptPath)
	if !c.stub {
		out, status, err := h.env.Runner(ctx, "", h.realEnv(), script, t.dir)
		return result{status: status, output: out}, err
	}
	record := h.recordDir(h.sequence)
	if err := os.Mkdir(record, dirMode); err != nil {
		return result{}, err
	}
	out, status, err := h.env.Runner(ctx, "", h.stubEnv(c, record), script, t.dir)
	if err != nil {
		return result{}, err
	}
	res := result{status: status, output: out}
	if status == 0 {
		res.call, err = readRecorded(record)
	}
	return res, err
}

// realEnv is the environment of a run with the real zizmor: this process's, without a token, so that the
// audits run offline.
func (h *harness) realEnv() []string {
	return slices.DeleteFunc(h.env.Environ(), func(entry string) bool {
		name, _, _ := strings.Cut(entry, "=")
		return slices.Contains(tokenVariables, name)
	})
}

// stubEnv is the environment of a stub run, whose call is recorded in the directory record: the stub first on the PATH, what the go tool needs, the
// variables that could change the audit (every one set), and the variables of the case, which come last and
// so win.
func (h *harness) stubEnv(c probeCase, record string) []string {
	count := cmp.Or(c.count, stubCount)
	if c.count == emptyCount {
		count = ""
	}
	env := []string{"PATH=" + h.stubDir() + string(os.PathListSeparator) + h.env.Getenv("PATH")}
	for _, name := range passThrough {
		if value := h.env.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	env = append(env, stubOutVar+"="+record, countVar+"="+count)
	if c.answer != "" {
		env = append(env, stubJSONVar+"="+c.answer)
	}
	return slices.Concat(env, hostile, c.vars)
}

// judge compares what a run did with what the case wants, and returns what is wrong.
func judge(c probeCase, res result) []string {
	var problems []string
	switch {
	case c.want.pass && res.status != 0:
		problems = append(problems, fmt.Sprintf("exit %d, wanted a pass", res.status))
	case !c.want.pass && res.status == 0:
		problems = append(problems, "wanted a failure that mentions "+c.want.marker+", the script passed")
	case !c.want.pass && !strings.Contains(res.output, c.want.marker):
		problems = append(problems, fmt.Sprintf("exit %d, wanted a failure that mentions %s", res.status, c.want.marker))
	}
	problems = append(problems, outputProblems(c, res.output)...)
	if res.status == 0 {
		for _, a := range c.recorded {
			if !holds(a, res.call) {
				problems = append(problems, "the stub recorded a call that breaks: "+describe(a))
			}
		}
	}
	return problems
}

// outputProblems says what is wrong with the output of a run: a line that starts with a prefix the case
// forbids (white space before it does not count: every character `unicode.IsSpace` accepts), or text the
// case wants and the output lacks.
func outputProblems(c probeCase, output string) []string {
	var problems []string
	for _, prefix := range c.noLineStartsWith {
		for line := range strings.SplitSeq(output, "\n") {
			if strings.HasPrefix(strings.TrimLeftFunc(line, unicode.IsSpace), prefix) {
				problems = append(problems, fmt.Sprintf("a line of the output starts with %q: %q", prefix, line))
				break
			}
		}
	}
	for _, text := range c.outputHas {
		if !strings.Contains(output, text) {
			problems = append(problems, fmt.Sprintf("the output lacks %q", text))
		}
	}
	return problems
}

// holds reports whether the assertion is true of the recorded call.
func holds(a assertion, call recorded) bool {
	switch a.kind {
	case hasArg:
		return slices.Contains(call.args, a.value)
	case lacksArg:
		return !slices.Contains(call.args, a.value)
	case hasEnv:
		return slices.Contains(call.env, a.value)
	case lacksEnv:
		return !slices.ContainsFunc(call.env, func(entry string) bool { return strings.HasPrefix(entry, a.value+"=") })
	}
	return false
}

// describe is the assertion in words.
func describe(a assertion) string {
	switch a.kind {
	case hasArg:
		return "has the argument " + a.value
	case lacksArg:
		return "lacks the argument " + a.value
	case hasEnv:
		return "has the variable " + a.value
	case lacksEnv:
		return "lacks the variable " + a.value
	}
	return "unknown assertion"
}

// runAll runs the cases in order and prints one line for each.
func (h *harness) runAll(ctx context.Context, cases []probeCase) int {
	failures := 0
	for _, c := range cases {
		res, err := h.execute(ctx, c)
		if err != nil {
			return failure(h.env, fmt.Errorf("%s: %w", c.name, err))
		}
		problems := judge(c, res)
		if len(problems) == 0 {
			_, _ = fmt.Fprintln(h.env.Stdout, "ok    "+c.name)
			continue
		}
		failures++
		_, _ = fmt.Fprintln(h.env.Stdout, "FAIL  "+c.name+": "+strings.Join(problems, "; "))
		for line := range strings.SplitSeq(strings.TrimSuffix(res.output, "\n"), "\n") {
			_, _ = fmt.Fprintln(h.env.Stdout, "        "+line)
		}
		if c.control {
			_, _ = fmt.Fprintln(h.env.Stderr, "zizmorprobe: a control failed, the probes after it prove nothing")
			return exitProblem
		}
	}
	if failures > 0 {
		_, _ = fmt.Fprintf(h.env.Stderr, "zizmorprobe: %d probe(s) failed\n", failures)
		return exitProblem
	}
	_, _ = fmt.Fprintln(h.env.Stdout, "zizmorprobe: every case holds: each input it names is ignored or refused")
	return exitOK
}
