package verdict

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// strictParse is a reader of RFC 8259 JSON written for the tests and sharing no code with the package or
// with encoding/json, so that the oracle of FuzzJudge does not have the faults of the library it checks: it
// accepts exactly the texts of the grammar (one value, white space only before and after it, no control
// character in a string, no repeated name in an object, no leading zero or lone sign in a number) and
// nothing else. A value is a map (object), a slice (array), a string, or nil for a number or a literal,
// which the oracle never looks inside. Text that is not valid UTF-8 is refused.
func strictParse(data []byte) (value any, ok bool) {
	if !utf8.Valid(data) {
		return nil, false
	}
	p := &strictParser{text: data}
	p.space()
	value, ok = p.value()
	if !ok {
		return nil, false
	}
	p.space()
	return value, p.at == len(p.text)
}

type strictParser struct {
	text []byte
	at   int
}

func (p *strictParser) space() {
	for p.at < len(p.text) && strings.IndexByte(" \t\n\r", p.text[p.at]) >= 0 {
		p.at++
	}
}

func (p *strictParser) peek() byte {
	if p.at < len(p.text) {
		return p.text[p.at]
	}
	return 0
}

// literal accepts the word at the current place.
func (p *strictParser) literal(word string) bool {
	if strings.HasPrefix(string(p.text[p.at:]), word) {
		p.at += len(word)
		return true
	}
	return false
}

func (p *strictParser) value() (any, bool) {
	switch p.peek() {
	case '{':
		return p.object()
	case '[':
		return p.array()
	case '"':
		s, ok := p.str()
		return s, ok
	case 't':
		return nil, p.literal("true")
	case 'f':
		return nil, p.literal("false")
	case 'n':
		return nil, p.literal("null")
	default:
		return nil, p.number()
	}
}

func (p *strictParser) object() (any, bool) {
	p.at++ // {
	members := map[string]any{}
	p.space()
	if p.peek() == '}' {
		p.at++
		return members, true
	}
	for {
		p.space()
		if p.peek() != '"' {
			return nil, false
		}
		name, ok := p.str()
		if !ok {
			return nil, false
		}
		if _, repeated := members[name]; repeated {
			return nil, false
		}
		p.space()
		if p.peek() != ':' {
			return nil, false
		}
		p.at++
		p.space()
		member, ok := p.value()
		if !ok {
			return nil, false
		}
		members[name] = member
		p.space()
		switch p.peek() {
		case ',':
			p.at++
		case '}':
			p.at++
			return members, true
		default:
			return nil, false
		}
	}
}

func (p *strictParser) array() (any, bool) {
	p.at++ // [
	elements := []any{}
	p.space()
	if p.peek() == ']' {
		p.at++
		return elements, true
	}
	for {
		p.space()
		element, ok := p.value()
		if !ok {
			return nil, false
		}
		elements = append(elements, element)
		p.space()
		switch p.peek() {
		case ',':
			p.at++
		case ']':
			p.at++
			return elements, true
		default:
			return nil, false
		}
	}
}

