package verdict

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

// jsonString is the JSON text of a string.
func jsonString(text string) string {
	encoded, err := json.Marshal(text)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// findingJSON returns the JSON of one finding of the given rule, with a description and a first location.
func findingJSON(rule, desc, path string, row int) string {
	return `{"ident":` + jsonString(rule) + `,"desc":` + jsonString(desc) + `,"locations":[{"symbolic":{"key":{"Local":{"verbatim_path":` +
		jsonString(path) + `}}},"concrete":{"location":{"start_point":{"row":` + strconv.Itoa(row) + `,"column":3}}}}]}`
}

// list joins findings into an answer.
func list(findings ...string) []byte { return []byte("[" + strings.Join(findings, ",") + "]") }

// repeat returns n copies of the finding.
func repeat(n int, f string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = f
	}
	return out
}

// guarantee: ParseExpected
func TestParseExpected(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want int
		fail bool
	}{
		{name: "zero", text: "0", want: 0},
		{name: "one digit", text: "7", want: 7},
		{name: "the number in the Makefile", text: "13", want: 13},
		{name: "the most digits", text: "999999", want: 999999},
		{name: "one digit too many", text: "1000000", fail: true},
		{name: "absurdly long", text: strings.Repeat("9", 4000), fail: true},
		{name: "a number that overflows", text: "99999999999999999999", fail: true},
		{name: "empty", text: "", fail: true},
		{name: "a leading plus", text: "+1", fail: true},
		{name: "a leading minus", text: "-1", fail: true},
		{name: "minus zero", text: "-0", fail: true},
		{name: "a leading space", text: " 1", fail: true},
		{name: "a trailing space", text: "1 ", fail: true},
		{name: "a trailing newline", text: "1\n", fail: true},
		{name: "only a space", text: " ", fail: true},
		{name: "a leading zero (octal to one reader, decimal to another)", text: "010", fail: true},
		{name: "two zeros", text: "00", fail: true},
		{name: "a float", text: "1.0", fail: true},
		{name: "an exponent", text: "1e2", fail: true},
		{name: "a hexadecimal number", text: "0x10", fail: true},
		{name: "an underscore", text: "1_0", fail: true},
		{name: "a letter", text: "1x", fail: true},
		{name: "a word", text: "all", fail: true},
		{name: "Arabic-Indic digits", text: "٣", fail: true},
		{name: "a NUL byte", text: "1\x00", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseExpected(tc.text)
			if tc.fail {
				if err == nil {
					t.Fatalf("ParseExpected(%q) = %d, want an error", tc.text, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("ParseExpected(%q) = %d, %v; want %d", tc.text, got, err, tc.want)
			}
		})
	}
}

func TestParseExpectedErrorsDoNotRepeatControlCharacters(t *testing.T) {
	_, err := ParseExpected("1\n::error::x\x1b[31m")
	if err == nil {
		t.Fatal("no error")
	}
	if !printableLines(err.Error()) {
		t.Errorf("the error carries unprintable text: %q", err)
	}
}

// printableLines reports whether the text is one line of printable ASCII.
func printableLines(text string) bool {
	for _, r := range text {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

const (
	allowed = "self-repository"
	other   = "template-injection"
	site    = "./.github/workflows/zz.yml"
)

func TestJudgePasses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		expected int
		answer   []byte
	}{
		{name: "no findings, none expected", expected: 0, answer: []byte("[]")},
		{name: "white space around an empty list", expected: 0, answer: []byte(" \t\r\n[ ]\n\n")},
		{name: "one allowed finding", expected: 1, answer: list(findingJSON(allowed, "d", site, 1))},
		{name: "thirteen allowed findings", expected: 13, answer: list(repeat(13, findingJSON(allowed, "d", site, 1))...)},
		{name: "a finding with the bare minimum, a rule name", expected: 1, answer: []byte(`[{"ident":"self-repository"}]`)},
		{name: "extra keys are ignored", expected: 1, answer: []byte(`[{"ident":"self-repository","ignored":true,"fixes":[],"x":{"y":[1,2]}}]`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Judge(tc.expected, tc.answer)
			if !got.Pass {
				t.Fatalf("Judge = %q, want a pass", got.Lines)
			}
			want := "zizmor: no findings but the " + strconv.Itoa(tc.expected) + " allowed ones"
			if len(got.Lines) != 1 || got.Lines[0] != want {
				t.Errorf("lines = %q, want [%q]", got.Lines, want)
			}
		})
	}
}

