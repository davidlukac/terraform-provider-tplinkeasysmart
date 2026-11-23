//go:build !windows

package probe

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

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
		dir    string
		status int
		out    string
	}{
		{name: "standard output and error together, and a status", script: "echo out; echo err >&2; exit 3", status: 3, out: "out\nerr\n"},
		{name: "a pass", script: "echo fine", status: 0, out: "fine\n"},
		{name: "only the environment given", script: `echo "[$ONLY][${HOME-unset}]"`, env: []string{"ONLY=1"}, status: 0, out: "[1][unset]\n"},
		{name: "the directory given", script: "pwd", dir: dir, status: 0, out: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, status, err := execRunner(context.Background(), tc.dir, tc.env, sh, "-c", tc.script)
			if err != nil || status != tc.status {
				t.Fatalf("status %d, %v; want %d", status, err, tc.status)
			}
			if tc.out != "" && out != tc.out {
				t.Errorf("output = %q, want %q", out, tc.out)
			}
			if tc.dir != "" && !strings.HasSuffix(strings.TrimSpace(out), dirBase(dir)) {
				t.Errorf("output = %q, want the directory %q", out, dir)
			}
		})
	}
}

// dirBase is the last element of a path.
func dirBase(path string) string { return path[strings.LastIndex(path, string(os.PathSeparator))+1:] }

func TestExecRunnerErrors(t *testing.T) {
	if _, _, err := execRunner(context.Background(), "", nil, "/nonexistent/program"); err == nil {
		t.Error("a program that does not exist is not an error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := execRunner(ctx, "", nil, "sh", "-c", "true"); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
}
