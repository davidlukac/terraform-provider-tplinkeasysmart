//go:build !windows

package probe

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newTree is an empty scratch tree.
func newTree(t *testing.T) tree {
	t.Helper()
	return tree{dir: t.TempDir()}
}

// mustWrite writes a file of the tree or fails the test.
func mustWrite(t *testing.T, tr tree, rel, content string) {
	t.Helper()
	if err := tr.write(rel, content); err != nil {
		t.Fatal(err)
	}
}

func TestTreeWriteAndRead(t *testing.T) {
	tr := newTree(t)
	mustWrite(t, tr, "a/b/c.txt", "one")
	mustWrite(t, tr, "a/b/c.txt", "two")
	got, err := tr.read("a/b/c.txt")
	if err != nil || got != "two" {
		t.Fatalf("read = %q, %v; want the replaced content", got, err)
	}
	if _, err := tr.read("missing"); err == nil {
		t.Error("reading a missing file did not fail")
	}
}

func TestTreeWriteFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(tree) error
		rel   string
	}{
		{name: "a directory in the way of the file", setup: func(tr tree) error { return tr.mkdir("d") }, rel: "d"},
		{name: "a file in the way of the directory", setup: func(tr tree) error { return tr.write("f", "x") }, rel: "f/g"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newTree(t)
			if err := tc.setup(tr); err != nil {
				t.Fatal(err)
			}
			if err := tr.write(tc.rel, "x"); err == nil {
				t.Error("write succeeded")
			}
		})
	}
}

func TestTreeReplace(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		old     string
		want    string
		err     error
		anyErr  bool
	}{
		{name: "every occurrence", content: "a-b-a", old: "a", want: "X-b-X"},
		{name: "no occurrence", content: "abc", old: "z", err: errNothingChanged},
		{name: "an empty file", content: "", old: "z", err: errNothingChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newTree(t)
			mustWrite(t, tr, "f", tc.content)
			err := tr.replace("f", tc.old, "X")
			if !errors.Is(err, tc.err) {
				t.Fatalf("replace error = %v, want %v", err, tc.err)
			}
			if got, _ := tr.read("f"); tc.err == nil && got != tc.want {
				t.Errorf("content = %q, want %q", got, tc.want)
			}
		})
	}
	if err := newTree(t).replace("missing", "a", "b"); err == nil || errors.Is(err, errNothingChanged) {
		t.Errorf("replace on a missing file = %v, want a read error", err)
	}
}

func TestTreeDropLines(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    string
		err     error
	}{
		{name: "the lines that hold the text", content: "a\nxx\nb\nyxy\n", want: "a\nb\n"},
		{name: "the last line without a newline", content: "a\nx", want: "a"},
		{name: "no line holds it", content: "a\nb\n", err: errNothingChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := newTree(t)
			mustWrite(t, tr, "f", tc.content)
			err := tr.dropLines("f", "x")
			if !errors.Is(err, tc.err) {
				t.Fatalf("dropLines error = %v, want %v", err, tc.err)
			}
			if got, _ := tr.read("f"); tc.err == nil && got != tc.want {
				t.Errorf("content = %q, want %q", got, tc.want)
			}
		})
	}
	if err := newTree(t).dropLines("missing", "a"); err == nil || errors.Is(err, errNothingChanged) {
		t.Errorf("dropLines on a missing file = %v, want a read error", err)
	}
}

func TestTreeMoveAndSymlink(t *testing.T) {
	tr := newTree(t)
	mustWrite(t, tr, "from", "x")
	if err := tr.move("from", "to"); err != nil {
		t.Fatal(err)
	}
	if err := tr.symlink("link", "to"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(tr.path("link")); err != nil || got != "to" {
		t.Errorf("link target = %q, %v", got, err)
	}
	if _, err := os.Stat(tr.path("from")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the moved file is still there: %v", err)
	}
}

func TestCopyDir(t *testing.T) {
	src := t.TempDir()
	srcTree := tree{dir: src}
	mustWrite(t, srcTree, "a/run.sh", "#!/bin/sh\n")
	mustWrite(t, srcTree, "a/b/data.txt", "data")
	if err := os.Chmod(srcTree.path("a/run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("data.txt", srcTree.path("a/b/link")); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy")
	if err := copyDir(filepath.Join(src, "a"), dst); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dst, "run.sh")); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("run.sh: %v, %v; want the mode kept", info, err)
	}
	if got, err := os.ReadFile(filepath.Join(dst, "b", "data.txt")); err != nil || string(got) != "data" {
		t.Errorf("data.txt = %q, %v", got, err)
	}
	if got, err := os.Readlink(filepath.Join(dst, "b", "link")); err != nil || got != "data.txt" {
		t.Errorf("the link was not copied as a link: %q, %v", got, err)
	}
}

