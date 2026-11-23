// Package verdict judges the answer of zizmor, the audit of the workflow files, for `make zizmor`: the run
// passes only when the answer holds exactly the expected number of findings of one rule and no other
// finding. It is the library part of the command tools/zizmorverdict, a build tool of this repository.
//
// The answer is untrusted text: zizmor copies strings of the audited tree into it (a description, a path,
// the text of a step), and a pull request can write them. It is read as JSON by the standard library, up to
// 16 MiB, and the program fails closed on anything it did not expect:
//
//   - the answer is valid UTF-8 and a single JSON array (nothing but white space before and after it);
//     null, an object, a scalar, an empty answer and trailing text are failures;
//   - every element is an object with a non-empty string under the exact key "ident" (the match is not
//     case-insensitive, unlike the decoding of a struct), the name of the rule;
//   - the findings of the rule [AllowedRule] are counted, and the run passes only when there are exactly
//     as many as expected and no finding of another rule. The rule is counted, not located.
//
// The expected number is a plain non-negative integer: digits only, no sign, no space, no leading zero
// (so that 010 is not read as octal by one reader and as ten by another), at most six digits.
//
// What it prints is built from the answer, so it is made safe for a job log: every string taken from the
// answer is cut to 200 characters and printed as a quoted, ASCII-only literal, so that a control
// character, an escape sequence or a line that starts with "::" (a workflow command of the log) cannot
// reach it, and at most 20 findings are listed. The answer is read in this process, from the standard
// input; the command line is the only other input, and nothing in the audited tree is read.
package verdict

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// Format is the output format of zizmor that the answer must be in.
	Format = "json-v1"
	// AllowedRule is the rule whose findings are expected, in the number given on the command line.
	AllowedRule = "self-repository"

	// MaxAnswer is the most bytes of an answer that are read: Run refuses an answer of more bytes. The answer for
	// this repository is about 44 KB.
	MaxAnswer = 16 << 20
	// maxExpectedDigits is the most digits an expected count may have.
	maxExpectedDigits = 6
	// maxListed is the most findings of other rules a verdict lists.
	maxListed = 20
	// maxField is the most characters of a string of the answer a verdict prints.
	maxField = 200
	// unknownPlace is what stands for a place that the finding does not give in the form it is read.
	unknownPlace = "?"
	// ellipsis ends a string that was cut.
	ellipsis = "..."

	// keyIdent is the key of the rule name in a finding.
	keyIdent = "ident"
	// keyDesc is the key of the description in a finding.
	keyDesc = "desc"
	// keyLocations is the key of the locations in a finding.
	keyLocations = "locations"
)

// Verdict is the outcome of the judgment of one answer.
type Verdict struct {
	// Pass is true when the answer holds exactly the expected findings and nothing else.
	Pass bool
	// Lines are the lines to print, without a line terminator: to the standard output when Pass, to
	// the standard error when not.
	Lines []string
}

// ParseExpected reads the expected number of findings: digits only, no sign, no space, no leading zero
// unless the number is 0, at most six digits.
func ParseExpected(text string) (int, error) {
	switch {
	case text == "":
		return 0, errors.New("the expected count is empty")
	case len(text) > maxExpectedDigits:
		return 0, fmt.Errorf("the expected count %s is longer than %d digits", quote(text), maxExpectedDigits)
	case len(text) > 1 && text[0] == '0':
		return 0, fmt.Errorf("the expected count %s has a leading zero", quote(text))
	}
	for i := range len(text) {
		if text[i] < '0' || text[i] > '9' {
			return 0, fmt.Errorf("the expected count %s is not a plain non-negative integer", quote(text))
		}
	}
	return strconv.Atoi(text)
}

// finding is one element of the answer, as far as the verdict reads it.
type finding struct {
	rule string
	raw  map[string]json.RawMessage
}

// decode reads the answer as a list of findings.
func decode(answer []byte) ([]finding, error) {
	if !utf8.Valid(answer) {
		return nil, errors.New("the answer is not valid UTF-8")
	}
	if first := bytes.TrimLeft(answer, " \t\r\n"); len(first) == 0 || first[0] != '[' {
		return nil, errors.New("the answer is not a list of findings (a JSON array)")
	}
	decoder := json.NewDecoder(bytes.NewReader(answer))
	var elements []map[string]json.RawMessage
	if err := decoder.Decode(&elements); err != nil {
		return nil, fmt.Errorf("the answer is not a list of findings: %s", quote(err.Error()))
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("the answer has text after the list of findings")
	}
	if err := rejectRepeatedKeys(answer); err != nil {
		return nil, err
	}
	findings := make([]finding, len(elements))
	for i, raw := range elements {
		rule, err := ruleOf(raw)
		if err != nil {
			return nil, fmt.Errorf("finding %d: %w", i+1, err)
		}
		findings[i] = finding{rule: rule, raw: raw}
	}
	return findings, nil
}

