package regression

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// maxFuzzEnvFile bounds the input of FuzzParseEnvFile: a file of any size runs the same code, and the
// fuzzing engine minimizes a long input slowly.
const maxFuzzEnvFile = 1024

// refusedLine is the one text ParseEnvFile refuses a line with: the number of the line and nothing of what
// it holds, because the file holds the switch's password.
var refusedLine = regexp.MustCompile(`^line ([0-9]+) is not KEY=VALUE$`)

// envText writes a parsed file back as text, one quoted line per key in the order of keys.
func envText(env map[string]string, keys []string) string {
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=\"" + env[k] + "\"\n")
	}
	return b.String()
}

// FuzzParseEnvFile: no text makes ParseEnvFile panic; a refusal carries the line number and none of the
// text (so a password on that line cannot reach a log); what it accepts has keys that are not empty and
// values without quotes around them, no more entries than there are lines, and it is the same read again
// (the file is parsed once per run, so a parse that changed with the second call would be a surprise).
// A parsed file written back in the quoted form reads back as the same map, except for the keys the
// format cannot spell (one that starts with "export " or with "#").
func FuzzParseEnvFile(f *testing.F) {
	for _, seed := range []string{
		"",
		"A=1\n",
		"# comment\n\nA=1\nexport B=\"two words\"\nC='x'\n D = y \n",
		"A=1\r\nB=2\r\n",
		"A=1\nsecret-without-equals\n",
		"=v\n",
		" = \n",
		"export \n",
		"export export A=1\n", //nolint:dupword // the seed is that text: a key that is "export A"
		"A==\n",
		"A=\"\"\n",
		"A='\"mixed\"'\n",
		"A=\"unclosed\n",
		"A=1\nA=2\n",
		"#A=1",
		"export #A=1\n",
		"\x00=\x00\n",
		"K=" + strings.Repeat("v", 100),
		"PASSWORD=hunter2\nnot a line\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > maxFuzzEnvFile {
			text = text[:maxFuzzEnvFile]
		}
		env, err := ParseEnvFile(strings.NewReader(text))
		lines := strings.Count(text, "\n") + 1
		if err != nil {
			if m := refusedLine.FindStringSubmatch(err.Error()); m == nil {
				t.Fatalf("the refusal of %q carries something else than the line number: %v", text, err)
			} else if n, _ := strconv.Atoi(m[1]); n < 1 || n > lines {
				t.Fatalf("the refusal of %q names line %d, and the text has %d", text, n, lines)
			}
			if env != nil {
				t.Fatalf("the refusal of %q came with a map of %d entries", text, len(env))
			}
			return
		}
		if len(env) > lines {
			t.Fatalf("%q has %d lines and gave %d entries", text, lines, len(env))
		}
		keys := make([]string, 0, len(env))
		for k, v := range env {
			if k == "" || k != strings.TrimSpace(k) || strings.Contains(k, "=") {
				t.Fatalf("%q gave the key %q", text, k)
			}
			if strings.HasPrefix(v, `"`) || strings.HasSuffix(v, `"`) || strings.HasPrefix(v, "'") || strings.HasSuffix(v, "'") {
				t.Fatalf("%q gave the value %q of %q with its quotes", text, v, k)
			}
			keys = append(keys, k)
		}
		again, err := ParseEnvFile(strings.NewReader(text))
		if err != nil || fmt.Sprint(again) != fmt.Sprint(env) {
			t.Fatalf("%q read twice gave %v and then %v, %v", text, env, again, err)
		}
		if slices.ContainsFunc(keys, func(k string) bool { return strings.HasPrefix(k, "export ") || strings.HasPrefix(k, "#") }) {
			return
		}
		slices.Sort(keys)
		back, err := ParseEnvFile(strings.NewReader(envText(env, keys)))
		if err != nil || fmt.Sprint(back) != fmt.Sprint(env) {
			t.Fatalf("%q gave %v, which written back reads as %v, %v", text, env, back, err)
		}
	})
}
