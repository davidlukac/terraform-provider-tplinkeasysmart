package probe

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

// Runner runs a program in a directory and returns what it printed (standard output and error together) and
// its exit status. A nil env is the environment of this process. An error is for a program that could not be
// run at all (not found, a canceled context); a program that exits with a status is a status.
type Runner func(ctx context.Context, dir string, env []string, name string, args ...string) (output string, status int, err error)

// execRunner is the Runner that runs the program for real.
func execRunner(ctx context.Context, dir string, env []string, name string, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // the script of this repository, and git
	cmd.Dir = dir
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", 0, ctxErr
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return out.String(), exitErr.ExitCode(), nil
	}
	return out.String(), 0, err
}