func TestJudgeFailsOnTheNumberOfAllowedFindings(t *testing.T) {
	for _, tc := range []struct {
		name     string
		expected int
		have     int
	}{
		{name: "one fewer", expected: 13, have: 12},
		{name: "one more", expected: 13, have: 14},
		{name: "none, one expected", expected: 1, have: 0},
		{name: "one, none expected", expected: 0, have: 1},
		{name: "none expected, many found", expected: 0, have: 13},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Judge(tc.expected, list(repeat(tc.have, findingJSON(allowed, "d", site, 1))...))
			if got.Pass {
				t.Fatal("passed")
			}
			wantTail := "zizmor findings: " + strconv.Itoa(tc.have) + " of the allowed rule (self-repository), 0 of other rules; " +
				"allowed exactly " + strconv.Itoa(tc.expected) + " of the first and none of the second (ZIZMOR_SELF_REPOSITORY_FINDINGS in the Makefile)"
			if len(got.Lines) != 1 || got.Lines[0] != wantTail {
				t.Errorf("lines = %q, want [%q]", got.Lines, wantTail)
			}
		})
	}
}

func TestJudgeFailsOnAFindingOfAnotherRule(t *testing.T) {
	for _, tc := range []struct {
		name     string
		expected int
		answer   []byte
		counts   string
	}{
		{name: "the allowed ones exact, one other", expected: 2, answer: list(findingJSON(allowed, "d", site, 1), findingJSON(other, "d", site, 1), findingJSON(allowed, "d", site, 1)), counts: "2 of the allowed rule (self-repository), 1 of other rules; allowed exactly 2"},
		{name: "only one other", expected: 0, answer: list(findingJSON(other, "d", site, 1)), counts: "0 of the allowed rule (self-repository), 1 of other rules; allowed exactly 0"},
		{name: "two others, fewer allowed", expected: 3, answer: list(findingJSON(other, "d", site, 1), findingJSON("excessive-permissions", "d", site, 2), findingJSON(allowed, "d", site, 1)), counts: "1 of the allowed rule (self-repository), 2 of other rules; allowed exactly 3"},
		{name: "a rule whose name starts with the allowed one", expected: 1, answer: list(findingJSON("self-repository-extra", "d", site, 1)), counts: "0 of the allowed rule"},
		{name: "a rule whose name differs in case", expected: 1, answer: list(findingJSON("Self-Repository", "d", site, 1)), counts: "0 of the allowed rule"},
		{name: "a rule whose name has a trailing space", expected: 1, answer: list(findingJSON("self-repository ", "d", site, 1)), counts: "0 of the allowed rule"},
		{name: "a rule whose name has a leading space", expected: 1, answer: list(findingJSON(" self-repository", "d", site, 1)), counts: "0 of the allowed rule"},
		{name: "a rule whose name ends in a NUL", expected: 1, answer: list(findingJSON("self-repository\x00", "d", site, 1)), counts: "0 of the allowed rule"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Judge(tc.expected, tc.answer)
			if got.Pass {
				t.Fatal("passed")
			}
			last := got.Lines[len(got.Lines)-1]
			if !strings.Contains(last, tc.counts) {
				t.Errorf("last line = %q, want it to hold %q", last, tc.counts)
			}
			if len(got.Lines) < 2 {
				t.Errorf("lines = %q, want the finding of the other rule listed before the counts", got.Lines)
			}
		})
	}
}

