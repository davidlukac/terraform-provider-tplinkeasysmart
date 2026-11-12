package main

import (
	"fmt"
	"regexp"
	"strings"
)

// routingHeading starts the section whose table routes a change to the entries it touches.
const routingHeading = "## Routing"

// fieldOrder is the order of the fields of an entry, as the catalog's "Entry format" paragraph states it.
var fieldOrder = []string{"Rule", "Why", "Design question", "Test requirement", "Automation", "Adversarial probe"}

// automationField is the label of the field whose tokens are resolved.
const automationField = "Automation"

var (
	anchorLine = regexp.MustCompile(`^<a id="([^"]+)"></a>\s*$`)
	badHeading = regexp.MustCompile(`^### DC-`)
	fieldStart = regexp.MustCompile(`^\*\*([^*]+)\.\*\*(.*)$`)
	selfLink   = regexp.MustCompile(`\]\(#([a-z0-9-]+)\)`)
)

// Problem is one thing wrong with the catalog or with a name it uses.
type Problem struct {
	File string
	Line int
	Msg  string
}

// String is the one-line form the command prints.
func (p Problem) String() string { return fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.Msg) }

// field is one `**Label.** text` paragraph of an entry; the text runs to the next blank line.
type field struct {
	label string
	line  int
	text  string
}

// entry is one class of the catalog.
type entry struct {
	id     string
	line   int
	fields []field
}

// row is a row of a table of the page that is not part of an entry.
type row struct {
	line int
	text string
}

// link is a link of the routing table to an entry.
type link struct {
	id     string
	anchor string
	line   int
}

// catalog is what the parser makes of the page.
type catalog struct {
	// path is the page's path, for the problems it reports.
	path    string
	entries []entry
	// rows are the rows of the tables outside the entries and outside the routing table: a sentence
	// there may name a check as well ("how the common classes apply here").
	rows    []row
	routing []link
	// anchors holds every anchor a link into the page may use: the entries' and the headings'.
	anchors  map[string]bool
	problems []Problem
}

// catalogParser holds the state of one pass over the page.
type catalogParser struct {
	rules   rules
	cat     catalog
	cur     *entry
	open    *field // the field a following line may continue, nil after a blank line
	pending string // an anchor line seen and not yet followed by its heading
	pendAt  int
	routing bool
}

// parseCatalog reads the page. It reports the problems of the page's own shape (a malformed heading,
// an anchor that is not its heading's id); the checks of the fields and the names come after.
func parseCatalog(text string, rl rules) catalog {
	p := &catalogParser{rules: rl, cat: catalog{path: rl.path, anchors: map[string]bool{}}}
	for i, line := range strings.Split(text, "\n") {
		p.line(i+1, line)
	}
	p.finishEntry()
	p.dropPending()
	return p.cat
}

func (p *catalogParser) problem(line int, format string, args ...any) {
	p.cat.problems = append(p.cat.problems, Problem{File: p.rules.path, Line: line, Msg: fmt.Sprintf(format, args...)})
}

// line takes one line of the page.
func (p *catalogParser) line(n int, line string) {
	if strings.HasPrefix(line, "#") {
		p.cat.anchors[slug(line)] = true
	}
	if m := selfLink.FindAllStringSubmatch(line, -1); m != nil {
		// A link inside the page is checked after the whole page is read (an anchor may come later).
		for _, l := range m {
			p.cat.routing = append(p.cat.routing, link{anchor: l[1], line: n})
		}
	}
	switch {
	case anchorLine.MatchString(line):
		p.dropPending()
		p.pending, p.pendAt = anchorLine.FindStringSubmatch(line)[1], n
		p.cat.anchors[p.pending] = true
	case p.rules.entryHeading.MatchString(line):
		p.startEntry(n, p.rules.entryHeading.FindStringSubmatch(line)[1])
	case badHeading.MatchString(line):
		p.problem(n, "entry heading must be `%s`: %q", p.rules.headingForm(), line)
	case strings.HasPrefix(line, "## "), strings.TrimSpace(line) == "---":
		p.finishEntry()
		p.dropPending()
		p.routing = strings.HasPrefix(line, routingHeading)
	default:
		p.body(n, line)
	}
}

// startEntry opens the entry whose heading is at line n and checks it against the anchor before it.
func (p *catalogParser) startEntry(n int, id string) {
	p.finishEntry()
	want := strings.ToLower(id)
	switch {
	case p.pending == "":
		p.problem(n, "%s has no anchor line `<a id=%q></a>` before its heading", id, want)
	case p.pending != want:
		p.problem(p.pendAt, "anchor %q does not match the heading %s (want %q)", p.pending, id, want)
	}
	p.pending = ""
	p.cur = &entry{id: id, line: n}
	p.routing = false
}

