package audit

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// feed writes the chunks to a lineEscaper, closes it and returns what it wrote.
func feed(chunks ...string) string {
	var out bytes.Buffer
	l := &lineEscaper{out: &out}
	for _, c := range chunks {
		if n, err := l.Write([]byte(c)); n != len(c) || err != nil {
			panic("a write that is not whole")
		}
	}
	l.Close()
	return out.String()
}

func TestLineEscaper(t *testing.T) {
	long := strings.Repeat("x", 300)
	veryLong := strings.Repeat("y", maxLineBytes+100)
	for _, tc := range []struct {
		name   string
		chunks []string
		want   string
	}{
		{name: "nothing", chunks: nil, want: ""},
		{name: "a line", chunks: []string{"INFO audit: completed a.yml\n"}, want: "zizmor: INFO audit: completed a.yml\n"},
		{name: "a line without its end", chunks: []string{"no end"}, want: "zizmor: no end\n"},
		{name: "a line in pieces", chunks: []string{"a", "b", "c\nd", "e\n"}, want: "zizmor: abc\nzizmor: de\n"},
		{name: "an empty line", chunks: []string{"a\n\nb\n"}, want: linePrefix + "a\n" + linePrefix + "\n" + linePrefix + "b\n"},
		{name: "a carriage return before the end", chunks: []string{"a\r\nb\r\n"}, want: "zizmor: a\nzizmor: b\n"},
		{name: "a new line and a workflow command in a file name", chunks: []string{"INFO audit: completed a\n::warning::x.yml\n"},
			want: "zizmor: INFO audit: completed a\nzizmor: ::warning::x.yml\n"},
		{name: "a line that starts with a workflow command", chunks: []string{"::error::x\n"}, want: "zizmor: ::error::x\n"},
		{name: "a control character and an escape sequence", chunks: []string{"a\x00\x1b[31mb\tc\n"}, want: "zizmor: a\\x00\\x1b[31mb\\tc\n"},
		{name: "characters outside ASCII", chunks: []string{"\u00e9\n"}, want: "zizmor: \\u00e9\n"},
		{name: "a long line is cut to 200 characters", chunks: []string{long + "\n"}, want: "zizmor: " + strings.Repeat("x", 197) + "...\n"},
		{name: "a line over the bytes kept ends in an ellipsis", chunks: []string{veryLong + "\n"}, want: "zizmor: " + strings.Repeat("y", 197) + "......\n"},
		{name: "a line over the bytes kept, in pieces", chunks: []string{veryLong[:500], veryLong[500:] + "\nz\n"}, want: "zizmor: " + strings.Repeat("y", 197) + "......\nzizmor: z\n"},
		{name: "a line of exactly the bytes kept is not marked as cut", chunks: []string{strings.Repeat("y", 150) + "\n"}, want: "zizmor: " + strings.Repeat("y", 150) + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := feed(tc.chunks...); got != tc.want {
				t.Errorf("output = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLineEscaperCutsAtTheLimitOfBytes(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int
		cut  bool
	}{
		{name: "one byte under", size: maxLineBytes - 1, cut: false},
		{name: "exactly the limit", size: maxLineBytes, cut: false},
		{name: "one byte over", size: maxLineBytes + 1, cut: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			l := &lineEscaper{out: &out}
			if _, err := l.Write([]byte(strings.Repeat("\u00e9", tc.size/2) + strings.Repeat("a", tc.size%2) + "\n")); err != nil {
				t.Fatal(err)
			}
			if l.written != 1 {
				t.Fatalf("%d lines written, want 1", l.written)
			}
			if got := strings.HasSuffix(strings.TrimSuffix(out.String(), "\n"), "......"); got != tc.cut {
				t.Errorf("output %q: cut marker = %v, want %v", out.String(), got, tc.cut)
			}
		})
	}
}

func TestLineEscaperForwardsAtMostTwoHundredLines(t *testing.T) {
	for _, tc := range []struct {
		name    string
		lines   int
		dropped int
	}{
		{name: "at the limit", lines: maxForwardedLines, dropped: 0},
		{name: "one over", lines: maxForwardedLines + 1, dropped: 1},
		{name: "many over", lines: maxForwardedLines + 50, dropped: 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := feed(strings.Repeat("line\n", tc.lines))
			lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
			want := maxForwardedLines
			if tc.dropped > 0 {
				want++
			}
			if len(lines) != want {
				t.Fatalf("%d lines, want %d", len(lines), want)
			}
			last := lines[len(lines)-1]
			if tc.dropped > 0 && last != "zizmor: and "+strconv.Itoa(tc.dropped)+" more lines of its output were left out" {
				t.Errorf("last line = %q", last)
			}
			if tc.dropped == 0 && strings.Contains(got, "left out") {
				t.Errorf("a count of dropped lines when none was dropped: %q", got)
			}
		})
	}
}

// maxEscapedLine is the longest line the escaper can write, which is what it promises: the prefix, the 200
// characters verdict.Escape keeps, each of them at most ten characters long (a character outside the first
// planes is written as \UXXXXXXXX, ten characters), and the "..." that marks a line cut before it was escaped. A
// line of more than 200 characters that Escape cuts has 197 of them and its own "...", which is less.
const maxEscapedLine = len(linePrefix) + 200*10 + len(ellipsis)

// The longest lines the escaper writes: the bound above is reached, and not exceeded.
func TestLineEscaperLongestLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want int
	}{
		{name: "200 characters of four bytes are all the bytes kept allow, and the line is not marked as cut", text: strings.Repeat("\U0001f308", 200), want: len(linePrefix) + 200*10},
		{name: "201 characters of four bytes: the bytes kept end at 200 characters and the line is marked as cut, the longest line", text: strings.Repeat("\U0001f308", 201), want: maxEscapedLine},
		{name: "201 characters of two bytes are cut by the escaper to 197", text: strings.Repeat("\u00e9", 201), want: len(linePrefix) + 197*6 + 3},
		{name: "invalid bytes are four characters each", text: strings.Repeat("\xff", 200), want: len(linePrefix) + 200*4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.TrimSuffix(feed(tc.text+"\n"), "\n")
			if len(got) != tc.want {
				t.Errorf("a line of %d bytes, want %d: %.60s", len(got), tc.want, got)
			}
			if len(got) > maxEscapedLine {
				t.Errorf("a line longer than the bound: %d", len(got))
			}
		})
	}
}

