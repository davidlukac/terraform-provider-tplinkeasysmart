package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	// textFile is what is searched for ids and links: documentation, code, workflows and scripts.
	textFile = regexp.MustCompile(`(\.(md|go|yml|yaml|sh|txt)|(^|/)Makefile)$`)
)

// Check reads the catalog of the repository at root and everything it names, and returns the
// problems, sorted by file and line. An error means the repository could not be read at all.
func Check(root string, opts Options) ([]Problem, error) {
	rl, err := newRules(opts)
	if err != nil {
		return nil, err
	}
	text, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rl.path)))
	if err != nil {
		return nil, fmt.Errorf("reading the catalog: %w", err)
	}
	r, err := loadRepo(root)
	if err != nil {
		return nil, err
	}
	cat := parseCatalog(string(text), rl)
	problems := append([]Problem{}, r.problems...)
	problems = append(problems, cat.problems...)
	problems = append(problems, cat.shapeProblems()...)
	problems = append(problems, cat.routingProblems()...)
	problems = append(problems, r.automationProblems(cat)...)
	idProblems, err := r.idProblems(cat, rl)
	if err != nil {
		return nil, err
	}
	problems = append(problems, idProblems...)
	sort.SliceStable(problems, func(i, j int) bool {
		if problems[i].File != problems[j].File {
			return problems[i].File < problems[j].File
		}
		return problems[i].Line < problems[j].Line
	})
	return problems, nil
}

// automationProblems resolves the tokens of the Automation field of every entry, and those of the
// rows of the other tables of the page.
func (r *repo) automationProblems(cat catalog) []Problem {
	var out []Problem
	for _, e := range cat.entries {
		for _, f := range e.fields {
			if f.label != automationField {
				continue
			}
			tokens, bad := parseTokens(f.text)
			for _, b := range bad {
				out = append(out, Problem{cat.path, f.line, e.id + ": " + b})
			}
			if len(tokens) == 0 {
				out = append(out, Problem{cat.path, f.line, e.id + ": Automation names no check (a claim without a name is not accepted: use `reading:` with a reason or `proposed:` with an issue)"})
			}
			out = append(out, r.tokensProblems(cat.path, f.line, e.id+": ", tokens)...)
		}
	}
	for _, row := range cat.rows {
		// Outside an Automation field a span that only looks like a token (an unknown kind, a kind
		// without a name, as in the page's own description of the kinds) is prose, not a claim.
		tokens, _ := parseTokens(row.text)
		var claims []automationToken
		for _, t := range tokens {
			if t.kind == kindReading || t.name != "" {
				claims = append(claims, t)
			}
		}
		out = append(out, r.tokensProblems(cat.path, row.line, "", claims)...)
	}
	return out
}

// tokensProblems resolves tokens found at a line of the page; prefix starts each message.
func (r *repo) tokensProblems(file string, line int, prefix string, tokens []automationToken) []Problem {
	var out []Problem
	for _, t := range tokens {
		for _, msg := range r.tokenProblems(t) {
			out = append(out, Problem{file, line, prefix + msg})
		}
	}
	return out
}

// tokenProblems checks one token: its form, then that its name resolves in the repository.
func (r *repo) tokenProblems(t automationToken) []string {
	if msgs := t.formProblems(); msgs != nil {
		return msgs
	}
	if t.kind == kindProposed {
		return r.proposedProblems(t)
	}
	if t.kind == kindReading || r.resolves(t) {
		return nil
	}
	return []string{fmt.Sprintf("`%s:%s` does not resolve: %s", t.kind, t.name, r.whereToLook(t.kind))}
}

// resolves reports whether the name of a token of a kind that names something exists.
func (r *repo) resolves(t automationToken) bool {
	switch t.kind {
	case kindTest, kindFuzz:
		return r.funcs[t.name]
	case kindMake:
		return r.targets[t.name]
	case kindLint:
		return r.linters[t.name]
	case kindAnalyzer:
		return r.devDirs[t.name] || r.analyzers[t.name]
	case kindScript:
		return r.has(t.name)
	case kindWorkflow:
		return r.has(".github/workflows/" + t.name)
	}
	return false
}

