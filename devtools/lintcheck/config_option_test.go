package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// -config names another configuration, which the linter finds in the working directory under the
// usual name, and -linters "" leaves the choice of linters to it: the run that holds the dead-code
// configuration to its fixture depends on both.
func TestRunHoldsAnotherConfigurationToItsOwnFixtures(t *testing.T) {
	const other = "version: \"2\"\nrun:\n  tests: false\n"
	tests := []struct {
		name        string
		args        []string
		wantConfig  string
		wantLinters string
	}{
		{"the default configuration and linters", nil, "version: \"2\"\n", defaultLinters},
		{"another configuration", []string{"-config", "other.yml"}, other, defaultLinters},
		{"no limit on the linters", []string{"-linters", ""}, "version: \"2\"\n", ""},
		{"another configuration and no limit", []string{"-config", "other.yml", "-linters", ""}, other, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := repo(t, "# nothing\n")
			if err := os.WriteFile(filepath.Join(root, "other.yml"), []byte(other), 0o600); err != nil {
				t.Fatal(err)
			}
			var gotConfig, gotLinters string
			lint := func(_, dir, reportPath, linters string) error {
				text, err := os.ReadFile(filepath.Join(dir, configName))
				if err != nil {
					t.Error(err)
				}
				gotConfig, gotLinters = string(text), linters
				return os.WriteFile(reportPath, []byte(`{"Issues":[]}`), 0o600)
			}
			var stdout, stderr bytes.Buffer
			args := append([]string{"-root", root, "-lint", "the-linter"}, tc.args...)
			if got := run(args, &stdout, &stderr, lint); got != exitOK {
				t.Fatalf("status = %d (stdout %q, stderr %q)", got, stdout.String(), stderr.String())
			}
			if gotConfig != tc.wantConfig || gotLinters != tc.wantLinters {
				t.Errorf("the linter ran with the configuration %q and the linters %q, want %q and %q", gotConfig, gotLinters, tc.wantConfig, tc.wantLinters)
			}
		})
	}
}

func TestRunWithAConfigurationThatDoesNotExistIsAnError(t *testing.T) {
	root := repo(t, "")
	var stdout, stderr bytes.Buffer
	got := run([]string{"-root", root, "-lint", "the-linter", "-config", "absent.yml"}, &stdout, &stderr, func(_, _, _, _ string) error {
		t.Error("the linter must not run without its configuration")
		return nil
	})
	if got != exitError || !strings.Contains(stderr.String(), "reading the configuration") {
		t.Errorf("status %d, stderr %q", got, stderr.String())
	}
}

// --enable-only limits the linters to the named ones and would override what the configuration enables,
// so it is passed only when linters are named.
func TestExecLinterLimitsTheLintersOnlyWhenTheyAreNamed(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := filepath.Join(dir, "linter.sh")
	body := "#!/bin/sh\necho \"$@\" > " + argsFile + "\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil { //nolint:gosec // a test script must be executable
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		linters string
		want    bool // --enable-only is passed
	}{
		{"named linters", "depguard,forbidigo", true},
		{"no linters named", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := execLinter(script, dir, filepath.Join(dir, "r.json"), tc.linters); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(argsFile) //nolint:gosec // the file this test wrote
			if err != nil {
				t.Fatal(err)
			}
			if has := strings.Contains(string(got), "--enable-only"); has != tc.want {
				t.Errorf("arguments %q: --enable-only passed = %v, want %v", got, has, tc.want)
			}
			if tc.want && !strings.Contains(string(got), "--enable-only "+tc.linters) {
				t.Errorf("arguments %q lack the linters %q", got, tc.linters)
			}
		})
	}
}