// hex4 reads four hexadecimal digits.
func (p *strictParser) hex4() (rune, bool) {
	if p.at+4 > len(p.text) {
		return 0, false
	}
	var r rune
	for _, c := range p.text[p.at : p.at+4] {
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	p.at += 4
	return r, true
}

// str reads a string and returns it with its escapes decoded (a surrogate pair is one character, a lone
// surrogate is U+FFFD).
func (p *strictParser) str() (string, bool) {
	p.at++ // "
	var out strings.Builder
	for p.at < len(p.text) {
		c := p.text[p.at]
		switch {
		case c == '"':
			p.at++
			return out.String(), true
		case c < 0x20:
			return "", false
		case c != '\\':
			out.WriteByte(c)
			p.at++
		default:
			p.at++
			r, ok := p.escape()
			if !ok {
				return "", false
			}
			out.WriteRune(r)
		}
	}
	return "", false
}

// escape reads what follows a backslash.
func (p *strictParser) escape() (rune, bool) {
	c := p.peek()
	p.at++
	if r, simple := map[byte]rune{'"': '"', '\\': '\\', '/': '/', 'b': '\b', 'f': '\f', 'n': '\n', 'r': '\r', 't': '\t'}[c]; simple {
		return r, true
	}
	if c != 'u' {
		return 0, false
	}
	r, ok := p.hex4()
	if !ok {
		return 0, false
	}
	switch {
	case r >= 0xD800 && r < 0xDC00:
		// a high surrogate: a low one right after it makes one character, otherwise it is a lone surrogate
		save := p.at
		if p.peek() == '\\' && p.at+1 < len(p.text) && p.text[p.at+1] == 'u' {
			p.at += 2
			if low, ok := p.hex4(); ok && low >= 0xDC00 && low < 0xE000 {
				return 0x10000 + (r-0xD800)<<10 + (low - 0xDC00), true
			}
		}
		p.at = save
		return utf8.RuneError, true
	case r >= 0xDC00 && r < 0xE000:
		return utf8.RuneError, true
	}
	return r, true
}

// number accepts the grammar of a number: -? (0 | [1-9][0-9]*) (. [0-9]+)? ([eE] [+-]? [0-9]+)?
func (p *strictParser) number() bool {
	digits := func() bool {
		start := p.at
		for p.at < len(p.text) && p.text[p.at] >= '0' && p.text[p.at] <= '9' {
			p.at++
		}
		return p.at > start
	}
	if p.peek() == '-' {
		p.at++
	}
	switch {
	case p.peek() == '0':
		p.at++
	case p.peek() >= '1' && p.peek() <= '9':
		digits()
	default:
		return false
	}
	if p.peek() == '.' {
		p.at++
		if !digits() {
			return false
		}
	}
	if c := p.peek(); c == 'e' || c == 'E' {
		p.at++
		if c := p.peek(); c == '+' || c == '-' {
			p.at++
		}
		if !digits() {
			return false
		}
	}
	return true
}

// The oracle must itself be right: these are texts of the grammar and texts that are not.
func TestStrictParse(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		ok   bool
	}{
		{"an empty list", `[]`, true},
		{"white space around", " \t\r\n[ ]\n", true},
		{"nested", `[{"a":[1,-2.5e+3,true,false,null,"x"],"b":{}}]`, true},
		{"zero", `[0,-0,0.5,10]`, true},
		{"the same name in two objects", `[{"a":1},{"a":2}]`, true},
		{"the same name at two depths", `{"a":{"a":1}}`, true},
		{"escapes", "[\"\\\"\\\\\\/\\b\\f\\n\\r\\t\\u00e9\\ud83d\\ude00\"]", true},
		{"a lone surrogate", `["\ud800"]`, true},
		{"a repeated name", `{"a":1,"a":2}`, false},
		{"a repeated name, one escaped", `{"a":1,"a":2}`, false},
		{"a repeated name after a nested value", `{"a":1,"x":{"y":1},"x":2}`, false},
		{"a repeated empty name", `{"":1,"":2}`, false},
		{"empty", ``, false},
		{"two values", `[] []`, false},
		{"trailing text", `[]x`, false},
		{"a trailing comma", `[1,]`, false},
		{"a leading comma", `[,1]`, false},
		{"an object with a trailing comma", `{"a":1,}`, false},
		{"a name that is not a string", `{1:2}`, false},
		{"a missing colon", `{"a" 1}`, false},
		{"a leading zero", `[01]`, false},
		{"a lone minus", `[-]`, false},
		{"a dot without digits", `[1.]`, false},
		{"an exponent without digits", `[1e]`, false},
		{"a plus sign", `[+1]`, false},
		{"NaN", `[NaN]`, false},
		{"a single-quoted string", `['a']`, false},
		{"a control character in a string", "[\"a\x01\"]", false},
		{"a raw newline in a string", "[\"a\nb\"]", false},
		{"a bad escape", `["\x"]`, false},
		{"a short unicode escape", `["\u12"]`, false},
		{"a bad hex digit", `["\u12g4"]`, false},
		{"an unterminated string", `["a`, false},
		{"an unterminated list", `[1`, false},
		{"a byte order mark", "\xef\xbb\xbf[]", false},
		{"invalid UTF-8", "[\"\xff\"]", false},
		{"a literal that is cut", `[tru]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := strictParse([]byte(tc.text)); ok != tc.ok {
				t.Errorf("strictParse(%q) accepted = %v, want %v", tc.text, ok, tc.ok)
			}
		})
	}
}
