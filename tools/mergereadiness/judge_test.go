package main

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// section is the section of the pull request template, every box unticked (four boxes); ticked is the
// same with every box ticked.
const section = `## Merge readiness

<!-- Kept by the maintainer's assistant. -->

- [ ] CI is green on the head commit
- [ ] Local checks done
- [ ] **Ready for the maintainer to merge**

### Pending work

- [ ] a task (reviewer)
`

var ticked = strings.ReplaceAll(section, "[ ]", "[x]")

// heading is the first line of the section of every judgment below that supplies its own boxes.
const heading = "## Merge readiness\n\n"

func TestJudgeGivesTheVerdictOfEachDescription(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		pass     bool
		unticked int
		first    string // the first line of the output
	}{
		{"every box unticked", "Why.\n\n" + section, false, 4, "merge readiness: 4 box(es) not ticked:"},
		{"every box ticked", "Why.\n\n" + ticked, true, 0, msgAllTicked},
		{"one box left", strings.Replace(ticked, "[x] Local", "[ ] Local", 1), false, 1, "merge readiness: 1 box(es) not ticked:"},
		{"a capital X is ticked", strings.ReplaceAll(section, "[ ]", "[X]"), true, 0, msgAllTicked},
		{"an empty description", "", true, 0, msgNoSection},
		{"only blank lines", "\n\n  \n", true, 0, msgNoSection},
		{"no section", "## What and why\n\n- [ ] a box of another section\n", true, 0, msgNoSection},
		{"a section without boxes", heading + "nothing here\n", false, 0, msgNoBoxes},
		{"a section with only text items", heading + "- one\n- two\n", false, 0, msgNoBoxes},
		{"emphasis in the title", "## **Merge readiness**\n- [ ] x\n", false, 1, "merge readiness: 1 box(es) not ticked:"},
		{"code marks in the title", "## `Merge readiness`\n- [ ] x\n", false, 1, "merge readiness: 1 box(es) not ticked:"},
		{"a link as the title", "## [Merge readiness](https://example.com/a_b)\n- [ ] x\n", false, 1, "merge readiness: 1 box(es) not ticked:"},
		{"a tag in the title", "## <b>Merge</b> readiness\n- [ ] x\n", false, 1, "merge readiness: 1 box(es) not ticked:"},
		{"the title is matched whole", "## Merge readiness notes\n\n- [ ] x\n", true, 0, msgNoSection},
		{"the title ignores case", "## MERGE READINESS\n\n- [ ] x\n", false, 1, "merge readiness: 1 box(es) not ticked:"},
		{"the title collapses whitespace", "## Merge \t readiness\n- [ ] x\n", false, 1, "merge readiness: 1 box(es) not ticked:"},
		{"a closing sequence on the heading", "## Merge readiness ##\n\n- [ ] x\n", false, 1, "merge readiness: 1 box(es) not ticked:"},
		{"three spaces before the heading", "   ## Merge readiness\n- [ ] x\n", false, 1, "merge readiness: 1 box(es) not ticked:"},
		{"an empty box is a box", heading + "- [ ]\n", false, 1, "merge readiness: 1 box(es) not ticked:"},
		{"a tab after the marker", heading + "-\t[ ] a\n", false, 1, "merge readiness: 1 box(es) not ticked:"},
		{"an empty heading is not the section", "##\n- [ ] x\n", true, 0, msgNoSection},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Judge(tt.body)
			if got.Pass != tt.pass || got.Unticked != tt.unticked || got.Lines[0] != tt.first {
				t.Errorf("Judge = pass %v, unticked %d, lines %q; want pass %v, unticked %d, first line %q",
					got.Pass, got.Unticked, got.Lines, tt.pass, tt.unticked, tt.first)
			}
		})
	}
}