func TestCopyFailures(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		do   func() error
	}{
		{name: "a directory that is not there", do: func() error { return copyDir(filepath.Join(dir, "missing"), filepath.Join(dir, "x")) }},
		{name: "a destination that cannot be made", do: func() error { return copyDir(dir, filepath.Join(file, "sub")) }},
		{name: "a file that is not there", do: func() error { return copyFile(filepath.Join(dir, "missing"), filepath.Join(dir, "x")) }},
		{name: "a file that is a directory", do: func() error { return copyFile(dir, filepath.Join(dir, "x")) }},
		{name: "a destination in a directory that is not there", do: func() error { return copyFile(file, filepath.Join(dir, "no", "x")) }},
		{name: "a link that is not a link", do: func() error { return copyLink(file, filepath.Join(dir, "x")) }},
		{name: "a link where there is one already", do: func() error {
			if err := os.Symlink("t", filepath.Join(dir, "l")); err != nil {
				return err
			}
			return copyLink(filepath.Join(dir, "l"), filepath.Join(dir, "l"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.do(); err == nil {
				t.Error("no error")
			}
		})
	}
}

// miniTree is a tree with the files the fixtures change: one workflow that uses the setup action twice, the
// way fuzz.yml does, and a .github/ with a workflows and an actions directory.
func miniTree(t *testing.T) tree {
	t.Helper()
	tr := newTree(t)
	mustWrite(t, tr, fuzzWorkflow, "jobs:\n  a:\n    steps:\n      - "+setupUse+" # zizmor: ignore[self-repository] why\n      - run: x\n      - "+setupUse+" # zizmor: ignore[self-repository] why\n")
	if err := tr.mkdir(".github/actions"); err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestFixtures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup []fixture
		check func(t *testing.T, tr tree)
	}{
		{name: "addFinding", setup: []fixture{addFinding}, check: func(t *testing.T, tr tree) {
			t.Helper()
			wantFile(t, tr, findingFile, findingWorkflow)
		}},
		{name: "configFile", setup: []fixture{configFile(".github/zizmor.yml")}, check: func(t *testing.T, tr tree) {
			t.Helper()
			wantFile(t, tr, ".github/zizmor.yml", disableRule)
		}},
		{name: "ignoreFile", setup: []fixture{ignoreFile(".github/.ignore")}, check: func(t *testing.T, tr tree) {
			t.Helper()
			wantFile(t, tr, ".github/.ignore", ignoredFile)
		}},
		{name: "breakWithBOM", setup: []fixture{addFinding, breakWithBOM}, check: func(t *testing.T, tr tree) {
			t.Helper()
			wantFile(t, tr, findingFile, byteOrderMark+findingWorkflow)
		}},
		{name: "breakWithSecondDocument", setup: []fixture{addFinding, breakWithSecondDocument}, check: func(t *testing.T, tr tree) {
			t.Helper()
			wantFile(t, tr, findingFile, findingWorkflow+"---\n"+findingWorkflow)
		}},
		{name: "breakWithDuplicateKey", setup: []fixture{addFinding, breakWithDuplicateKey}, check: func(t *testing.T, tr tree) {
			t.Helper()
			wantFile(t, tr, findingFile, findingWorkflow+"permissions: {}\n")
		}},
		{name: "breakWithSchemaError", setup: []fixture{addFinding, breakWithSchemaError}, check: func(t *testing.T, tr tree) {
			t.Helper()
			wantFile(t, tr, findingFile, strings.Replace(findingWorkflow, "ubuntu-24.04", "5", 1))
		}},
		{name: "suppressIn", setup: []fixture{addFinding, suppressIn(findingFile)}, check: func(t *testing.T, tr tree) {
			t.Helper()
			got, _ := tr.read(findingFile)
			if !strings.Contains(got, findingStep+" # zizmor: ignore[template-injection] probe\n") {
				t.Errorf("the suppression is not after the step: %q", got)
			}
		}},
		{name: "removeOneSetupUse", setup: []fixture{removeOneSetupUse}, check: func(t *testing.T, tr tree) {
			t.Helper()
			got, _ := tr.read(fuzzWorkflow)
			if strings.Contains(got, setupUse) || !strings.Contains(got, "run: x") {
				t.Errorf("the uses were not removed alone: %q", got)
			}
		}},
		{name: "addOneSetupUse", setup: []fixture{addOneSetupUse}, check: func(t *testing.T, tr tree) {
			t.Helper()
			wantFile(t, tr, ".github/workflows/zz-extra.yml", extraWorkflow)
		}},
		{name: "nameAnotherRuleOnSetupUses", setup: []fixture{nameAnotherRuleOnSetupUses}, check: func(t *testing.T, tr tree) {
			t.Helper()
			got, _ := tr.read(fuzzWorkflow)
			if strings.Contains(got, "ignore[self-repository]") || strings.Count(got, "ignore[template-injection]") != 2 {
				t.Errorf("the rule was not renamed on both uses: %q", got)
			}
		}},
		{name: "linkAWorkflow", setup: []fixture{linkAWorkflow}, check: func(t *testing.T, tr tree) {
			t.Helper()
			wantLink(t, tr, ".github/workflows/zz.yml", "../../elsewhere/zz.yml")
			got, _ := tr.read("elsewhere/zz.yml")
			if !strings.Contains(got, findingStep+suppression) {
				t.Errorf("the linked workflow lacks the finding or its suppression: %q", got)
			}
			wantMissing(t, tr, findingFile)
		}},
		{name: "linkACleanWorkflow", setup: []fixture{linkACleanWorkflow}, check: func(t *testing.T, tr tree) {
			t.Helper()
			wantLink(t, tr, ".github/workflows/clean.yml", "../../elsewhere/clean.yml")
			wantFile(t, tr, "elsewhere/clean.yml", cleanWorkflow)
		}},
		{name: "linkAnAction", setup: []fixture{linkAnAction}, check: func(t *testing.T, tr tree) {
			t.Helper()
			wantLink(t, tr, ".github/actions/linked/action.yml", "../../../elsewhere/action.yml")
			wantFile(t, tr, "elsewhere/action.yml", injectedAction)
		}},
		{name: "addActionOutsideGithub", setup: []fixture{addActionOutsideGithub}, check: func(t *testing.T, tr tree) {
			t.Helper()
			wantFile(t, tr, "ci/action.yml", injectedAction)
		}},
		{name: "linkADirectory", setup: []fixture{linkADirectory}, check: func(t *testing.T, tr tree) {
			t.Helper()
			wantLink(t, tr, ".github/actions/linked", "../../ci")
			wantFile(t, tr, "ci/action.yml", injectedAction)
		}},
		{name: "addHostileFileName", setup: []fixture{addHostileFileName}, check: func(t *testing.T, tr tree) {
			t.Helper()
			wantFile(t, tr, ".github/workflows/a\n::warning::x.yml", cleanWorkflow)
		}},
		{name: "shadowTheRunner", setup: []fixture{shadowTheRunner}, check: func(t *testing.T, tr tree) {
			t.Helper()
			wantFile(t, tr, "go.mod", shadowModule)
			wantFile(t, tr, "tools/zizmorrun/main.go", shadowMain)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := miniTree(t)
			if err := all(tc.setup...)(tr); err != nil {
				t.Fatal(err)
			}
			tc.check(t, tr)
		})
	}
}

