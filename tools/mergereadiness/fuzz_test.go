package main

import (
	"strings"
	"testing"
	"unicode"
)

// checkOutput fails the test for a verdict whose output holds a control character or a line that
// would be read as a workflow command.
func checkOutput(t *testing.T, v Verdict) {
	t.Helper()
	for _, l := range v.Lines {
		if strings.HasPrefix(strings.TrimLeftFunc(l, unicode.IsSpace), "::") {
			t.Fatalf("a workflow command in the output: %q", l)
		}
		for _, r := range l {
			if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
				t.Fatalf("a control character %U in the output line %q", r, l)
			}
		}
	}
}

// injected returns the ticked template with an unticked box inserted after the heading of its section, at
// the line chosen by at, preceded by whatever the fuzzer put before it (middle), and the whole between a
// prefix and a suffix that the fuzzer supplies. The box is a plain line of its own.
func injected(prefix, middle, suffix string, at uint) string {
	lines := strings.Split(strings.TrimSuffix(ticked, "\n"), "\n")
	at = 1 + at%uint(len(lines))
	withBox := append(append(append([]string{}, lines[:at]...), middle, "- [ ] injected"), lines[at:]...)
	return prefix + "\n" + strings.Join(withBox, "\n") + "\n" + suffix
}

// FuzzJudge: whatever the description holds, the judgment ends, its output holds no control character and no
// line that starts a workflow command, and an unticked box inserted inside the section of an otherwise
// ticked template is never green, whatever stands before and after the section and whatever stands between
// the heading and the box (heading-like lines of every kind, fences, comments, HTML blocks, quotes).
//
// What this proves, and what it does not: the naive reading knows no structure and never ends the section,
// so it counts the injected line whatever stands around it, and that is what the target exercises (a
// weakening of it, such as an end-of-section rule, is found at once). A regression of the structured
// reading is masked by the naive one: it is covered by the unit tests and by the oracle, not by this target.
func FuzzJudge(f *testing.F) {
	for _, seed := range []string{
		"", section, ticked, heading + "- [ ] a\n", "```\n" + section + "```\n", "<!--\n" + section, "- a\n  ```\n" + section,
		"> ## Merge readiness\n> - [ ] a\n", "## Merge readiness\n1. [ ] a\n", "<!-- c --> ```\n" + heading,
		"## a" + strings.Repeat(" ", 100) + "b", "::error::x\n" + heading + "- [ ] ::error::y\n", "\r\n\r\r\n", "\xff\xfe",
	} {
		f.Add(seed, "", "", uint(0))
	}
	for _, middle := range []string{
		"```sh\n# run this\n```", "<!--\n# c\n-->", "<div>\n## Other\n</div>", "<pre>\n# Other\n</pre>", "<div>\n```\n# x\n</div>",
		"<!-->\n\n    # x", "    # x", "# x", "Notes\n---", "<!-- c --> ## Other", "> ## Other", "### x", "<details>\n## x\n</details>",
		"1. [x] a", "~~~\n## x\n~~~", "<h2>Other</h2>", "##", "\u00a0## x", "| a | b |\n|---|---|\n| - [ ] x | y |",
		"text[^1]\n\n[^1]: a note", "[^1]: - [x] done",
	} {
		f.Add("", middle, "", uint(2))
	}
	f.Add("<!--", "", "-->", uint(3))
	f.Add("```", "", "```", uint(5))
	f.Add("<div>\n```", "", "</div>", uint(1))
	f.Add("[ ]: x\n", "", "\n## Merge readiness\n", uint(2))
	f.Fuzz(func(t *testing.T, body, middle, suffix string, at uint) {
		checkOutput(t, Judge(body))
		if v := Judge(injected(body, middle, suffix, at)); v.Pass {
			t.Fatalf("green with an unticked box injected (prefix %q, middle %q, suffix %q, line %d): %q", body, middle, suffix, at, v.Lines)
		}
	})
}
