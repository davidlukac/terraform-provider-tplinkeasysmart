package main

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// defaultCatalog is the catalog of the library, relative to the repository root.
	defaultCatalog = "docs/defect-classes.md"
	// defaultLetters are the class letters the library's catalog defines: DC-C (common) and DC-L (library).
	defaultLetters = "C,L"
)

// Options say which page is the catalog and which ids it defines; the defaults are the library's.
type Options struct {
	// Catalog is the path of the catalog page, slash-separated, relative to the repository root.
	Catalog string
	// Letters are the class letters of the ids the page defines (`P` for DC-P01), comma-separated. An id
	// with another letter belongs to another repository's catalog: it is checked for its shape only.
	Letters string
}

// DefaultOptions are the options of the library's own catalog.
func DefaultOptions() Options { return Options{Catalog: defaultCatalog, Letters: defaultLetters} }

// classLetter is one class letter of an id.
var classLetter = regexp.MustCompile(`^[A-Z]$`)

// rules are the options made ready for use: the path, the letters and the patterns that depend on them.
type rules struct {
	path    string
	letters []string
	// entryHeading is `### DC-<letter>nn <title>` for the letters of the page.
	entryHeading *regexp.Regexp
	// routingLink is a link of the routing table to an entry of the page: `[DC-<letter>nn](#anchor)`.
	routingLink *regexp.Regexp
	// pageLink is a link into a catalog page by its file name: `<prefix>defect-classes.md#<anchor>`.
	pageLink *regexp.Regexp
}

// idUse is a class id written anywhere, of any letter; only the ids with a letter of the page must be entries.
var idUse = regexp.MustCompile(`\bDC-([A-Z])[0-9]{2}\b`)

// newRules checks the options and builds the patterns. An error is a usage error.
func newRules(o Options) (rules, error) {
	var r rules
	if o.Catalog == "" || path.IsAbs(o.Catalog) || !filepath.IsLocal(filepath.FromSlash(o.Catalog)) {
		return r, fmt.Errorf("the catalog path %q must be a path below the repository root", o.Catalog)
	}
	r.path = path.Clean(o.Catalog)
	seen := map[string]bool{}
	for _, l := range strings.Split(o.Letters, ",") {
		if !classLetter.MatchString(l) {
			return r, fmt.Errorf("the id letters %q must be capital letters separated by commas (C,L for DC-C01 and DC-L01)", o.Letters)
		}
		if seen[l] {
			return r, errors.New("the id letter " + l + " is listed twice")
		}
		seen[l] = true
		r.letters = append(r.letters, l)
	}
	class := "DC-[" + strings.Join(r.letters, "") + "][0-9]{2}"
	r.entryHeading = regexp.MustCompile(`^### (` + class + `)( .+)?$`)
	r.routingLink = regexp.MustCompile(`\[(` + class + `)\]\(#([a-z0-9-]*)\)`)
	r.pageLink = regexp.MustCompile(`([A-Za-z0-9_./:~%@+-]*)` + regexp.QuoteMeta(path.Base(r.path)) + `#([A-Za-z0-9_-]+)`)
	return r, nil
}

// owns reports whether the page defines the ids of the class letter.
func (r rules) owns(letter string) bool { return indexOf(r.letters, letter) >= 0 }

// headingForm is what an entry heading must look like, for the message of one that does not.
func (r rules) headingForm() string {
	return "### DC-<" + strings.Join(r.letters, "|") + "><two digits> <title>"
}
