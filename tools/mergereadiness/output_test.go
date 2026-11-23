package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode"
)

func TestTheUntickedBoxesAreListedInOrder(t *testing.T) {
	got := Judge(section)
	want := []string{
		"merge readiness: 4 box(es) not ticked:",
		"  - [ ] line 5: CI is green on the head commit",
		"  - [ ] line 6: Local checks done",
		"  - [ ] line 7: **Ready for the maintainer to merge**",
		"  - [ ] line 11: a task (reviewer)",
	}
	if strings.Join(got.Lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got.Lines, "\n"), strings.Join(want, "\n"))
	}
}

func TestAnEmptyBoxIsListedWithTheFullPrefix(t *testing.T) {
	if got := Judge(heading + "- [ ]\n"); got.Lines[1] != "  - [ ] line 3: " {
		t.Errorf("got %q", got.Lines[1])
	}
}

// The runner trims the leading blanks of a line before it looks for a workflow command, so what keeps a
// box's text from becoming one is the fixed prefix of the line, and what keeps a control character out
// is the sanitizing.
func TestNoLineOfTheOutputIsAWorkflowCommandOrHoldsAControlCharacter(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"a command", "::error::boom"},
		{"a command after blanks", "   ::add-mask::x"},
		{"an escape sequence", "\x1b[31mred"},
		{"a bell and a delete", "a\x07b\x7fc"},
		{"a line separator", "a\u2028::error::b"},
		{"a next line", "a\u0085::error::b"},
		{"a bidirectional override", "a\u202eb"},
		{"an invalid byte", "a\xffb"},
		{"a vertical tab", "a\vb"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Judge(heading + "- [ ] " + tt.text + "\n")
			if got.Pass || len(got.Lines) != 2 {
				t.Fatalf("Judge = %v %q", got.Pass, got.Lines)
			}
			for _, l := range got.Lines {
				if strings.HasPrefix(strings.TrimLeftFunc(l, unicode.IsSpace), "::") {
					t.Errorf("a workflow command: %q", l)
				}
				if strings.IndexFunc(l, func(r rune) bool { return unicode.IsControl(r) || r == '\u2028' || r == '\u202e' }) >= 0 {
					t.Errorf("a control character: %q", l)
				}
			}
		})
	}
}

func TestALongTextIsCutAtTheLimit(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want string
	}{
		{"one under", 199, strings.Repeat("a", 199)},
		{"at the limit", 200, strings.Repeat("a", 200)},
		{"one over", 201, strings.Repeat("a", 197) + "..."},
		{"far over", 5000, strings.Repeat("a", 197) + "..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Judge(heading + "- [ ] " + strings.Repeat("a", tt.n) + "\n"); got.Lines[1] != boxPrefix+"line 3: "+tt.want {
				t.Errorf("got %d characters: %q", len(got.Lines[1]), got.Lines[1])
			}
		})
	}
}

func TestACutCountsCharactersNotBytes(t *testing.T) {
	text := strings.Repeat("\u00e9", 201)
	want := boxPrefix + "line 3: " + strings.Repeat("\u00e9", 197) + "..."
	if got := Judge(heading + "- [ ] " + text + "\n"); got.Lines[1] != want {
		t.Errorf("got %q", got.Lines[1])
	}
}

func TestTheListIsCutAtFiftyBoxesAndTheRestIsCounted(t *testing.T) {
	tests := []struct {
		boxes int
		lines int
		last  string
	}{
		{49, 50, "  - [ ] line 51: box"},
		{50, 51, "  - [ ] line 52: box"},
		{51, 52, "... and 1 more"},
		{57, 52, "... and 7 more"},
		{120, 52, "... and 70 more"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.boxes), func(t *testing.T) {
			got := Judge(heading + strings.Repeat("- [ ] box\n", tt.boxes))
			if got.Unticked != tt.boxes || len(got.Lines) != tt.lines || got.Lines[len(got.Lines)-1] != tt.last {
				t.Errorf("unticked %d, %d lines, last %q", got.Unticked, len(got.Lines), got.Lines[len(got.Lines)-1])
			}
			if want := fmt.Sprintf("merge readiness: %d box(es) not ticked:", tt.boxes); got.Lines[0] != want {
				t.Errorf("heading %q, want %q", got.Lines[0], want)
			}
		})
	}
}