// FuzzLineEscaper: whatever zizmor writes, in whatever pieces, every line that comes out starts with the prefix
// (so none starts a workflow command), is printable ASCII with no end of line inside it, and is no longer than
// the bound, and at most the limit of lines (and one more for the count) come out.
func FuzzLineEscaper(f *testing.F) {
	for _, seed := range []string{"", "a\n", "a\n::warning::x\n", "\x1b[31m\n", strings.Repeat("x", 3000), "\u202e\n\r\n", "::error::\n::error::", strings.Repeat("l\n", 300)} {
		f.Add([]byte(seed), 1)
	}
	f.Fuzz(func(t *testing.T, data []byte, split int) {
		if split < 0 {
			split = -split
		}
		at := 0
		if len(data) > 0 {
			at = split % (len(data) + 1)
		}
		out := feed(string(data[:at]), string(data[at:]))
		if out != "" && !strings.HasSuffix(out, "\n") {
			t.Fatalf("output does not end with a new line: %q", out)
		}
		lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		if out == "" {
			lines = nil
		}
		if len(lines) > maxForwardedLines+1 {
			t.Fatalf("%d lines came out", len(lines))
		}
		for _, line := range lines {
			if !strings.HasPrefix(line, linePrefix) || len(line) > maxEscapedLine {
				t.Fatalf("a line without the prefix or too long: %q", line)
			}
			for _, r := range line {
				if r > unicode.MaxASCII || !unicode.IsPrint(r) {
					t.Fatalf("an unprintable character %U in %q", r, line)
				}
			}
		}
	})
}