// The markup the structured reading does not understand, or reads differently from the renderer, is red.
func TestJudgeIsRedWhereTheMarkupIsAmbiguous(t *testing.T) {
	const ambiguous = "merge readiness: ambiguous markup:"
	tests := []struct {
		name string
		body string
	}{
		{"an ordered box", heading + "1. [ ] a\n"},
		{"an ordered box with a parenthesis", heading + "1) [ ] a\n"},
		{"a nested box", heading + "- - [ ] a\n"},
		{"a quoted box", heading + "> - [ ] a\n"},
		{"a no-break space in the box", heading + "- [\u00a0] a\n"},
		{"a tab in the box", heading + "- [\t] a\n"},
		{"text glued to the box", heading + "- [ ]a\n"},
		{"a quoted heading", "> ## Merge readiness\n> - [ ] a\n"},
		{"a Setext heading of level 2", "Merge readiness\n---\n\n- [ ] a\n"},
		{"a Setext heading of level 1", "Merge readiness\n===\n\n- [ ] a\n"},
		{"a heading in HTML", "<h2>Merge readiness</h2>\n\n- [ ] a\n"},
		{"a title in bold type", "**Merge readiness**\n\n- [ ] a\n"},
		{"a heading of level 3", "### Merge readiness\n\n- [ ] x\n"},
		{"a heading of level 1", "# Merge readiness\n\n- [ ] x\n"},
		{"a heading indented four spaces", "    ## Merge readiness\n- [ ] x\n"},
		{"a fence in a list item that the renderer closes at the heading", "- item\n  ```\n" + heading + "- [ ] a\n"},
		{"a fence line in an HTML block", "<div>\n```\n</div>\n\n" + heading + "- [ ] a\n"},
		{"a comment that hides the heading", "<!--\n" + heading + "-->\n- [ ] x\n"},
		{"a fence that quotes the section", "Example:\n\n```\n" + section + "```\n"},
		{"a tilde fence that quotes the section", "~~~\n" + section + "~~~\n"},
		{"a longer fence that quotes the section", "````\n" + section + "````\n"},
		{"a box in a fence of the section", heading + "- [x] done\n\n```\n- [ ] quoted\n```\n"},
		{"a box in a comment of the section over several lines", heading + "<!--\n- [ ] hidden\n-->\n- [x] a\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Judge(tt.body)
			if got.Pass || got.Unticked == 0 || !strings.HasPrefix(got.Lines[0], ambiguous) {
				t.Errorf("Judge = pass %v, unticked %d, lines %q; want an ambiguous red verdict", got.Pass, got.Unticked, got.Lines)
			}
		})
	}
}

func TestTheAmbiguousVerdictCountsWhatItRecognized(t *testing.T) {
	got := Judge(heading + "- [x] done\n- [ ] one\n1. [ ] two\n")
	want := "merge readiness: ambiguous markup: 2 possible box(es) not ticked, 2 recognized:"
	if got.Lines[0] != want {
		t.Errorf("heading %q, want %q", got.Lines[0], want)
	}
}

// The section is read to the end of the description by the naive reading, so a box after it is reported.
func TestTheSectionMustBeTheLastOne(t *testing.T) {
	tests := []struct {
		name string
		body string
		pass bool
	}{
		{"boxes of a section before it are not judged", "## What\n\n- [ ] before\n\n" + ticked, true},
		{"a box of a section after it", ticked + "\n## Checklist\n\n- [ ] after\n", false},
		{"a box after a heading of level 1", ticked + "\n# Notes\n\n- [ ] after\n", false},
		{"a box after a heading of level 3", ticked + "\n### More\n\n- [ ] after\n", false},
		{"a ticked box of a section after it", ticked + "\n## Checklist\n\n- [x] after\n", true},
		{"the Pending work subsection belongs to it", heading + "- [x] a\n\n### Pending work\n\n- [ ] queued (implementer)\n", false},
		{"a Setext heading does not end it", heading + "- [x] a\n\nNotes\n---\n\n- [ ] after\n", false},
		{"a heading in a fence does not end it", heading + "```\n## Other\n```\n\n- [ ] after the fence\n", false},
		{"a heading in a comment does not end it", heading + "<!--\n## Other\n-->\n- [ ] after\n", false},
		{"a box in a comment of one line does not count", heading + "<!-- - [ ] hidden -->\n- [x] a\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Judge(tt.body); got.Pass != tt.pass {
				t.Errorf("Judge = pass %v (%q), want %v", got.Pass, got.Lines, tt.pass)
			}
		})
	}
}

