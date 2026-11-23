package audit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/tools/zizmorverdict/verdict" //nolint:depguard // a build tool imports its own and its sibling tools' packages; the rule keeps product code from importing a tool
)

// Exit statuses of Run.
const (
	exitFail  = 1
	exitUsage = 2
)

// githubDir is the directory whose files are workflows and actions, under the root.
const githubDir = ".github"

// offlineMessage is printed when the audit runs offline.
const offlineMessage = "no GH_TOKEN: offline audits only"

// maxListedLinks is the most symbolic links a refusal lists.
const maxListedLinks = 20

// Runner runs a command in a directory with the environment the command carries, writes what it prints to the
// two writers, and returns its exit status. An error is for a program that could not be run at all (not found,
// a canceled context); a program that exits with a status is a status.
type Runner func(ctx context.Context, dir string, cmd Command, stdout, stderr io.Writer) (status int, err error)

// Environment is what Run takes from the process, so that the tests can replace it.
type Environment struct {
	Stdout, Stderr io.Writer
	Environ        func() []string
	Getenv         func(string) string
	Runner         Runner
}

// ProcessEnvironment is the environment of this process.
func ProcessEnvironment() Environment {
	return Environment{Stdout: os.Stdout, Stderr: os.Stderr, Environ: os.Environ, Getenv: os.Getenv, Runner: execRunner}
}

// usage is what Run prints for a command line it does not understand.
const usage = "usage: zizmorrun ROOT EXPECTED (EXPECTED: the number of self-repository findings the workflows may carry)"

// Run is the whole command: args are the command line without the program name, the root of the repository
// to audit and the expected number of `self-repository` findings. It refuses a symbolic link under .github/,
// runs zizmor in the root with the command Build gives, and prints the verdict of package verdict on its
// answer. It returns 0 when the audit passes, 1 when it does not (a refused link, a failed or missing zizmor,
// an answer that is not exactly the expected findings, an expected count that is not a plain non-negative
// integer), and 2 for a command line that is not two arguments. The expected count is checked before anything
// is run, and nothing is run when a link is refused.
func Run(ctx context.Context, args []string, env Environment) int {
	if len(args) != 2 {
		_, _ = io.WriteString(env.Stderr, usage+"\n")
		return exitUsage
	}
	root, expected := args[0], args[1]
	if _, err := verdict.ParseExpected(expected); err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "zizmor: "+err.Error())
		return exitFail
	}
	if status := refuseLinks(root, env.Stderr); status != 0 {
		return status
	}
	token := Token(env.Getenv)
	if token == "" {
		_, _ = io.WriteString(env.Stdout, offlineMessage+"\n")
	}
	// One byte over the verdict's limit is all it needs to refuse an answer that is too large: any larger cap gives the
	// same verdict (an equivalent mutant of the constant), it only keeps more of the answer in memory.
	answer := &capped{limit: verdict.MaxAnswer + 1}
	log := &lineEscaper{out: env.Stderr}
	status, err := env.Runner(ctx, root, Build(token, env.Environ()), answer, log)
	log.Close()
	switch {
	case err != nil:
		_, _ = fmt.Fprintln(env.Stderr, "zizmor: it could not be run:", strconv.QuoteToASCII(err.Error()))
		return exitFail
	case status != 0:
		_, _ = fmt.Fprintf(env.Stderr, "zizmor: it failed with exit status %d\n", status)
		return exitFail
	}
	return verdict.Run([]string{expected}, bytes.NewReader(answer.data), env.Stdout, env.Stderr)
}

// refuseLinks lists the symbolic links under .github/ of the root on errOut and returns the exit status for
// them: 0 when there is none. The directory that cannot be read is a failure too.
func refuseLinks(root string, errOut io.Writer) int {
	links, err := findLinks(root)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "zizmor: the files under "+githubDir+"/ could not be listed:", strconv.QuoteToASCII(err.Error()))
		return exitFail
	}
	if len(links) == 0 {
		return 0
	}
	_, _ = io.WriteString(errOut, "symbolic links under "+githubDir+"/ are not allowed:\n")
	for i, link := range links {
		if i == maxListedLinks {
			_, _ = fmt.Fprintf(errOut, "and %d more\n", len(links)-maxListedLinks)
			break
		}
		_, _ = io.WriteString(errOut, strconv.QuoteToASCII(link)+"\n")
	}
	return exitFail
}

// findLinks returns the paths, relative to the root, of the symbolic links at or under .github/ (the directory
// itself included, as a link to it would make the audit follow it); links are not followed.
func findLinks(root string) ([]string, error) {
	var links []string
	err := filepath.WalkDir(filepath.Join(root, githubDir), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			links = append(links, githubDir+path[len(filepath.Join(root, githubDir)):])
		}
		return nil
	})
	return links, err
}

// capped is a writer that keeps the first limit bytes and drops the rest, so that an answer of any size ends: the
// verdict refuses one over its own limit.
type capped struct {
	data  []byte
	limit int
}

// Write keeps what fits and reports all of p as written.
func (c *capped) Write(p []byte) (int, error) {
	room := c.limit - len(c.data) // never negative: the data never grows past the limit
	c.data = append(c.data, p[:min(room, len(p))]...)
	return len(p), nil
}

// execRunner is the Runner that runs the program for real.
func execRunner(ctx context.Context, dir string, c Command, stdout, stderr io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, c.Name, c.Args...) //nolint:gosec // the pinned tool, found on the PATH, with the fixed options of Build
	cmd.Dir = dir
	cmd.Env = c.Env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.ExitCode(), nil
	}
	return 0, err
}