// dropPending reports an anchor that no entry heading followed.
func (p *catalogParser) dropPending() {
	if p.pending != "" {
		p.problem(p.pendAt, "anchor %q is not followed by an entry heading", p.pending)
	}
	p.pending = ""
}

// finishEntry closes the entry being read and keeps it.
func (p *catalogParser) finishEntry() {
	if p.cur != nil {
		p.cat.entries = append(p.cat.entries, *p.cur)
	}
	p.cur, p.open = nil, nil
}

// body takes a line that is neither a heading nor an anchor: a row of the routing table, or part of
// the field of an entry.
func (p *catalogParser) body(n int, line string) {
	if p.routing {
		for _, m := range p.rules.routingLink.FindAllStringSubmatch(line, -1) {
			p.cat.routing = append(p.cat.routing, link{id: m[1], anchor: m[2], line: n})
		}
	}
	if p.cur == nil {
		if !p.routing && strings.HasPrefix(strings.TrimSpace(line), "|") {
			p.cat.rows = append(p.cat.rows, row{line: n, text: line})
		}
		return
	}
	if strings.TrimSpace(line) == "" {
		p.open = nil
		return
	}
	if m := fieldStart.FindStringSubmatch(line); m != nil {
		p.cur.fields = append(p.cur.fields, field{label: m[1], line: n, text: strings.TrimSpace(m[2])})
		p.open = &p.cur.fields[len(p.cur.fields)-1]
		return
	}
	if p.open != nil {
		p.open.text += " " + strings.TrimSpace(line)
	}
}

// slug is the anchor GitHub makes of a heading: lower case, punctuation dropped, spaces to hyphens.
func slug(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(strings.TrimLeft(heading, "# "))) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-', r == '_', r >= '0' && r <= '9', r >= 'a' && r <= 'z':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// shapeProblems checks the fields of every entry: each of fieldOrder once, in order, not empty.
func (c catalog) shapeProblems() []Problem {
	var out []Problem
	for _, e := range c.entries {
		out = append(out, entryShape(c.path, e)...)
	}
	return out
}

func entryShape(file string, e entry) []Problem {
	var out []Problem
	add := func(line int, format string, args ...any) {
		out = append(out, Problem{File: file, Line: line, Msg: e.id + ": " + fmt.Sprintf(format, args...)})
	}
	next := 0 // index into fieldOrder of the field expected next
	seen := map[string]bool{}
	for _, f := range e.fields {
		idx := indexOf(fieldOrder, f.label)
		switch {
		case idx < 0:
			add(f.line, "unknown field %q (the fields are %s)", f.label, strings.Join(fieldOrder, ", "))
			continue
		case seen[f.label]:
			add(f.line, "field %q appears twice", f.label)
			continue
		case idx < next:
			add(f.line, "field %q is out of order (the order is %s)", f.label, strings.Join(fieldOrder, ", "))
		}
		seen[f.label] = true
		next = max(next, idx+1)
		if f.text == "" {
			add(f.line, "field %q is empty", f.label)
		}
	}
	for _, want := range fieldOrder {
		if !seen[want] {
			add(e.line, "missing field %q", want)
		}
	}
	return out
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// routingProblems checks the routing table against the entries and the anchors of the links: an entry
// is routed, and a link goes to the anchor of its id. (An id of the table that is not an entry is
// reported with the other ids that resolve to nothing.)
func (c catalog) routingProblems() []Problem {
	var out []Problem
	routed := map[string]bool{}
	for _, l := range c.routing {
		if l.id == "" { // a link inside the page, not a row of the table
			if !c.anchors[l.anchor] {
				out = append(out, Problem{c.path, l.line, fmt.Sprintf("the link #%s goes to no anchor of this page", l.anchor)})
			}
			continue
		}
		routed[l.id] = true
		if l.anchor != strings.ToLower(l.id) {
			out = append(out, Problem{c.path, l.line, fmt.Sprintf("the routing link of %s goes to #%s, not #%s", l.id, l.anchor, strings.ToLower(l.id))})
		}
	}
	for _, e := range c.entries {
		if !routed[e.id] {
			out = append(out, Problem{c.path, e.line, fmt.Sprintf("%s is in no row of the routing table", e.id)})
		}
	}
	return out
}