// whereToLook says where a name of the kind is looked up, for the message of a name that is not there.
func (r *repo) whereToLook(kind string) string {
	switch kind {
	case kindTest:
		return "no function of that name in a _test.go file (a renamed or removed test?)"
	case kindFuzz:
		return "no fuzz target of that name in a _test.go file"
	case kindMake:
		return "no such target in the Makefile"
	case kindLint:
		if !r.lintersOK {
			return "the " + lintConfig + " has no `linters: enable:` list"
		}
		return "not enabled in " + lintConfig
	case kindAnalyzer:
		return "no directory of that name in devtools, and no analyzer of that name declared there"
	case kindScript:
		return "no such file"
	default:
		return "no such file in .github/workflows"
	}
}

// proposedProblems reports a proposal whose name already exists: it was built, and the sentence
// must name it with its kind.
func (r *repo) proposedProblems(t automationToken) []string {
	base, _, _ := strings.Cut(t.name, "(")
	if r.funcs[base] || r.targets[base] || r.devDirs[base] || r.analyzers[base] {
		return []string{fmt.Sprintf("`proposed:%s` is built already: name it with its kind (test:, fuzz:, make:, analyzer:)", t.name)}
	}
	return nil
}

// idProblems checks every `DC-xnn` id and every link `defect-classes.md#anchor` written in a text
// file of the repository. An id of a letter the page defines must be an entry, a link to the page
// itself must go to an anchor of it. An id of another letter, and a link by an absolute URL (the page
// of another repository, which is not read), are checked for their shape only.
func (r *repo) idProblems(cat catalog, rl rules) ([]Problem, error) {
	sc := idScope{rules: rl, ids: map[string]bool{}, anchors: cat.anchors}
	for _, e := range cat.entries {
		sc.ids[e.id] = true
	}
	var out []Problem
	for _, rel := range r.files {
		if !textFile.MatchString(rel) || strings.HasPrefix(rel, devtoolsDir+"/") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(rel)))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", rel, err)
		}
		out = append(out, sc.problems(rel, string(data))...)
	}
	return out, nil
}

// idScope is what the ids and links of a file are resolved against.
type idScope struct {
	rules   rules
	ids     map[string]bool // the entries of the page
	anchors map[string]bool // the anchors of the page
}

// foreignAnchor is the shape of an anchor of a page that is not read: lower case, as GitHub makes it,
// and an id-like start (`dc-` and a letter and a digit) must be a whole id.
var (
	foreignAnchor = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
	idLikeAnchor  = regexp.MustCompile(`^dc-[a-z][0-9]`)
	idAnchor      = regexp.MustCompile(`^dc-[a-z][0-9]{2}(-[a-z0-9_-]*)?$`)
)

// problems finds the ids and the catalog links of one file that are wrong.
func (sc idScope) problems(rel, text string) []Problem {
	var out []Problem
	base := path.Base(sc.rules.path)
	for i, line := range strings.Split(text, "\n") {
		for _, m := range idUse.FindAllStringSubmatch(line, -1) {
			if sc.rules.owns(m[1]) && !sc.ids[m[0]] {
				out = append(out, Problem{rel, i + 1, fmt.Sprintf("%s is not an entry of %s", m[0], sc.rules.path)})
			}
		}
		for _, m := range sc.rules.pageLink.FindAllStringSubmatch(line, -1) {
			if strings.Contains(m[1], "://") {
				if !foreignAnchor.MatchString(m[2]) || (idLikeAnchor.MatchString(m[2]) && !idAnchor.MatchString(m[2])) {
					out = append(out, Problem{rel, i + 1, fmt.Sprintf("the link %s%s#%s has an anchor of the wrong shape (lower case letters, digits and hyphens; an id is dc-<letter><two digits>)", m[1], base, m[2])})
				}
			} else if !sc.anchors[m[2]] {
				out = append(out, Problem{rel, i + 1, fmt.Sprintf("the link %s#%s goes to no anchor of %s", base, m[2], sc.rules.path)})
			}
		}
	}
	return out
}