// The links must lead where the probes say: the relative targets resolve to the files of the scratch tree.
func TestLinksResolve(t *testing.T) {
	for _, tc := range []struct {
		name string
		fx   fixture
		link string
	}{
		{name: "workflow", fx: linkAWorkflow, link: ".github/workflows/zz.yml"},
		{name: "clean workflow", fx: linkACleanWorkflow, link: ".github/workflows/clean.yml"},
		{name: "action", fx: linkAnAction, link: ".github/actions/linked/action.yml"},
		{name: "directory", fx: linkADirectory, link: ".github/actions/linked"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := miniTree(t)
			if err := tc.fx(tr); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(tr.path(tc.link)); err != nil {
				t.Errorf("the link does not resolve: %v", err)
			}
		})
	}
}

// A fixture that cannot do its work is an error and never a silent no-op, so a probe cannot pass for the
// wrong reason.
func TestFixturesFailOnATreeThatLacksWhatTheyChange(t *testing.T) {
	for _, tc := range []struct {
		name string
		fx   fixture
	}{
		{name: "breakWithBOM", fx: breakWithBOM},
		{name: "breakWithSecondDocument", fx: breakWithSecondDocument},
		{name: "breakWithDuplicateKey", fx: breakWithDuplicateKey},
		{name: "breakWithSchemaError", fx: breakWithSchemaError},
		{name: "suppressIn", fx: suppressIn(findingFile)},
		{name: "removeOneSetupUse", fx: removeOneSetupUse},
		{name: "nameAnotherRuleOnSetupUses", fx: nameAnotherRuleOnSetupUses},
		{name: "linkACleanWorkflow", fx: linkACleanWorkflow},
		{name: "linkADirectory", fx: linkADirectory},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.fx(newTree(t)); err == nil {
				t.Error("the fixture changed an empty tree without a complaint")
			}
		})
	}
}

func TestAllStopsAtTheFirstFailure(t *testing.T) {
	boom := errors.New("boom")
	calls := 0
	count := func(tree) error { calls++; return nil }
	err := all(count, func(tree) error { return boom }, count)(newTree(t))
	if !errors.Is(err, boom) || calls != 1 {
		t.Errorf("err = %v after %d calls, want boom after 1", err, calls)
	}
}

func wantFile(t *testing.T, tr tree, rel, want string) {
	t.Helper()
	if got, err := tr.read(rel); err != nil || got != want {
		t.Errorf("%s = %q, %v; want %q", rel, got, err, want)
	}
}

func wantLink(t *testing.T, tr tree, rel, target string) {
	t.Helper()
	if got, err := os.Readlink(tr.path(rel)); err != nil || got != target {
		t.Errorf("link %s = %q, %v; want %q", rel, got, err, target)
	}
}

func wantMissing(t *testing.T, tr tree, rel string) {
	t.Helper()
	if _, err := os.Lstat(tr.path(rel)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s exists: %v", rel, err)
	}
}