// The ambiguous verdict counts what the naive reading found, past a hundred as well.
func TestTheAmbiguousListIsCountedPastAHundred(t *testing.T) {
	got := Judge(heading + strings.Repeat("1. [ ] box\n", 130))
	if got.Unticked != 130 || got.Lines[len(got.Lines)-2] != msgAmbiguousNote && got.Lines[len(got.Lines)-1] != msgAmbiguousNote {
		t.Errorf("unticked %d, lines %q", got.Unticked, got.Lines[len(got.Lines)-2:])
	}
	if want := "merge readiness: ambiguous markup: 130 possible box(es) not ticked, 0 recognized:"; got.Lines[0] != want {
		t.Errorf("heading %q, want %q", got.Lines[0], want)
	}
}

// A scan that backtracked would take minutes on these; the bound is far above what a linear scan needs.
// It does not cover the lookup of many titled headings (about half a second at 1 MiB, see doc.go): no input
// here has many lines titled "Merge readiness", and the bound would not catch that case.
func TestHostileLinesAreReadInMomentsWhateverTheyHold(t *testing.T) {
	const bound = 5 * time.Second
	big := 65000
	tests := []struct {
		name string
		body string
	}{
		{"spaces inside a heading", "## a" + strings.Repeat(" ", big) + "b\n" + heading + "- [ ] x\n"},
		{"tabs inside a heading", "## a" + strings.Repeat("\t", big) + "b\n" + heading + "- [ ] x\n"},
		{"spaces and a closing sequence", "## a" + strings.Repeat(" #", big/2) + "\n"},
		{"spaces then hashes in the section heading", "## Merge readiness" + strings.Repeat(" ", big) + "#\n- [ ] x\n"},
		{"a long run of markers", heading + strings.Repeat("- ", big/2) + "[ ] x\n"},
		{"a long run of quotes and blanks", heading + strings.Repeat("> ", big/2) + "- [ ] x\n"},
		{"a long run of digits", heading + strings.Repeat("1", big) + ". [ ] x\n"},
		{"many comments on a line", heading + strings.Repeat("<!--a-->", big/8) + "\n- [x] y\n"},
		{"many unclosed comment openers", heading + strings.Repeat("<!--", big/4) + "\n- [x] y\n"},
		{"many link openers", "a" + strings.Repeat("](", 200000) + "\n" + heading + "- [ ] x\n"},
		{"many tag openers", "a" + strings.Repeat("<a", 200000) + "\n" + heading + "- [ ] x\n"},
		{"many tag openers in a heading", "## " + strings.Repeat("<", big) + "\n" + heading + "- [ ] x\n"},
		{"many fence lines", heading + strings.Repeat("```\n", big) + "- [x] y\n"},
		{"many lines of the filler", strings.Repeat("text with [ ] and <!-- a comment --> and ``` and ## inside a line\n", 20000) + section},
		{"long lines of backticks", heading + strings.Repeat("`", big) + " a`\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			started := time.Now()
			got := Judge(tt.body)
			if elapsed := time.Since(started); elapsed > bound {
				t.Errorf("took %v for %d characters", elapsed, len(tt.body))
			}
			if len(got.Lines) == 0 {
				t.Error("no verdict")
			}
		})
	}
}

func TestALargeBodyIsStillJudgedRight(t *testing.T) {
	filler := strings.Repeat("text with [ ] and <!-- a comment --> and ``` and ## inside a line\n", 20000)
	if got := Judge(filler + section + filler); got.Pass || got.Unticked != 4 {
		t.Errorf("pass %v, unticked %d (%q)", got.Pass, got.Unticked, got.Lines)
	}
}

// writeBody writes the description to a file and returns its path.
func writeBody(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunPrintsTheVerdictAndReturnsTheExitStatus(t *testing.T) {
	tests := []struct {
		name string
		body string
		code int
		out  string
	}{
		{"an empty file passes", "", 0, msgNoSection + "\n"},
		{"a ticked description passes", ticked, 0, msgAllTicked + "\n"},
		{"an unticked box fails", heading + "- [ ] a\n", 1, "merge readiness: 1 box(es) not ticked:\n  - [ ] line 3: a\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			if code := Run([]string{"-body-file", writeBody(t, tt.body)}, &out); code != tt.code || out.String() != tt.out {
				t.Errorf("code %d, output %q; want %d, %q", code, out.String(), tt.code, tt.out)
			}
		})
	}
}