// frame is an object or an array the token reader of rejectRepeatedKeys is inside of.
type frame struct {
	// keys are the names seen in an object (nil for an array); wantKey is true while the next token is a name.
	keys    map[string]struct{}
	wantKey bool
}

// keyChecker follows the tokens of the answer and the objects they are in.
type keyChecker struct{ stack []*frame }

// top is the object or array the next token is in, nil at the top level.
func (k *keyChecker) top() *frame {
	if len(k.stack) == 0 {
		return nil
	}
	return k.stack[len(k.stack)-1]
}

// done records that a value ended: in an object the next token is a name again.
func (k *keyChecker) done() {
	if top := k.top(); top != nil && top.keys != nil {
		top.wantKey = true
	}
}

// add takes the next token: a name is recorded (and refused when the object has it already), anything else
// opens, closes or is a value.
func (k *keyChecker) add(token json.Token) error {
	if top := k.top(); top != nil && top.keys != nil && top.wantKey {
		return k.name(top, token)
	}
	delim, isDelim := token.(json.Delim)
	switch {
	case isDelim && delim == '{':
		k.stack = append(k.stack, &frame{keys: map[string]struct{}{}, wantKey: true})
	case isDelim && delim == '[':
		k.stack = append(k.stack, &frame{})
	case isDelim:
		k.stack = k.stack[:len(k.stack)-1]
		k.done()
	default:
		k.done()
	}
	return nil
}

// name takes a token where an object has a name: the name, or the delimiter that closes the object.
func (k *keyChecker) name(top *frame, token json.Token) error {
	name, isName := token.(string)
	if !isName { // the end of the object: the decoder gives nothing else where it expects a name
		k.stack = k.stack[:len(k.stack)-1]
		k.done()
		return nil
	}
	if _, seen := top.keys[name]; seen {
		return fmt.Errorf("the answer repeats the name %s in an object", quote(name))
	}
	top.keys[name] = struct{}{}
	top.wantKey = false
	return nil
}

// rejectRepeatedKeys fails when an object of the answer, at any depth, has the same name twice (the decoder
// of the standard library keeps the last one, so a repeated "ident" would be read as whichever comes
// last). Names are compared after their escapes are decoded, so "ident" and "\u0069dent" are the same name.
//
// The tokens are read differently from the typed decode that came before (a number is a float64 here, so one that
// does not fit is an error here and not there), so an error is not the end of the answer: it is returned, and the
// scan ends without an error only at the end of the text with no object or list left open.
func rejectRepeatedKeys(answer []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(answer))
	var k keyChecker
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("the answer is not a list of findings: %s", quote(err.Error()))
		}
		if repeated := k.add(token); repeated != nil {
			return repeated
		}
	}
	if len(k.stack) != 0 {
		return errors.New("the answer ends inside an object or a list")
	}
	return nil
}

// ruleOf returns the rule name of a finding: a non-empty string under the key "ident".
func ruleOf(raw map[string]json.RawMessage) (string, error) {
	var rule string
	if err := json.Unmarshal(raw[keyIdent], &rule); err != nil || rule == "" {
		return "", errors.New(`has no rule name (a non-empty string under "ident")`)
	}
	return rule, nil
}

// place is where a finding is, "path:line" of its first location, or unknownPlace when the finding does
// not give it in that form. It only serves the message, never the verdict.
func (f finding) place() string {
	var locations []struct {
		Symbolic struct {
			Key struct {
				Local struct {
					Path string `json:"verbatim_path"`
				}
			}
		}
		Concrete struct {
			Location struct {
				StartPoint struct {
					Row int `json:"row"`
				} `json:"start_point"`
			}
		}
	}
	if err := json.Unmarshal(f.raw[keyLocations], &locations); err != nil || len(locations) == 0 {
		return unknownPlace
	}
	first := locations[0]
	return quote(first.Symbolic.Key.Local.Path) + ":" + strconv.Itoa(first.Concrete.Location.StartPoint.Row+1)
}