func TestJudgeNamesTheOtherFindingsPlaceDescriptionAndRule(t *testing.T) {
	got := Judge(0, list(findingJSON(other, "code injection via template expansion", "./.github/workflows/zz-probe.yml", 11)))
	want := `zizmor: "./.github/workflows/zz-probe.yml":12: "code injection via template expansion" ("template-injection")`
	if got.Pass || len(got.Lines) != 2 || got.Lines[0] != want {
		t.Fatalf("Judge = %v, %q; want a failure whose first line is %q", got.Pass, got.Lines, want)
	}
}

func TestPlace(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string
		want   string
	}{
		{name: "the first location, the row counted from one", answer: `[{"ident":"x","desc":"d","locations":[{"symbolic":{"key":{"Local":{"verbatim_path":"a.yml"}}},"concrete":{"location":{"start_point":{"row":0}}}},{"symbolic":{"key":{"Local":{"verbatim_path":"b.yml"}}},"concrete":{"location":{"start_point":{"row":9}}}}]}]`, want: `"a.yml":1`},
		{name: "no locations key", answer: `[{"ident":"x","desc":"d"}]`, want: "?"},
		{name: "an empty list of locations", answer: `[{"ident":"x","locations":[]}]`, want: "?"},
		{name: "locations that are not a list", answer: `[{"ident":"x","locations":{}}]`, want: "?"},
		{name: "a location that is not an object", answer: `[{"ident":"x","locations":[1]}]`, want: "?"},
		{name: "a row that is not a number", answer: `[{"ident":"x","locations":[{"concrete":{"location":{"start_point":{"row":"1"}}}}]}]`, want: "?"},
		{name: "a path that is not a string", answer: `[{"ident":"x","locations":[{"symbolic":{"key":{"Local":{"verbatim_path":3}}}}]}]`, want: "?"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := decode([]byte(tc.answer))
			if err != nil {
				t.Fatal(err)
			}
			if got := findings[0].place(); got != tc.want {
				t.Errorf("place() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDescriptionIsEmptyWhenItIsNotAString(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string
		want   string
	}{
		{name: "a string", answer: `[{"ident":"x","desc":"why"}]`, want: "why"},
		{name: "absent", answer: `[{"ident":"x"}]`, want: ""},
		{name: "a number", answer: `[{"ident":"x","desc":4}]`, want: ""},
		{name: "null", answer: `[{"ident":"x","desc":null}]`, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := decode([]byte(tc.answer))
			if err != nil {
				t.Fatal(err)
			}
			if got := findings[0].description(); got != tc.want {
				t.Errorf("description() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Every answer below is one the verdict must refuse, with a message that says what is wrong, whatever the
// number expected: a reader that treated it as "no findings" would pass the gate on nothing.
func TestJudgeRefusesWhatItDoesNotUnderstand(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer []byte
		marker string
	}{
		{name: "empty", answer: nil, marker: "not a list"},
		{name: "white space only", answer: []byte(" \n"), marker: "not a list"},
		{name: "an object", answer: []byte(`{}`), marker: "not a list"},
		{name: "an object that holds the findings", answer: []byte(`{"findings":[]}`), marker: "not a list"},
		{name: "null", answer: []byte(`null`), marker: "not a list"},
		{name: "a string", answer: []byte(`"[]"`), marker: "not a list"},
		{name: "a number", answer: []byte(`0`), marker: "not a list"},
		{name: "a boolean", answer: []byte(`true`), marker: "not a list"},
		{name: "plain text", answer: []byte(`no findings`), marker: "not a list"},
		{name: "a byte order mark before the list", answer: []byte("\xef\xbb\xbf[]"), marker: "not a list"},
		{name: "an unterminated list", answer: []byte(`[`), marker: "not a list of findings:"},
		{name: "a list cut in the middle of a finding", answer: []byte(`[{"ident":"self-rep`), marker: "not a list of findings:"},
		{name: "a trailing bracket", answer: []byte(`[]]`), marker: "text after"},
		{name: "two lists", answer: []byte(`[] []`), marker: "text after"},
		{name: "a list and text", answer: []byte(`[]x`), marker: "text after"},
		{name: "a list and a NUL", answer: []byte("[]\x00"), marker: "text after"},
		{name: "a list and a log line", answer: []byte("[]\n INFO audit: done\n"), marker: "text after"},
		{name: "invalid UTF-8", answer: []byte("[{\"ident\":\"self-repository\",\"desc\":\"\xff\"}]"), marker: "UTF-8"},
		{name: "a list of numbers", answer: []byte(`[1]`), marker: "not a list of findings:"},
		{name: "a list of lists", answer: []byte(`[[]]`), marker: "not a list of findings:"},
		{name: "a list of strings", answer: []byte(`["self-repository"]`), marker: "not a list of findings:"},
		{name: "a null element", answer: []byte(`[null]`), marker: "finding 1:"},
		{name: "an element without a rule", answer: []byte(`[{}]`), marker: "finding 1:"},
		{name: "a rule that is null", answer: []byte(`[{"ident":null}]`), marker: "finding 1:"},
		{name: "a rule that is empty", answer: []byte(`[{"ident":""}]`), marker: "finding 1:"},
		{name: "a rule that is a number", answer: []byte(`[{"ident":1}]`), marker: "finding 1:"},
		{name: "a rule that is a list", answer: []byte(`[{"ident":["self-repository"]}]`), marker: "finding 1:"},
		{name: "a rule that is an object", answer: []byte(`[{"ident":{"self-repository":1}}]`), marker: "finding 1:"},
		{name: "the key in another case", answer: []byte(`[{"IDENT":"self-repository"}]`), marker: "finding 1:"},
		{name: "the key with a prefix", answer: []byte(`[{"x-ident":"self-repository"}]`), marker: "finding 1:"},
		{name: "the second finding has no rule", answer: []byte(`[{"ident":"self-repository"},{"desc":"x"}]`), marker: "finding 2:"},
		{name: "the third finding has no rule", answer: []byte(`[{"ident":"self-repository"},{"ident":"self-repository"},{}]`), marker: "finding 3:"},
	} {
		for _, expected := range []int{0, 1} {
			t.Run(tc.name+" expecting "+strconv.Itoa(expected), func(t *testing.T) {
				got := Judge(expected, tc.answer)
				if got.Pass {
					t.Fatalf("passed on %q", tc.answer)
				}
				if len(got.Lines) != 1 || !strings.Contains(got.Lines[0], tc.marker) || !strings.HasPrefix(got.Lines[0], "zizmor: ") {
					t.Errorf("lines = %q, want one starting with %q that holds %q", got.Lines, "zizmor: ", tc.marker)
				}
			})
		}
	}
}

// A real answer of the pinned zizmor (--no-ignores --format json-v1, taken from this repository): the format
// the reader is written for. Its thirteen findings are the allowed ones of the Makefile.
func TestJudgeReadsARealAnswerOfZizmor(t *testing.T) {
	answer, err := os.ReadFile("testdata/zizmor-1.32.0-self-repository.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		expected int
		pass     bool
	}{
		{name: "the allowed number", expected: 13, pass: true},
		{name: "one fewer allowed", expected: 12, pass: false},
		{name: "one more allowed", expected: 14, pass: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Judge(tc.expected, answer); got.Pass != tc.pass {
				t.Errorf("Judge(%d) = %v, %q; want pass %v", tc.expected, got.Pass, got.Lines, tc.pass)
			}
		})
	}
	// The same answer with one finding renamed is a finding of another rule, and its place is read.
	renamed := strings.Replace(string(answer), `"ident": "self-repository"`, `"ident": "template-injection"`, 1)
	if renamed == string(answer) {
		t.Fatal("the fixture does not hold the rule name in the form the test replaces")
	}
	got := Judge(13, []byte(renamed))
	if got.Pass || len(got.Lines) != 2 || !strings.Contains(got.Lines[0], `"./.github/workflows/`) || !strings.Contains(got.Lines[0], `("template-injection")`) {
		t.Errorf("Judge = %v, %q; want a failure that names the place and the rule of the renamed finding", got.Pass, got.Lines)
	}
}

// What the verdict prints is made of strings the audited tree controls (a description, a path): none of
// them may start a workflow command of the job log, add a line, or carry a control character.
func TestJudgeOutputIsSafeForALog(t *testing.T) {
	hostile := "line\n::error::injected\r::add-mask::x\x1b[31m\u202e\u2028\x00\u00e9"
	got := Judge(0, list(findingJSON(hostile, hostile, "::stop-commands::\n"+hostile, 2)))
	if got.Pass {
		t.Fatal("passed")
	}
	if len(got.Lines) != 2 {
		t.Fatalf("lines = %q, want a finding line and the counts", got.Lines)
	}
	for _, line := range got.Lines {
		if !printableLines(line) || strings.HasPrefix(line, "::") {
			t.Errorf("an unsafe line: %q", line)
		}
	}
	if !strings.Contains(got.Lines[0], `\n::error::injected\r`) {
		t.Errorf("the hostile text is not shown escaped: %q", got.Lines[0])
	}
}

func TestJudgeCutsLongStrings(t *testing.T) {
	long := strings.Repeat("x", 5000)
	for _, tc := range []struct {
		name string
		text string
		want int
	}{
		{name: "the limit is not cut", text: strings.Repeat("y", maxField), want: maxField + 2},
		{name: "one over the limit is cut to the limit", text: strings.Repeat("y", maxField+1), want: maxField + 2},
		{name: "a very long string", text: long, want: maxField + 2},
		{name: "wide characters are counted as characters", text: strings.Repeat("é", maxField+1), want: maxField*6 - 3*6 + 3 + 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := quote(tc.text)
			if len(got) != tc.want {
				t.Errorf("len(quote) = %d, want %d: %.40s", len(got), tc.want, got)
			}
		})
	}
	if got := quote(strings.Repeat("y", maxField+1)); !strings.HasSuffix(got, ellipsis+`"`) {
		t.Errorf("a cut string does not end in the ellipsis: ...%s", got[len(got)-10:])
	}
	if got := quote(strings.Repeat("y", maxField)); strings.Contains(got, ellipsis) {
		t.Errorf("a string of the limit was cut: %s", got)
	}
}

func TestJudgeListsAtMostTwentyFindings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int
		lines int
		more  string
	}{
		{name: "none over the limit", count: maxListed, lines: maxListed + 1},
		{name: "one over the limit", count: maxListed + 1, lines: maxListed + 2, more: "zizmor: and 1 more findings of other rules"},
		{name: "many over the limit", count: 500, lines: maxListed + 2, more: "zizmor: and 480 more findings of other rules"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Judge(0, list(repeat(tc.count, findingJSON(other, "d", site, 1))...))
			if got.Pass || len(got.Lines) != tc.lines {
				t.Fatalf("pass %v, %d lines, want a failure with %d", got.Pass, len(got.Lines), tc.lines)
			}
			if tc.more != "" && got.Lines[maxListed] != tc.more {
				t.Errorf("line %d = %q, want %q", maxListed+1, got.Lines[maxListed], tc.more)
			}
			counts := got.Lines[len(got.Lines)-1]
			if want := strconv.Itoa(tc.count) + " of other rules"; !strings.Contains(counts, want) {
				t.Errorf("counts = %q, want %q (every finding counts, listed or not)", counts, want)
			}
		})
	}
}

// An object that has the same name twice is refused wherever it is, whichever value comes last: the decoder of
// the standard library would keep the last one, and a finding whose rule name is read as whichever comes last
// can be made to say what its writer wants.
func TestJudgeRefusesARepeatedName(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string
	}{
		{name: "the rule name twice, the allowed rule last", answer: `[{"ident":"template-injection","ident":"self-repository"}]`},
		{name: "the rule name twice, the allowed rule first", answer: `[{"ident":"self-repository","ident":"template-injection"}]`},
		{name: "the rule name twice with the same value", answer: `[{"ident":"self-repository","ident":"self-repository"}]`},
		{name: "the second name written with an escape", answer: "[{\"ident\":\"template-injection\",\"\\u0069dent\":\"self-repository\"}]"},
		{name: "the first name written with an escape", answer: "[{\"\\u0069dent\":\"self-repository\",\"ident\":\"template-injection\"}]"},
		{name: "both names written with different escapes", answer: "[{\"id\\u0065nt\":\"self-repository\",\"\\u0069dent\":\"template-injection\"}]"},
		{name: "another name twice", answer: `[{"ident":"self-repository","desc":"a","desc":"b"}]`},
		{name: "a name with capitals twice", answer: `[{"ident":"self-repository","Desc":1,"Desc":2}]`},
		{name: "an empty name twice", answer: `[{"ident":"self-repository","":1,"":2}]`},
		{name: "in a nested object", answer: `[{"ident":"self-repository","locations":[{"concrete":{"a":1,"a":2}}]}]`},
		{name: "in the object of a list", answer: `[{"ident":"self-repository","l":[{"a":1,"a":2}]}]`},
		{name: "after a nested object", answer: `[{"ident":"self-repository","x":{"y":1},"x":2}]`},
		{name: "after an empty object", answer: `[{"ident":"self-repository","x":{},"x":2}]`},
		{name: "after a list", answer: `[{"ident":"self-repository","x":[1,2],"x":3}]`},
		{name: "after an empty list", answer: `[{"ident":"self-repository","x":[],"x":3}]`},
		{name: "after a list of objects", answer: `[{"ident":"self-repository","x":[{"a":1}],"x":3}]`},
		{name: "after two nested levels", answer: `[{"ident":"self-repository","x":{"y":{"z":1}},"x":3}]`},
		{name: "in the second finding", answer: `[{"ident":"self-repository"},{"ident":"self-repository","a":1,"a":1}]`},
		{name: "the repeat is the last name", answer: `[{"ident":"self-repository","a":1,"b":2,"a":3}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// subtests: the two expected counts are the same case, read both ways
			for _, expected := range []int{1, 2} {
				got := Judge(expected, []byte(tc.answer))
				if got.Pass || len(got.Lines) != 1 || !strings.Contains(got.Lines[0], "repeats the name") {
					t.Errorf("expecting %d: pass %v, lines %q; want a refusal that says a name is repeated", expected, got.Pass, got.Lines)
				}
			}
		})
	}
}

// The same name in different objects, or at different depths, is not a repeat.
func TestJudgeAcceptsTheSameNameInDifferentObjects(t *testing.T) {
	for _, tc := range []struct {
		name     string
		answer   string
		expected int
	}{
		{name: "two findings with the same names", answer: `[{"ident":"self-repository","a":1},{"ident":"self-repository","a":2}]`, expected: 2},
		{name: "a name inside a nested object that the finding has too", answer: `[{"ident":"self-repository","a":{"a":1,"ident":"x"}}]`, expected: 1},
		{name: "empty nested objects side by side", answer: `[{"ident":"self-repository","a":{},"b":{},"c":[],"d":[]}]`, expected: 1},
		{name: "a list of objects with the same names", answer: `[{"ident":"self-repository","l":[{"a":1},{"a":2}]}]`, expected: 1},
		{name: "names that differ in case", answer: `[{"ident":"self-repository","Desc":1,"desc":2}]`, expected: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Judge(tc.expected, []byte(tc.answer)); !got.Pass {
				t.Errorf("Judge = %q, want a pass", got.Lines)
			}
		})
	}
}

// Escape is the one escaper of untrusted text of the tool: one line of printable ASCII, cut at 200 characters.
func TestEscape(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{name: "plain text is kept", text: "completed ./.github/workflows/a.yml", want: "completed ./.github/workflows/a.yml"},
		{name: "empty", text: "", want: ""},
		{name: "a new line and a workflow command", text: "a\n::warning::x", want: `a\n::warning::x`},
		{name: "a carriage return, a tab and a NUL", text: "a\r\tb\x00", want: `a\r\tb\x00`},
		{name: "an escape sequence", text: "\x1b[31mred", want: `\x1b[31mred`},
		{name: "a quote and a backslash", text: `say "a\b"`, want: `say \"a\\b\"`},
		{name: "characters outside ASCII", text: "caf\u00e9 \U0001f308", want: "caf\\u00e9 \\U0001f308"},
		{name: "a line separator and a right-to-left override", text: "a\u2028b\u202e", want: "a\\u2028b\\u202e"},
		{name: "invalid UTF-8", text: "a\xffb", want: `a\xffb`},
		{name: "the limit is kept whole", text: strings.Repeat("x", maxField), want: strings.Repeat("x", maxField)},
		{name: "one over the limit is cut with an ellipsis", text: strings.Repeat("x", maxField+1), want: strings.Repeat("x", maxField-len(ellipsis)) + ellipsis},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Escape(tc.text); got != tc.want {
				t.Errorf("Escape(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// The token reader that looks for repeated names reads numbers differently from the typed decode that came
// before it: a number the typed decode takes as text (one a float64 cannot hold) is an error for the tokens.
// An error there is not the end of the answer: the answer is refused, so that a name repeated after such a
// number is not left unseen.
func TestJudgeRefusesWhatTheTokenReaderCannotRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string
	}{
		{name: "a repeated name after a number out of range", answer: `[{"ident":"template-injection","n":1e999,"ident":"self-repository"}]`},
		{name: "a repeated name after a negative number out of range", answer: `[{"ident":"template-injection","n":-1e999,"ident":"self-repository"}]`},
		{name: "a repeated name in a list after a number out of range", answer: `[{"ident":"self-repository"},{"l":[1e999],"ident":"a","ident":"self-repository"}]`},
		{name: "a number out of range and no repeated name", answer: `[{"ident":"self-repository","n":1e999}]`},
		{name: "a number out of range as the first value", answer: `[{"n":1e999,"ident":"self-repository"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Judge(1, []byte(tc.answer))
			if got.Pass || len(got.Lines) != 1 || !strings.Contains(got.Lines[0], "not a list of findings:") {
				t.Errorf("pass %v, lines %q; want a refusal that says the answer could not be read", got.Pass, got.Lines)
			}
		})
	}
}

// The scan fails closed by itself too: an answer it cannot read to its end, or that ends inside an object or a
// list, is an error and not "nothing repeated".
func TestRejectRepeatedKeysFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string
		want   string // a word of the error, "" for none
	}{
		{name: "a complete answer", answer: `[{"a":1},{"a":2}]`},
		{name: "a number the tokens cannot read", answer: `[{"a":1e999}]`, want: "not a list of findings"},
		{name: "a list that is not closed", answer: `[{"a":1}`, want: "ends inside"},
		{name: "an object that is not closed", answer: `[{"a":`, want: "ends inside"},
		{name: "nothing", answer: ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := rejectRepeatedKeys([]byte(tc.answer))
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("error %v, want none", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("error %v, want one with %q", err, tc.want)
			}
		})
	}
}
