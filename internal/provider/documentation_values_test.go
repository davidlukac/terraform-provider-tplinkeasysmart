package provider

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The tracked text files carry documentation values only: MAC addresses from the range reserved by
// RFC 7042 (00:00:5e:00:53:xx) or locally administered ones (the second hex digit of the first octet
// is 2, 6, a or e), and no private 192.168 address, which would be a real network. The CHANGELOG is
// history and stays as it was published.
func TestTheTextCarriesDocumentationValuesOnly(t *testing.T) {
	mac := regexp.MustCompile(`(?i)\b[0-9a-f]{2}([:-])[0-9a-f]{2}(?:[:-][0-9a-f]{2}){4}\b`)
	private := regexp.MustCompile(`\b192\.168\.\d{1,3}\.\d{1,3}\b`)
	err := filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == ".git" || name == "bin" || name == "dist" || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(name) {
		case ".go", ".md", ".tf", ".yml", ".yaml", ".json", ".sh", ".tmpl":
		default:
			return nil
		}
		if name == "CHANGELOG.md" || name == "documentation_values_test.go" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		for _, m := range mac.FindAllString(text, -1) {
			if !documentationMAC(m) {
				t.Errorf("%s: %q is not a documentation or locally administered MAC address", path, m)
			}
		}
		for _, a := range private.FindAllString(text, -1) {
			t.Errorf("%s: %q is a private network address; use 192.0.2.0/24", path, a)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func documentationMAC(m string) bool {
	hex := strings.ToLower(strings.NewReplacer(":", "", "-", "").Replace(m))
	if strings.HasPrefix(hex, "00005e0053") {
		return true
	}
	first, err := strconv.ParseUint(hex[:2], 16, 8)
	return err == nil && first&0x02 != 0 // locally administered
}
