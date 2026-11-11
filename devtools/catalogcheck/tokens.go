package main

import (
	"fmt"
	"regexp"
	"strings"
)

// The kinds of a token of an Automation field, as the catalog's "Entry format" paragraph lists them.
const (
	kindTest     = "test"
	kindFuzz     = "fuzz"
	kindMake     = "make"
	kindLint     = "lint"
	kindAnalyzer = "analyzer"
	kindScript   = "script"
	kindWorkflow = "workflow"
	kindReading  = "reading"
	kindProposed = "proposed"
)

// minReasonWords is the least a `reading:` reason has: "needs a person" alone is not a reason.
const minReasonWords = 3

var (
	codeSpan    = regexp.MustCompile("`([^`]*)`")
	tokenSpan   = regexp.MustCompile(`^([a-z]+):(.*)$`)
	proposedFmt = regexp.MustCompile(`^[A-Za-z0-9_.-]+\(#[0-9]+\)$`)
	testName    = regexp.MustCompile(`^Test[A-Za-z0-9_]*$`)
	fuzzName    = regexp.MustCompile(`^Fuzz[A-Za-z0-9_]*$`)
)

// kinds is the set of kinds a token may have.
var kinds = map[string]bool{
	kindTest: true, kindFuzz: true, kindMake: true, kindLint: true, kindAnalyzer: true,
	kindScript: true, kindWorkflow: true, kindReading: true, kindProposed: true,
}

// automationToken is one `kind:name` code span of an Automation field. For `reading:` the name is empty and the
// reason is the text after the span, up to the next automationToken.
type automationToken struct {
	kind   string
	name   string
	reason string
}

// parseTokens returns the tokens of an Automation field and the problems of their form.
func parseTokens(text string) ([]automationToken, []string) {
	var tokens []automationToken
	var bad []string
	var spans [][2]int // the start and the end of each token's code span in the text
	for _, s := range codeSpan.FindAllStringSubmatchIndex(text, -1) {
		m := tokenSpan.FindStringSubmatch(text[s[2]:s[3]])
		if m == nil {
			continue
		}
		if !kinds[m[1]] {
			bad = append(bad, fmt.Sprintf("unknown kind %q in `%s`", m[1], m[0]))
			continue
		}
		tokens = append(tokens, automationToken{kind: m[1], name: m[2]})
		spans = append(spans, [2]int{s[0], s[1]})
	}
	// The reason of a `reading:` runs to the start of the next token; a code span that is not a token
	// (such as `sent`) is part of the reason.
	for i := range tokens {
		end := len(text)
		if i+1 < len(tokens) {
			end = spans[i+1][0]
		}
		tokens[i].reason = text[spans[i][1]:end]
	}
	return tokens, bad
}

// formProblems checks a token without looking at the repository: the shape of its name, and the
// reason of a `reading:`.
func (t automationToken) formProblems() []string {
	switch t.kind {
	case kindReading:
		if t.name != "" {
			return []string{fmt.Sprintf("`reading:%s`: the reason follows the token, outside the code span", t.name)}
		}
		if len(strings.Fields(strings.Trim(t.reason, " ;.,:()"))) < minReasonWords {
			return []string{"`reading:` carries no reason (say why this cannot be scripted, in at least three words)"}
		}
	case kindProposed:
		if !proposedFmt.MatchString(t.name) {
			return []string{fmt.Sprintf("`proposed:%s` must be `proposed:<name>(#<issue>)`", t.name)}
		}
	case kindTest:
		if !testName.MatchString(t.name) {
			return []string{fmt.Sprintf("`test:%s` is not a Test function name", t.name)}
		}
	case kindFuzz:
		if !fuzzName.MatchString(t.name) {
			return []string{fmt.Sprintf("`fuzz:%s` is not a Fuzz function name", t.name)}
		}
	default:
		if t.name == "" || strings.ContainsAny(t.name, " \t") {
			return []string{fmt.Sprintf("`%s:%s` needs a name without spaces", t.kind, t.name)}
		}
	}
	return nil
}
