package probe

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// File and directory modes of the scratch trees.
const (
	fileMode = 0o644
	dirMode  = 0o755
)

// tree is a scratch repository: a directory the fixtures change.
type tree struct{ dir string }

// path is the place of a slash-separated path relative to the tree.
func (t tree) path(rel string) string { return filepath.Join(t.dir, filepath.FromSlash(rel)) }

// write creates or replaces a file, with its directories.
func (t tree) write(rel, content string) error {
	if err := os.MkdirAll(filepath.Dir(t.path(rel)), dirMode); err != nil {
		return err
	}
	return os.WriteFile(t.path(rel), []byte(content), fileMode)
}

// read returns the content of a file.
func (t tree) read(rel string) (string, error) {
	data, err := os.ReadFile(t.path(rel))
	return string(data), err
}

// mkdir creates a directory, with its parents.
func (t tree) mkdir(rel string) error { return os.MkdirAll(t.path(rel), dirMode) }

// move renames a file.
func (t tree) move(from, to string) error { return os.Rename(t.path(from), t.path(to)) }

// symlink creates a symbolic link at rel whose target is the text given (relative to the link).
func (t tree) symlink(rel, target string) error { return os.Symlink(target, t.path(rel)) }

// errNothingChanged is what a fixture that would change nothing returns: a probe whose fixture silently
// did nothing would pass for the wrong reason.
var errNothingChanged = errors.New("the fixture changed nothing")

// replace replaces every occurrence of old in a file with new, and fails when there is none.
func (t tree) replace(rel, old, replacement string) error {
	content, err := t.read(rel)
	if err != nil {
		return err
	}
	if !strings.Contains(content, old) {
		return fmt.Errorf("%s does not hold %q: %w", rel, old, errNothingChanged)
	}
	return t.write(rel, strings.ReplaceAll(content, old, replacement))
}

// dropLines removes the lines of a file that hold text, and fails when there is none.
func (t tree) dropLines(rel, text string) error {
	content, err := t.read(rel)
	if err != nil {
		return err
	}
	var kept []string
	for line := range strings.SplitSeq(content, "\n") {
		if !strings.Contains(line, text) {
			kept = append(kept, line)
		}
	}
	if len(kept) == len(strings.Split(content, "\n")) {
		return fmt.Errorf("%s has no line with %q: %w", rel, text, errNothingChanged)
	}
	return t.write(rel, strings.Join(kept, "\n"))
}

// copyDir copies the directory src to dst: directories, regular files with their modes, and symbolic links
// as links.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := dst + strings.TrimPrefix(path, src)
		switch {
		case entry.IsDir():
			return os.MkdirAll(target, dirMode) //nolint:gosec // G122: the scratch tree is this program's own, nothing else writes in it
		case entry.Type()&fs.ModeSymlink != 0:
			return copyLink(path, target)
		default:
			return copyFile(path, target)
		}
	})
}

// copyLink makes dst a symbolic link with the target of the link src.
func copyLink(src, dst string) error {
	link, err := os.Readlink(src)
	if err != nil {
		return err
	}
	return os.Symlink(link, dst)
}

// copyFile copies one regular file with its permissions.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src) //nolint:gosec // a file of the repository the operator names
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, info.Mode().Perm()) //nolint:gosec // a file of the scratch tree this program makes
}
