package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// reportName is the file the linter writes its JSON report to, in a directory of its own.
const reportName = "report.json"

// dirMode and fileMode are the permissions of the directories and files of the copy.
const (
	dirMode  = 0o750
	fileMode = 0o600
)

// linterRunner runs the linter in dir with the configuration of that directory and writes its JSON report
// to reportPath. It returns an error when the linter did not run (a bad configuration, a build failure):
// findings are not an error.
type linterRunner func(bin, dir, reportPath, linters string) error

// lintFixtures copies the fixture module and the configuration to a temporary directory, runs the linter
// there and returns what it reported.
func lintFixtures(o options, lint linterRunner) ([]finding, error) {
	work, err := os.MkdirTemp("", "lintcheck-work-")
	if err != nil {
		return nil, fmt.Errorf("making the working directory: %w", err)
	}
	defer os.RemoveAll(work)
	out, err := os.MkdirTemp("", "lintcheck-report-")
	if err != nil {
		return nil, fmt.Errorf("making the report directory: %w", err)
	}
	defer os.RemoveAll(out)
	if err := copyTree(filepath.Join(o.root, o.fixtures), work); err != nil {
		return nil, fmt.Errorf("copying the fixtures: %w", err)
	}
	config, err := os.ReadFile(filepath.Join(o.root, o.config))
	if err != nil {
		return nil, fmt.Errorf("reading the configuration: %w", err)
	}
	if err := os.WriteFile(filepath.Join(work, configName), config, fileMode); err != nil {
		return nil, fmt.Errorf("copying the configuration: %w", err)
	}
	reportPath := filepath.Join(out, reportName)
	if err := lint(o.lint, work, reportPath, o.linters); err != nil {
		return nil, err
	}
	return readFindings(reportPath)
}

// copyTree copies the regular files of the directory src into dst, with their relative paths.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, dirMode)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileMode)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return err
		}
		return out.Close()
	})
}

// lintExitFindings is the exit status of golangci-lint when it ran and found something.
const lintExitFindings = 1

// execLinter runs golangci-lint, limited to linters when that is not empty (empty: the ones the
// configuration enables). GOWORK is off so that a go.work of the developer cannot change what the
// fixture module resolves; the exit status that means "found something" is not an error.
func execLinter(bin, dir, reportPath, linters string) error {
	args := []string{"run"}
	if linters != "" {
		args = append(args, "--enable-only", linters)
	}
	args = append(args, "--output.json.path", reportPath, "--output.text.path", "", "./...")
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if err == nil || (errors.As(err, &exit) && exit.ExitCode() == lintExitFindings) {
		return nil
	}
	return fmt.Errorf("running golangci-lint: %w\n%s", err, stderr.String())
}
