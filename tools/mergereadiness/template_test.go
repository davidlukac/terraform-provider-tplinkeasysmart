package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// templatePath is the pull request template of the repository, from the directory of this package: the
// module root is two levels up (the test checks that it is).
var templatePath = filepath.Join("..", "..", ".github", "pull_request_template.md")

// templateBoxes are the boxes of the section of the template, in order: the six of the checklist and the
// placeholder of the "Pending work" list. They are the text after each box, as the check prints it.
var templateBoxes = []string{
	"CI is green on the head commit",
	"Local checks done (format, vet, lint, tests with `-race`, coverage policy)",
	"Review done (light, deep or adversarial)",
	"Review findings fixed and pushed",
	"No agent is still working on this pull request",
	"**Ready for the maintainer to merge** (set last, by the assistant only)",
	"<task> (<agent role>)",
}

// readTemplate is the real template of the repository.
func readTemplate(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat(filepath.Join("..", "..", "go.mod")); err != nil {
		t.Fatalf("the module root is not two levels above the package: %v", err)
	}
	data, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("the pull request template cannot be read: %v", err)
	}
	return string(data)
}

// The job reads the description of a pull request that uses the template: the template must be a
// description the program reads as a section with its boxes, whatever the program's own samples say.
// Renaming the heading or changing a box to a form the program does not read would otherwise turn the
// check green for every pull request.
func TestTheTemplateHoldsOneSectionThatTheCheckReads(t *testing.T) {
	template := readTemplate(t)
	found := structuredPass(splitLines(template))

	t.Run("exactly one section", func(t *testing.T) {
		if len(found.headings) != 1 {
			t.Fatalf("the template has %d merge readiness headings %v, want 1", len(found.headings), found.headings)
		}
	})

	t.Run("it is the last section", func(t *testing.T) {
		if len(found.headings) != 1 {
			t.Skip("covered by the case above")
		}
		var later []string
		visibleLines(splitLines(template), func(index int, l line) {
			if level, _, ok := headingOf(l); ok && index+1 > found.headings[0] && level <= sectionLevel {
				later = append(later, fmt.Sprintf("line %d", index+1))
			}
		})
		if len(later) > 0 {
			t.Errorf("a heading of level %d or shallower follows the section: %v", sectionLevel, later)
		}
	})

	t.Run("no box is ticked", func(t *testing.T) {
		if found.ticked != 0 {
			t.Errorf("%d boxes of the section are ticked in the template", found.ticked)
		}
	})

	t.Run("the checklist above the section is not read", func(t *testing.T) {
		for _, b := range found.unticked {
			if len(found.headings) == 1 && b.line < found.headings[0] {
				t.Errorf("line %d (%q) is before the heading, and is read as a box of the section", b.line, b.text)
			}
		}
	})

	t.Run("the boxes are the six of the checklist and the placeholder", func(t *testing.T) {
		if len(found.unticked) != len(templateBoxes) {
			t.Fatalf("the section has %d boxes the check reads, want %d: %v", len(found.unticked), len(templateBoxes), found.unticked)
		}
		for i, want := range templateBoxes {
			t.Run(want, func(t *testing.T) {
				if got := found.unticked[i].text; got != want {
					t.Errorf("box %d reads %q, want %q", i+1, got, want)
				}
			})
		}
	})
}

// What the job prints for a pull request that keeps the template as it is, and for one that ticked every
// box: the two ends of the life of a description.
func TestTheVerdictOnTheTemplateIsRedUntilEveryBoxIsTicked(t *testing.T) {
	template := readTemplate(t)
	head, section, found := strings.Cut(template, "\n## Merge readiness\n")
	if !found {
		t.Fatal("the template has no line \"## Merge readiness\"")
	}
	tests := []struct {
		name     string
		body     string
		pass     bool
		unticked int
		first    string
	}{
		{
			"as it is", template, false, len(templateBoxes),
			fmt.Sprintf("merge readiness: %d box(es) not ticked:", len(templateBoxes)),
		},
		{
			"every box of the section ticked", head + "\n## Merge readiness\n" + strings.ReplaceAll(section, "[ ]", "[x]"),
			true, 0, msgAllTicked,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Judge(tt.body)
			if got.Pass != tt.pass || got.Unticked != tt.unticked || got.Lines[0] != tt.first {
				t.Errorf("pass %v, %d unticked, first line %q; want %v, %d, %q",
					got.Pass, got.Unticked, got.Lines[0], tt.pass, tt.unticked, tt.first)
			}
		})
	}
}

// The program reads the file the way the job gives it, and exits with the status the job turns into red.
func TestRunOnTheTemplateFileIsRed(t *testing.T) {
	readTemplate(t)
	var out strings.Builder
	if code := Run([]string{"-body-file", templatePath}, &out); code != exitRed {
		t.Errorf("exit code %d, want %d; output %q", code, exitRed, out.String())
	}
	if !strings.HasPrefix(out.String(), fmt.Sprintf("merge readiness: %d box(es) not ticked:\n", len(templateBoxes))) {
		t.Errorf("output %q", out.String())
	}
}