// Whatever stands between the heading and an unticked box, the box is not green: a line the renderer does not
// read as a heading must not end the section for either reading.
func TestNothingBetweenTheHeadingAndABoxMakesItGreen(t *testing.T) {
	middles := []struct {
		name   string
		middle string
	}{
		{"a hash line in a fence", "```sh\n# run this\n```\n"},
		{"a hash line in a tilde fence", "~~~\n# run this\n~~~\n"},
		{"a hash line in a comment", "<!--\n# c\n-->\n"},
		{"a heading line in an HTML block", "<div>\n## Other\n</div>\n"},
		{"a heading line in a details block", "<details>\n## Other\n</details>\n"},
		{"a heading line in a pre block", "<pre>\n# Other\n</pre>\n"},
		{"a heading line in a table block", "<table>\n## Other\n</table>\n"},
		{"a heading line in a script block", "<script>\n## Other\n</script>\n"},
		{"a heading line after a comment", "<!-- c --> ## Other\n"},
		{"an indented heading line in an HTML block", "<div>\n   ## Other\n</div>\n"},
		{"a fence in an HTML block", "<div>\n```\n# x\n</div>\n"},
		{"a tilde fence in an HTML block", "<div>\n~~~\n# x\n</div>\n"},
		{"a complete comment that the parser reads as open", "<!-->\n\n    # x\n"},
		{"a heading line in indented code", "    # x\n"},
		{"a heading of level 1", "# x\n"},
		{"a Setext heading", "Notes\n---\n"},
		{"a table", "| a | b |\n|---|---|\n| - [ ] x | y |\n"},
		{"a footnote reference and a footnote definition", "text[^1]\n\n[^1]: a note\n"},
	}
	boxes := []string{"- [ ] b", "1. [ ] b", "> - [ ] b", "- - [ ] b", "- [\u00a0] b", "[^1]: - [ ] b", "> [^note]: 1. [ ] b", "   [^1]: - - [ ] b"}
	for _, m := range middles {
		for _, b := range boxes {
			t.Run(m.name+" then "+b, func(t *testing.T) {
				body := heading + "- [x] a\n\n" + m.middle + "\n" + b + "\n\n<!-- end -->\n"
				if got := Judge(body); got.Pass {
					t.Errorf("green: %q", body)
				}
			})
		}
	}
}

func TestJudgeAcceptsEveryListMarkerAndIndent(t *testing.T) {
	for _, marker := range []string{"-", "*", "+"} {
		for _, indent := range []string{"", "  ", "\t", "        "} {
			t.Run(marker+" "+indent, func(t *testing.T) {
				if got := Judge(heading + indent + marker + " [ ] a box\n"); got.Pass || got.Unticked != 1 {
					t.Errorf("Judge = pass %v, unticked %d: %q", got.Pass, got.Unticked, got.Lines)
				}
			})
		}
	}
}

func TestJudgeIgnoresTextThatIsNoBox(t *testing.T) {
	tests := []struct {
		name string
		line string
	}{
		{"no list marker", "[ ] no list marker"},
		{"empty brackets", "- [] empty brackets"},
		{"two spaces in the brackets", "- [  ] two spaces"},
		{"another letter", "- [y] another letter"},
		{"a box in the middle of an item", "- plain item [ ] with the box inside"},
		{"no space after the marker", "-[ ] no space after the marker"},
		{"a number without a dot", "1 [ ] a number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Judge(heading + tt.line + "\n- [x] done\n"); !got.Pass {
				t.Errorf("Judge = %q, want a pass", got.Lines)
			}
		})
	}
}

func TestJudgeAcceptsEveryLineEnding(t *testing.T) {
	for name, newline := range map[string]string{"CRLF": "\r\n", "CR": "\r", "LF": "\n"} {
		t.Run(name, func(t *testing.T) {
			red := Judge(strings.ReplaceAll(section, "\n", newline))
			if red.Pass || red.Unticked != 4 || strings.ContainsAny(strings.Join(red.Lines, ""), "\r") {
				t.Errorf("unticked: %v %d %q", red.Pass, red.Unticked, red.Lines)
			}
			if green := Judge(strings.ReplaceAll(ticked, "\n", newline)); !green.Pass {
				t.Errorf("ticked: %q", green.Lines)
			}
		})
	}
}

func TestJudgeFailsClosedWithTwoSections(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		unticked int
	}{
		{"the first is unticked", section + "\n## Notes\n\ntext\n\n" + ticked, 4},
		{"the second is unticked", ticked + "\n## Notes\n\ntext\n\n" + section, 4},
		{"both are unticked", section + "\n## Notes\n\n" + section, 8},
		{"both are ticked", ticked + "\n## Notes\n\n" + ticked, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Judge(tt.body)
			if got.Pass != (tt.unticked == 0) || got.Unticked != tt.unticked {
				t.Errorf("Judge = pass %v, unticked %d, want %d unticked", got.Pass, got.Unticked, tt.unticked)
			}
		})
	}
}