func TestRunRefusesACommandLineItDoesNotUnderstand(t *testing.T) {
	file := writeBody(t, ticked)
	tests := []struct {
		name string
		args []string
	}{
		{"no arguments", nil},
		{"an empty file name", []string{"-body-file", ""}},
		{"an unknown flag", []string{"-body", file}},
		{"a flag without its value", []string{"-body-file"}},
		{"an extra argument", []string{"-body-file", file, "more"}},
		{"only a name", []string{file}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			if code := Run(tt.args, &out); code != exitUsage || out.String() != msgUsage+"\n" {
				t.Errorf("code %d, output %q", code, out.String())
			}
		})
	}
}

func TestRunDoesNotReadTheEnvironment(t *testing.T) {
	t.Setenv("PR_BODY", section)
	var out strings.Builder
	if code := Run([]string{"-body-file", writeBody(t, "")}, &out); code != exitPass || out.String() != msgNoSection+"\n" {
		t.Errorf("code %d, output %q", code, out.String())
	}
}

func TestRunIsRedWhenTheDescriptionCannotBeRead(t *testing.T) {
	var out strings.Builder
	missing := filepath.Join(t.TempDir(), "absent.md")
	if code := Run([]string{"-body-file", missing}, &out); code != exitRed || !strings.HasPrefix(out.String(), "merge readiness: the description could not be read: ") {
		t.Errorf("a missing file: code %d, output %q", code, out.String())
	}
	out.Reset()
	if code := Run([]string{"-body-file", t.TempDir()}, &out); code != exitRed || !strings.HasPrefix(out.String(), "merge readiness: the description could not be read: ") {
		t.Errorf("a directory: code %d, output %q", code, out.String())
	}
}

// The limit is the documented one, 1 MiB, whatever the constant says: the sizes are literal.
func TestRunPinsTheLimitAtOneMebibyteByLiteralSizes(t *testing.T) {
	tests := []struct {
		name string
		size int
		code int
		out  string
	}{
		{"1048576 bytes are judged", 1048576, exitPass, msgNoSection + "\n"},
		{"1048577 bytes are refused", 1048577, exitRed, "merge readiness: the description is larger than 1 MiB: shorten it\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			if code := Run([]string{"-body-file", writeBody(t, strings.Repeat("a", tt.size))}, &out); code != tt.code || out.String() != tt.out {
				t.Errorf("code %d, output %q; want %d, %q", code, out.String(), tt.code, tt.out)
			}
		})
	}
}

// The naive reading reads to the end of the description, however many lines it has.
func TestAnUntickedBoxAfterHundredsOfThousandsOfLinesIsStillRed(t *testing.T) {
	filler := strings.Repeat("a\n", 500000)
	tests := []struct {
		name string
		body string
	}{
		{"a plain box", ticked + filler + "- [ ] last\n"},
		{"a box only the naive reading knows", ticked + filler + "1. [ ] last\n"},
		{"a footnote box", ticked + filler + "[^1]: - [ ] last\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.body) > maxBody {
				t.Fatalf("the body is %d bytes, over the limit", len(tt.body))
			}
			got := Judge(tt.body)
			if got.Pass || got.Unticked != 1 {
				t.Errorf("pass %v, unticked %d: %q", got.Pass, got.Unticked, got.Lines)
			}
		})
	}
	var out strings.Builder
	if code := Run([]string{"-body-file", writeBody(t, ticked+filler+"1. [ ] last\n")}, &out); code != exitRed {
		t.Errorf("Run on the same description returned %d: %q", code, out.String())
	}
}

func TestRunRefusesADescriptionOverOneMebibyte(t *testing.T) {
	tests := []struct {
		name string
		size int
		code int
		out  string
	}{
		{"exactly the limit", maxBody, exitPass, msgNoSection + "\n"},
		{"one byte over", maxBody + 1, exitRed, msgTooLarge + "\n"},
		{"far over", 3 * maxBody, exitRed, msgTooLarge + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out strings.Builder
			if code := Run([]string{"-body-file", writeBody(t, strings.Repeat("a", tt.size))}, &out); code != tt.code || out.String() != tt.out {
				t.Errorf("code %d, output %q; want %d, %q", code, out.String(), tt.code, tt.out)
			}
		})
	}
}

// failingWriter refuses every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestRunFailsWhenTheVerdictCannotBeWritten(t *testing.T) {
	if code := Run([]string{"-body-file", writeBody(t, ticked)}, failingWriter{}); code != exitRed {
		t.Errorf("code %d, want %d", code, exitRed)
	}
}
