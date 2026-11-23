package main

import (
	"reflect"
	"testing"
)

func TestHeadingOf(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		level int
		title string
		ok    bool
	}{
		{"level 2", "## Merge readiness", 2, "merge readiness", true},
		{"level 1", "# A", 1, "a", true},
		{"level 6", "###### A", 6, "a", true},
		{"level 7 is text", "####### A", 0, "", false},
		{"no space after the marker is text", "#hashtag", 0, "", false},
		{"no marker", "text", 0, "", false},
		{"empty line", "", 0, "", false},
		{"only spaces", "  ", 0, "", false},
		{"only three spaces", "   ", 0, "", false},
		{"a marker alone is an empty heading", "##", 2, "", true},
		{"a marker and blanks", "##  \t", 2, "", true},
		{"a tab after the marker", "##\tTitle", 2, "title", true},
		{"three spaces before", "   ## A", 2, "a", true},
		{"four spaces before make code", "    ## A", 0, "", false},
		{"a tab before makes code", "\t## A", 0, "", false},
		{"collapsed whitespace and case", "##  Merge \t  READINESS ", 2, "merge readiness", true},
		{"no-break space in the title is whitespace", "## Merge\u00a0readiness", 2, "merge readiness", true},
		{"closing sequence", "## A ##", 2, "a", true},
		{"closing sequence with a tab", "## A\t###", 2, "a", true},
		{"only a closing sequence", "## ##", 2, "", true},
		{"a hash glued to the title is part of it", "## A#", 2, "a#", true},
		{"emphasis in the title", "## **Merge readiness**", 2, "merge readiness", true},
		{"code marks and a closing sequence", "## `Merge readiness` ##", 2, "merge readiness", true},
		{"a closing sequence of any length", "## A #", 2, "a", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			level, title, ok := headingOf(line(tt.line))
			if level != tt.level || title != tt.title || ok != tt.ok {
				t.Errorf("headingOf(%q) = %d, %q, %v; want %d, %q, %v", tt.line, level, title, ok, tt.level, tt.title, tt.ok)
			}
		})
	}
}

