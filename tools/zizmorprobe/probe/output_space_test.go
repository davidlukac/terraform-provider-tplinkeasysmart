package probe

import (
	"strings"
	"testing"
)

// A line of the output that starts with a forbidden prefix is reported whatever white space comes before the
// prefix: every character unicode.IsSpace accepts, not only a space and a tab.
func TestOutputProblemsSkipsEveryKindOfWhiteSpaceBeforeAForbiddenPrefix(t *testing.T) {
	forbidden := probeCase{noLineStartsWith: []string{"::"}}
	for _, tc := range []struct {
		name   string
		output string
		want   bool // whether a problem is reported
	}{
		{name: "no white space", output: "::warning::x", want: true},
		{name: "a space", output: " ::warning::x", want: true},
		{name: "a tab", output: "\t::warning::x", want: true},
		{name: "a form feed", output: "\f::warning::x", want: true},
		{name: "a vertical tab", output: "\v::warning::x", want: true},
		{name: "a carriage return", output: "\r::warning::x", want: true},
		{name: "a no-break space (U+00A0)", output: "\u00a0::warning::x", want: true},
		{name: "a next line (U+0085)", output: "\u0085::warning::x", want: true},
		{name: "a line separator (U+2028)", output: "\u2028::warning::x", want: true},
		{name: "a paragraph separator (U+2029)", output: "\u2029::warning::x", want: true},
		{name: "an ideographic space (U+3000)", output: "\u3000::warning::x", want: true},
		{name: "several kinds in a row", output: " \t\f\v\u00a0\u0085\u2028::warning::x", want: true},
		{name: "on a later line", output: "zizmor: a\n\f::warning::x\n", want: true},
		{name: "the prefix after other text", output: "zizmor: ::warning::x", want: false},
		{name: "white space only", output: "\f\v\u00a0\u0085\u2028", want: false},
		{name: "a character that is not white space before it", output: "\u200b::warning::x", want: false},
		{name: "no line", output: "", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problems := outputProblems(forbidden, tc.output)
			if got := len(problems) > 0; got != tc.want {
				t.Fatalf("problems %q, reported = %v, want %v", problems, got, tc.want)
			}
			if tc.want && !strings.Contains(problems[0], "starts with") {
				t.Errorf("problem %q does not say that a line starts with the prefix", problems[0])
			}
		})
	}
}