// What the structured reading counts, without the cross-check: fences and comments.
func TestTheStructuredReadingFollowsFencesAndComments(t *testing.T) {
	tests := []struct {
		name     string
		rest     string // the text after the heading
		unticked int
	}{
		{"a shorter closing fence does not close", "````\n```\n- [ ] x\n````\n", 0},
		{"another character does not close", "```\n~~~\n- [ ] x\n```\n", 0},
		{"text after the closing fence does not close", "```\n``` text\n- [ ] x\n```\n", 0},
		{"a longer closing fence closes", "```\n- [ ] x\n`````\n- [ ] y\n", 1},
		{"an unclosed fence runs to the end", "```\n- [ ] x\n", 0},
		{"a backtick in the info string of a backtick fence is no fence", "``` a`b\n- [ ] x\n", 1},
		{"a backtick in the info string of a tilde fence is fine", "~~~ a`b\n- [ ] x\n~~~\n", 0},
		{"a fence indented three spaces opens", "   ```\n- [ ] x\n```\n", 0},
		{"a fence indented four spaces is code, not a fence", "    ```\n- [ ] x\n", 1},
		{"a closing fence indented three spaces closes", "```\n- [ ] x\n   ```\n- [ ] y\n", 1},
		{"a closing fence indented four spaces does not", "```\n    ```\n- [ ] x\n", 0},
		{"a comment on one line", "<!-- - [ ] hidden -->\n- [x] a\n", 0},
		{"a comment over several lines", "<!--\n- [ ] hidden\nmore\n-->\n- [x] a\n", 0},
		{"a comment after a ticked box", "- [x] a <!-- - [ ] b -->\n", 0},
		{"two comments on a line", "<!-- a --> <!-- - [ ] b -->\n- [x] a\n", 0},
		{"a comment indented three spaces hides", "   <!--\n- [ ] x\n-->\n", 0},
		{"a comment indented four spaces hides nothing", "    <!--\n- [ ] x\n-->\n", 1},
		{"an unclosed comment hides nothing", "<!-- a\n- [ ] x\n", 1},
		{"a comment that starts inside a line hides nothing", "text <!-- a\n- [ ] x\n--> y\n", 1},
		{"a comment marker in inline code hides nothing", "Use `<!--` here.\n- [ ] x\n", 1},
		{"an unclosed comment after a closed one hides nothing", "<!-- a --> <!-- b\n- [ ] x\n", 1},
		{"what follows a comment on its line counts", "<!-- a --> - [ ] x\n", 1},
		{"a comment and then a fence on the same line is no fence", "<!-- c --> ```\n- [ ] x\n", 1},
		{"what follows a comment that ends a line counts", "<!-- a\nb --> - [ ] visible\n", 1},
		{"a fence in a comment is no fence", "<!--\n```\n-->\n- [ ] x\n", 1},
		{"a comment in a fence is text", "```\n<!--\n```\n\n- [ ] x\n", 1},
		{"a comment of three lines hides each of them", "<!--\n- [ ] x\n- [ ] y\n-->\n- [x] z\n", 0},
		{"the form <!--> is a complete comment", "<!-->\n- [ ] x\n", 1},
		{"the form <!---> is a complete comment", "<!--->\n- [ ] x\n", 1},
		{"the form <!---> hides what follows it on the line", "<!---> - [ ] x\n- [x] y\n", 1},
		{"the form <!--> hides what follows it on the line", "<!--> - [ ] x\n- [x] y\n", 1},
		{"a comment opened after another closes", "<!-- a\n--> <!-- b\n- [ ] x\n-->\n- [x] y\n", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := structuredPass(splitLines(heading + tt.rest))
			if len(got.unticked) != tt.unticked {
				t.Errorf("%d unticked boxes (%v), want %d", len(got.unticked), got.unticked, tt.unticked)
			}
		})
	}
}

func TestAHeadingInACommentOrAFenceStartsNoStructuredSection(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"a comment", "<!--\n" + heading + "-->\n- [ ] x\n"},
		{"a fence", "```\n" + heading + "```\n- [ ] x\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := structuredPass(splitLines(tt.body)); len(got.unticked) != 0 || got.ticked != 0 {
				t.Errorf("the structured reading found a section: %v", got.unticked)
			}
		})
	}
}

func TestTheNaiveReadingNeverEndsTheSection(t *testing.T) {
	starts, boxes := naivePass(splitLines("- [ ] a\n## Merge readiness\n- [ ] b\n### Sub\n- [ ] c\n## Other\n- [ ] d\n# Top\n- [ ] e\n```\n# x\n```\n1. [ ] f\n"))
	if len(starts) != 1 || starts[0] != 2 {
		t.Errorf("starts %v, want [2]", starts)
	}
	want := []box{{3, "b"}, {5, "c"}, {7, "d"}, {9, "e"}, {13, "f"}}
	if !reflect.DeepEqual(boxes, want) {
		t.Errorf("boxes %v, want %v", boxes, want)
	}
	if starts, _ := naivePass(splitLines("- [ ] a\n## Other\n")); len(starts) != 0 {
		t.Error("found a section in a body without the title")
	}
}

