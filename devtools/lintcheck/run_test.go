package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repo builds a root with the configuration and a fixture module that holds one file and an expected list.
func repo(t *testing.T, expected string) string {
	t.Helper()
	root := t.TempDir()
	fixtures := filepath.Join(root, defaultFixtures)
	if err := os.MkdirAll(filepath.Join(fixtures, "model"), 0o750); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		configName:                           "version: \"2\"\n",
		defaultFixtures + "/go.mod":          "module example\n",
		defaultFixtures + "/model/a.go":      "package model\n",
		defaultFixtures + "/" + expectedName: expected,
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// fakeLinter is a linterRunner that checks what it was given and writes a canned report.
func fakeLinter(t *testing.T, report string, runErr error) linterRunner {
	t.Helper()
	return func(bin, dir, reportPath, linters string) error {
		if bin != "the-linter" || linters != defaultLinters {
			t.Errorf("run with bin %q, linters %q", bin, linters)
		}
		// The linter runs next to a copy of the configuration and the fixtures: the paths of the
		// exclusions are relative to the configuration.
		for _, name := range []string{configName, "go.mod", filepath.Join("model", "a.go")} {
			if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
				t.Errorf("%s is not in the working directory: %v", name, err)
			}
		}
		if runErr != nil {
			return runErr
		}
		return os.WriteFile(reportPath, []byte(report), 0o600)
	}
}

const oneIssue = `{"Issues":[{"FromLinter":"forbidigo","Text":"use of x","Pos":{"Filename":"model/a.go"}}]}`

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		expected   string
		report     string
		runErr     error
		args       []string
		wantStatus int
		wantOut    []string
		wantErr    []string
	}{
		{
			name: "as expected", expected: "model/a.go: forbidigo: use of x\n", report: oneIssue,
			wantStatus: exitOK, wantOut: []string{"1 findings, all expected"},
		},
		{
			name: "a rule no longer fires", expected: "model/a.go: forbidigo: use of x\n", report: `{"Issues":[]}`,
			wantStatus: exitDiffers, wantOut: []string{"missing", "model/a.go: forbidigo: use of x"},
		},
		{
			name: "a clean file is reported", expected: "# nothing\n", report: oneIssue,
			wantStatus: exitDiffers, wantOut: []string{"unexpected", "model/a.go: forbidigo: use of x"},
		},
		{
			name: "print", expected: "", report: oneIssue, args: []string{"-print"},
			wantStatus: exitOK, wantOut: []string{"model/a.go: forbidigo: use of x\n"},
		},
		{
			name: "the linter did not run", expected: "", runErr: errors.New("boom"),
			wantStatus: exitError, wantErr: []string{"boom"},
		},
		{
			name: "a bad expected list", expected: "not a finding\n", report: oneIssue,
			wantStatus: exitError, wantErr: []string{"line 1"},
		},
		{
			name: "a broken report", expected: "", report: "{", wantStatus: exitError, wantErr: []string{"parsing the linter's report"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := repo(t, tc.expected)
			var stdout, stderr bytes.Buffer
			args := append([]string{"-root", root, "-lint", "the-linter"}, tc.args...)
			got := run(args, &stdout, &stderr, fakeLinter(t, tc.report, tc.runErr))
			if got != tc.wantStatus {
				t.Errorf("status = %d, want %d (stdout %q, stderr %q)", got, tc.wantStatus, stdout.String(), stderr.String())
			}
			for _, want := range tc.wantOut {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout %q lacks %q", stdout.String(), want)
				}
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr %q lacks %q", stderr.String(), want)
				}
			}
		})
	}
}

func TestRunUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "no linter", args: nil, want: "-lint is required"},
		{name: "unknown flag", args: []string{"-nope"}, want: "flag provided but not defined"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			got := run(tc.args, &stdout, &stderr, func(_, _, _, _ string) error {
				t.Error("the linter must not run after a usage error")
				return nil
			})
			if got != exitError || !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("status %d, stderr %q; want %d and %q", got, stderr.String(), exitError, tc.want)
			}
		})
	}
}

func TestRunWithoutAnExpectedListOrConfigurationIsAnError(t *testing.T) {
	tests := []struct {
		name   string
		remove string
	}{
		{name: "no expected list", remove: defaultFixtures + "/" + expectedName},
		{name: "no configuration", remove: configName},
		{name: "no fixtures", remove: defaultFixtures},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := repo(t, "")
			if err := os.RemoveAll(filepath.Join(root, tc.remove)); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			got := run([]string{"-root", root, "-lint", "the-linter"}, &stdout, &stderr, func(_, _, reportPath, _ string) error {
				return os.WriteFile(reportPath, []byte(`{"Issues":[]}`), 0o600)
			})
			if got != exitError {
				t.Errorf("status = %d, want %d (stderr %q)", got, exitError, stderr.String())
			}
		})
	}
}

func TestExecLinterTreatsFindingsAsSuccessAndAnythingElseAsAFailure(t *testing.T) {
	script := func(status string) string {
		path := filepath.Join(t.TempDir(), "linter.sh")
		body := "#!/bin/sh\necho oops >&2\nexit " + status + "\n"
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil { //nolint:gosec // a test script must be executable
			t.Fatal(err)
		}
		return path
	}
	dir := t.TempDir()
	tests := []struct {
		name    string
		status  string
		wantErr bool
	}{
		{name: "clean", status: "0"},
		{name: "found something", status: "1"},
		{name: "a failure", status: "3", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := execLinter(script(tc.status), dir, filepath.Join(dir, "r.json"), defaultLinters)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want an error: %v", err, tc.wantErr)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "oops") {
				t.Errorf("err = %v, want the linter's stderr in it", err)
			}
		})
	}
	t.Run("not found", func(t *testing.T) {
		if err := execLinter(filepath.Join(dir, "absent"), dir, "r.json", defaultLinters); err == nil {
			t.Error("a linter that cannot start is an error")
		}
	})
}

func TestCopyTreeKeepsThePathsAndSkipsWhatIsNotAFile(t *testing.T) {
	src := t.TempDir()
	for path, content := range map[string]string{"a.go": "A", "model/b.go": "B", "internal/coverpolicy/c.go": "C"} {
		full := filepath.Join(src, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(src, "a.go"), filepath.Join(src, "link.go")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	dst := t.TempDir()
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"a.go": "A", "model/b.go": "B", "internal/coverpolicy/c.go": "C"} {
		got, err := os.ReadFile(filepath.Join(dst, path))
		if err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", path, got, err, want)
		}
	}
	if _, err := os.Lstat(filepath.Join(dst, "link.go")); err == nil {
		t.Error("a symbolic link was copied")
	}
	if err := copyTree(filepath.Join(src, "absent"), dst); err == nil {
		t.Error("a missing source must be an error")
	}
}
