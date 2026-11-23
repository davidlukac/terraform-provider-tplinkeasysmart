package probe

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The names the stub zizmor and the probes agree on.
const (
	// stubName is the name the program is run under to behave as zizmor.
	stubName = "zizmor"
	// stubOutVar names the directory the stub records its call in; stubJSONVar the answer it prints.
	stubOutVar  = "STUB_OUT"
	stubJSONVar = "STUB_JSON"
	// recordedArgs and recordedEnv are the files of that directory: the arguments, one per line, and the
	// environment, one entry per line.
	recordedArgs = "args"
	recordedEnv  = "env"
	// emptyAnswer is what the stub prints when no answer is set: no findings.
	emptyAnswer = "[]"
)

// isStub reports whether the program was started under the name of zizmor.
func isStub(argv0 string) bool { return filepath.Base(argv0) == stubName }

// runStub is the stub zizmor: it records its arguments and its environment in the directory named by
// STUB_OUT and prints the answer named by STUB_JSON (an empty list when there is none), like zizmor with
// no findings. The probes put it first on the PATH, to see how scripts/zizmor.sh calls zizmor.
func runStub(args []string, env Environment) int {
	dir := env.Getenv(stubOutVar)
	if dir == "" {
		_, _ = fmt.Fprintln(env.Stderr, "zizmor stub: "+stubOutVar+" is not set")
		return exitError
	}
	if err := os.WriteFile(filepath.Join(dir, recordedArgs), []byte(strings.Join(args, "\n")+"\n"), fileMode); err != nil { //nolint:gosec // the directory the probes name
		_, _ = fmt.Fprintln(env.Stderr, "zizmor stub:", err)
		return exitError
	}
	if err := os.WriteFile(filepath.Join(dir, recordedEnv), []byte(strings.Join(env.Environ(), "\n")+"\n"), fileMode); err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "zizmor stub:", err)
		return exitError
	}
	answer := env.Getenv(stubJSONVar)
	if answer == "" {
		answer = emptyAnswer
	}
	return writeLine(env.Stdout, answer)
}

// writeLine prints a line and returns the exit status for it.
func writeLine(w io.Writer, line string) int {
	if _, err := io.WriteString(w, line+"\n"); err != nil {
		return exitError
	}
	return exitOK
}

// installStub makes dir hold a program named zizmor that is this program: a link to the executable.
func installStub(dir, executable string) error {
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return err
	}
	return os.Symlink(executable, filepath.Join(dir, stubName))
}

// recorded is what the stub saw of one call.
type recorded struct {
	args []string
	env  []string
}

// readRecorded reads the call the stub recorded in dir.
func readRecorded(dir string) (recorded, error) {
	args, err := readLines(filepath.Join(dir, recordedArgs))
	if err != nil {
		return recorded{}, err
	}
	env, err := readLines(filepath.Join(dir, recordedEnv))
	return recorded{args: args, env: env}, err
}

// readLines reads a file as lines, without the empty one a final newline leaves.
func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the directory the probes name
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"), nil
}