func TestNaiveTitle(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{"an ATX heading", "## A b", "a b"},
		{"a quoted heading", "> ## A", "a"},
		{"a nested quote", "> > # A", "a"},
		{"a quote without a space", ">## A", "a"},
		{"a deep indent", "        ## A", "a"},
		{"a no-break space before", "\u00a0## A", "a"},
		{"a plain line", "Some  Text", "some text"},
		{"a line in bold type", "**Some Text**", "some text"},
		{"a heading in HTML", "<h2 id=\"x\">Some Text</h2>", "some text"},
		{"a quoted plain line", "> Some Text", "some text"},
		{"a list item keeps its marker", "- Some Text", "- some text"},
		{"an empty line", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := naiveTitle(line(tt.line)); got != tt.want {
				t.Errorf("naiveTitle(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

func TestStripInline(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"nothing to strip", "plain text", "plain text"},
		{"emphasis", "**a** _b_ ~~c~~", "a b c"},
		{"code marks", "`a`", "a"},
		{"a link", "[a](http://example.com/x_y)", "a"},
		{"a link with an unclosed target", "[a](b", "a(b"},
		{"brackets without a target", "[a] b", "a b"},
		{"a bracket and a parenthesis apart", "[a] (b)", "a (b)"},
		{"tags", "<b>a</b> <i>b</i>", "a b"},
		{"a tag with attributes", "<a href=\"x\">a</a>", "a"},
		{"a less-than sign that opens nothing", "a < b", "a < b"},
		{"an unclosed tag", "a <b", "a <b"},
		{"two unclosed tags", "<a <b", "<a <b"},
		{"two unclosed link targets", "[a](b [c](d", "a(b c(d"},
		{"a tag that contains another opener", "<a <b>", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(stripInline(line(tt.text))); got != tt.want {
				t.Errorf("stripInline(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestFenceMarker(t *testing.T) {
	tests := []struct {
		name string
		line string
		want string
	}{
		{"three backticks", "```", "```"},
		{"three tildes", "~~~", "~~~"},
		{"a longer run", "`````", "`````"},
		{"an info string", "```go", "```"},
		{"three spaces before", "   ```", "```"},
		{"four spaces before make code", "    ```", ""},
		{"two backticks", "``", ""},
		{"two backticks after three spaces", "   ``", ""},
		{"three backticks after three spaces", "   ```", "```"},
		{"only spaces", "   ", ""},
		{"two tildes", "~~", ""},
		{"text", "text", ""},
		{"empty", "", ""},
		{"a backtick in the info string of a backtick fence", "``` a`b", ""},
		{"a backtick in the info string of a tilde fence", "~~~ a`b", "~~~"},
		{"a tilde in the info string of a backtick fence", "``` a~b", "```"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(fenceMarker(line(tt.line))); got != tt.want {
				t.Errorf("fenceMarker(%q) = %q, want %q", tt.line, got, tt.want)
			}
		})
	}
}

func TestClosesFence(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		fence string
		want  bool
	}{
		{"the same", "```", "```", true},
		{"a longer one", "`````", "```", true},
		{"a shorter one", "```", "````", false},
		{"another character", "~~~", "```", false},
		{"text after it", "``` text", "```", false},
		{"blanks after it", "```  \t", "```", true},
		{"three spaces before", "   ```", "```", true},
		{"four spaces before", "    ```", "```", false},
		{"tildes with blanks after", "~~~ ", "~~~", true},
		{"tildes after a blank", "~~~ ~~~", "~~~", false},
		{"not a fence", "text", "```", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := closesFence(line(tt.line), line(tt.fence)); got != tt.want {
				t.Errorf("closesFence(%q, %q) = %v, want %v", tt.line, tt.fence, got, tt.want)
			}
		})
	}
}

func TestStrictBox(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		unticked bool
		text     string
		ok       bool
	}{
		{"unticked", "- [ ] a box", true, "a box", true},
		{"ticked", "- [x] a box", false, "a box", true},
		{"ticked with a capital", "- [X] a box", false, "a box", true},
		{"star", "* [ ] a", true, "a", true},
		{"plus", "+ [ ] a", true, "a", true},
		{"indented", "    - [ ] a", true, "a", true},
		{"a tab before", "\t- [ ] a", true, "a", true},
		{"a tab after the marker", "-\t[ ] a", true, "a", true},
		{"no text", "- [ ]", true, "", true},
		{"blanks after the box", "- [ ]  ", true, "", true},
		{"a tab after the box", "- [ ]\ta", true, "a", true},
		{"no space after the marker", "-[ ] a", false, "", false},
		{"nothing after the marker", "-", false, "", false},
		{"no list marker", "[ ] a", false, "", false},
		{"empty brackets", "- [] a", false, "", false},
		{"two spaces in the brackets", "- [  ] a", false, "", false},
		{"another letter", "- [y] a", false, "", false},
		{"text glued to the box", "- [ ]a", false, "", false},
		{"a short line", "- [", false, "", false},
		{"a box that is not closed", "- [ x", false, "", false},
		{"a ticked box that is not closed", "- [x y", false, "", false},
		{"a box that is not first", "- a [ ] b", false, "", false},
		{"an ordered item", "1. [ ] a", false, "", false},
		{"empty line", "", false, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			unticked, text, ok := strictBox(line(tt.line))
			if unticked != tt.unticked || text != tt.text || ok != tt.ok {
				t.Errorf("strictBox(%q) = %v, %q, %v; want %v, %q, %v", tt.line, unticked, text, ok, tt.unticked, tt.text, tt.ok)
			}
		})
	}
}

func TestNaiveBox(t *testing.T) {
	tests := []struct {
		name string
		line string
		text string
		ok   bool
	}{
		{"plain", "- [ ] a", "a", true},
		{"star", "* [ ] a", "a", true},
		{"ordered with a dot", "1. [ ] a", "a", true},
		{"ordered with a parenthesis", "12) [ ] a", "a", true},
		{"nested", "- - [ ] a", "a", true},
		{"nested ordered", "1. - [ ] a", "a", true},
		{"quoted", "> - [ ] a", "a", true},
		{"nested quotes", "> > 1. [ ] a", "a", true},
		{"indented", "        - [ ] a", "a", true},
		{"a no-break space in the box", "- [\u00a0] a", "a", true},
		{"a tab in the box", "- [\t] a", "a", true},
		{"a no-break space after the marker", "-\u00a0[ ] a", "a", true},
		{"no text", "- [ ]", "", true},
		{"text glued to the box", "- [ ]a", "a", true},
		{"a footnote definition", "[^1]: - [ ] a", "a", true},
		{"a footnote definition with a long label", "[^a long-label_1]: 1. [ ] a", "a", true},
		{"a footnote definition after three spaces", "   [^1]: - [ ] a", "a", true},
		{"a footnote definition in a quote", "> [^1]: - [ ] a", "a", true},
		{"a footnote definition with no blank after the colon", "[^1]:- [ ] a", "a", true},
		{"a footnote definition and a nested marker", "[^1]: - - [ ] a", "a", true},
		{"a footnote definition without a list marker", "[^1]: [ ] a", "", false},
		{"a footnote definition with text only", "[^1]: a note", "", false},
		{"a footnote label that is not closed", "[^1 - [ ] a", "", false},
		{"a footnote label without its colon", "[^1] - [ ] a", "", false},
		{"a footnote label opening only", "[^", "", false},
		{"a ticked box", "- [x] a", "", false},
		{"no marker", "[ ] a", "", false},
		{"no space after the marker", "-[ ] a", "", false},
		{"nothing after the marker", "-", "", false},
		{"a number without a dot", "1 [ ] a", "", false},
		{"nine digits are a marker", "123456789. [ ] a", "a", true},
		{"nine digits after a quote are a marker", "> 123456789. [ ] a", "a", true},
		{"ten digits after a quote are no marker", "> 1234567890. [ ] a", "", false},
		{"a dot is no marker", ". [ ] a", "", false},
		{"a parenthesis is no marker", ") [ ] a", "", false},
		{"a number at the end of the line", "1", "", false},
		{"a number at the end of a quoted line", "> 12", "", false},
		{"too many digits for a marker", "1234567890. [ ] a", "", false},
		{"a marker and nothing else", "- a", "", false},
		{"empty brackets", "- [] a", "", false},
		{"two spaces in the brackets", "- [  ] a", "", false},
		{"a short line", "- [ ", "", false},
		{"a box that is not closed", "- [ x", "", false},
		{"empty line", "", "", false},
		{"only quotes", ">>", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text, ok := naiveBox(line(tt.line))
			if text != tt.text || ok != tt.ok {
				t.Errorf("naiveBox(%q) = %q, %v; want %q, %v", tt.line, text, ok, tt.text, tt.ok)
			}
		})
	}
}

func TestSplitLinesAcceptsEveryLineEnding(t *testing.T) {
	want := []line{line("a"), line("b"), line("c"), line("")}
	for name, body := range map[string]string{"LF": "a\nb\nc\n", "CRLF": "a\r\nb\r\nc\r\n", "CR": "a\rb\rc\r"} {
		t.Run(name, func(t *testing.T) {
			if got := splitLines(body); !reflect.DeepEqual(got, want) {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}