// description is the text zizmor gives the finding, or "" when it gives none as a string.
func (f finding) description() string {
	var text string
	if err := json.Unmarshal(f.raw[keyDesc], &text); err != nil {
		return ""
	}
	return text
}

// Judge reads the answer and returns the verdict for the expected number of findings of AllowedRule.
func Judge(expected int, answer []byte) Verdict {
	findings, err := decode(answer)
	if err != nil {
		return Verdict{Lines: []string{"zizmor: " + err.Error()}}
	}
	allowed := 0
	var others []finding
	for _, f := range findings {
		if f.rule == AllowedRule {
			allowed++
		} else {
			others = append(others, f)
		}
	}
	if len(others) == 0 && allowed == expected {
		return Verdict{Pass: true, Lines: []string{fmt.Sprintf("zizmor: no findings but the %d allowed ones", expected)}}
	}
	var lines []string
	for i, f := range others {
		if i == maxListed {
			lines = append(lines, fmt.Sprintf("zizmor: and %d more findings of other rules", len(others)-maxListed))
			break
		}
		lines = append(lines, fmt.Sprintf("zizmor: %s: %s (%s)", f.place(), quote(f.description()), quote(f.rule)))
	}
	return Verdict{Lines: append(lines, fmt.Sprintf("zizmor findings: %d of the allowed rule (%s), %d of other rules; "+
		"allowed exactly %d of the first and none of the second (ZIZMOR_SELF_REPOSITORY_FINDINGS in the Makefile)",
		allowed, AllowedRule, len(others), expected))}
}

// quote is the text cut to maxField characters and printed as a quoted literal of printable ASCII.
func quote(text string) string {
	if runes := []rune(text); len(runes) > maxField {
		text = string(runes[:maxField-len(ellipsis)]) + ellipsis
	}
	return strconv.QuoteToASCII(text)
}

// Escape is untrusted text made safe for a job log: cut to 200 characters (the cut ends in "..."), and written
// as printable ASCII, with the quote marks of a Go literal left off: a control character, a new line, an
// escape sequence and any character outside ASCII become an escape such as \n or \u00e9, so that the result
// is one line of printable ASCII whatever the text holds. It is what every string taken from the answer of
// zizmor goes through, and what package audit applies to each line zizmor writes on its standard error.
func Escape(text string) string {
	quoted := quote(text)
	return quoted[1 : len(quoted)-1]
}

// Exit codes of Run.
const (
	exitPass  = 0
	exitFail  = 1
	exitUsage = 2
)

// usage is what Run prints for a command line it does not understand.
const usage = "usage: zizmorverdict EXPECTED < answer.json (zizmor --format " + Format + "; EXPECTED is the number of " +
	AllowedRule + " findings allowed)"

// Run is the whole command: args are the command line without the program name, which holds the expected
// number of findings of AllowedRule, and in is the answer of zizmor, read up to 16 MiB. It prints the verdict
// to out when it passes and to errOut when not, and returns the exit status: 0 when the answer holds exactly
// the expected findings and no other, 1 when it does not, when the expected number is not a plain
// non-negative integer, when the answer cannot be read or is too large, or when the verdict cannot be
// written, 2 for a command line that is not one argument. Nothing else is read: no file, no environment
// variable.
func Run(args []string, in io.Reader, out, errOut io.Writer) int {
	if len(args) != 1 {
		_, _ = io.WriteString(errOut, usage+"\n")
		return exitUsage
	}
	expected, err := ParseExpected(args[0])
	if err != nil {
		_, _ = io.WriteString(errOut, "zizmor: "+err.Error()+"\n")
		return exitFail
	}
	answer, err := io.ReadAll(io.LimitReader(in, MaxAnswer+1))
	if err != nil {
		_, _ = io.WriteString(errOut, "zizmor: the answer could not be read: "+quote(err.Error())+"\n")
		return exitFail
	}
	if len(answer) > MaxAnswer {
		_, _ = fmt.Fprintf(errOut, "zizmor: the answer is larger than %d bytes\n", MaxAnswer)
		return exitFail
	}
	verdict := Judge(expected, answer)
	dest := errOut
	if verdict.Pass {
		dest = out
	}
	if _, err := io.WriteString(dest, strings.Join(verdict.Lines, "\n")+"\n"); err != nil || !verdict.Pass {
		return exitFail
	}
	return exitPass
}
