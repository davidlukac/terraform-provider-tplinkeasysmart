package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	makefilePath = "Makefile"
	lintConfig   = ".golangci.yml"
)

var (
	// makeTarget is a rule line of a Makefile: names, then a colon that is not the start of `:=`.
	makeTarget = regexp.MustCompile(`^([A-Za-z0-9_][A-Za-z0-9_.-]*(?: +[A-Za-z0-9_][A-Za-z0-9_.-]*)*) *:([^=]|$)`)
	yamlItem   = regexp.MustCompile(`^( *)- +([A-Za-z0-9_-]+)`)
)

// indexConfig reads the Makefile and the linter configuration. A file that is missing is not an
// error here: the names that need it are reported as unresolvable.
func (r *repo) indexConfig() error {
	if data, err := readOptional(filepath.Join(r.root, makefilePath)); err != nil {
		return err
	} else if data != "" {
		for _, line := range strings.Split(data, "\n") {
			if m := makeTarget.FindStringSubmatch(line); m != nil {
				for _, name := range strings.Fields(m[1]) {
					r.targets[name] = true
				}
			}
		}
	}
	data, err := readOptional(filepath.Join(r.root, lintConfig))
	if err != nil {
		return err
	}
	r.linters, r.lintersOK = enabledLinters(data)
	return nil
}

// readOptional reads a file; a file that does not exist is empty and no error.
func readOptional(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	return string(data), nil
}

// enabledLinters returns the list under `linters:` / `enable:` of a golangci-lint configuration. It
// reads the two keys line by line, which is all the file's layout needs, so the tool has no YAML
// dependency; false means the file has no such list.
func enabledLinters(config string) (map[string]bool, bool) {
	out := map[string]bool{}
	inLinters, inEnable := false, false
	for _, line := range strings.Split(config, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		switch {
		case indent == 0:
			inLinters, inEnable = trim == "linters:", false
		case !inLinters:
		case indent == 2:
			inEnable = trim == "enable:"
		case inEnable:
			if m := yamlItem.FindStringSubmatch(line); m != nil {
				out[m[2]] = true
			}
		}
	}
	return out, len(out) > 0
}