func TestTheNaiveReadingFindsTheTitleInEveryForm(t *testing.T) {
	forms := []string{
		"## Merge readiness", "# Merge readiness", "###### Merge readiness", "> ## Merge readiness", "    ## Merge readiness",
		"Merge readiness", "**Merge readiness**", "_Merge readiness_", "<h2>Merge readiness</h2>", "<h2 id=\"x\">Merge readiness</h2>",
		"[Merge readiness](#top)", "## `Merge readiness` ##", "MERGE   READINESS", "> > Merge readiness",
	}
	for _, form := range forms {
		t.Run(form, func(t *testing.T) {
			if starts, _ := naivePass(splitLines("a\n" + form + "\n")); len(starts) != 1 || starts[0] != 2 {
				t.Errorf("starts %v, want [2]", starts)
			}
		})
	}
	for _, text := range []string{"- Merge readiness", "Merge readiness notes", "#Merge readiness", "- [ ] Merge readiness", "Merge readinesses", "<h2>Merge</h2> readiness x"} {
		t.Run("not "+text, func(t *testing.T) {
			if starts, _ := naivePass(splitLines(text + "\n")); len(starts) != 0 {
				t.Errorf("found the title in %q", text)
			}
		})
	}
}

func TestACopyOfTheSectionInACodeBlockIsExplained(t *testing.T) {
	const note = "  note: line %d holds the title of the section but is not read as its heading (a copy of the section in a code block, " +
		"a comment or an HTML block, or a form of heading this check does not read): remove the checkboxes of the copy, or the copy itself"
	tests := []struct {
		name  string
		body  string
		first string
		notes []int
		tail  string // the line that closes the list, when it is not a note
	}{
		{"a copy above a ticked section", "Context.\n\n```markdown\n" + section + "```\n\n" + ticked,
			"merge readiness: ambiguous markup: 4 possible box(es) not ticked, 4 recognized:", []int{4}, msgAmbiguousNote},
		{"a copy and no section", "```\n" + section + "```\n", "merge readiness: ambiguous markup: 4 possible box(es) not ticked, 0 recognized:", []int{2}, msgAmbiguousNote},
		{"a copy with every box ticked and no section", "```\n" + ticked + "```\n", msgNoBoxes, []int{2}, ""},
		{"a heading in an indented block above an unticked section", "    " + heading + "\n" + section, "merge readiness: 4 box(es) not ticked:", []int{1}, ""},
		{"a Setext heading with every box ticked", "Merge readiness\n---\n\n- [x] a\n", msgNoBoxes, []int{1}, ""},
		{"a section and a copy of it in a comment below", section + "\n<!--\n## Merge readiness\n- [ ] x\n-->\n",
			"merge readiness: ambiguous markup: 5 possible box(es) not ticked, 4 recognized:", []int{14}, msgAmbiguousNote},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Judge(tt.body)
			if got.Pass || got.Lines[0] != tt.first {
				t.Fatalf("pass %v, lines %q; want red, first line %q", got.Pass, got.Lines, tt.first)
			}
			var notes []string
			for _, n := range tt.notes {
				notes = append(notes, fmt.Sprintf(note, n))
			}
			tail := got.Lines[len(got.Lines)-len(notes):]
			if strings.Join(tail, "\n") != strings.Join(notes, "\n") {
				t.Errorf("notes %q, want %q", tail, notes)
			}
			if tt.tail != "" && got.Lines[len(got.Lines)-len(notes)-1] != tt.tail {
				t.Errorf("line before the notes %q, want %q", got.Lines[len(got.Lines)-len(notes)-1], tt.tail)
			}
		})
	}
}

func TestAtMostThreeCopiesAreExplained(t *testing.T) {
	got := Judge(strings.Repeat("```\n"+ticked+"```\n", 5))
	var notes int
	for _, l := range got.Lines {
		if strings.HasPrefix(l, notePrefix+"line ") {
			notes++
		}
	}
	if got.Pass || notes != maxNotes {
		t.Errorf("pass %v, %d notes: %q", got.Pass, notes, got.Lines)
	}
}

func TestAnOrdinarySectionHasNoNote(t *testing.T) {
	got := Judge(section)
	for _, l := range got.Lines {
		if strings.HasPrefix(l, notePrefix) {
			t.Errorf("a note on an ordinary red verdict: %q", l)
		}
	}
	if got := Judge(ticked); !got.Pass || len(got.Lines) != 1 {
		t.Errorf("a ticked section: %q", got.Lines)
	}
}
