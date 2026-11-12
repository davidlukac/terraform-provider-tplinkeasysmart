// Command catalogcheck verifies that a defect catalog (docs/defect-classes.md in this repository) is
// true: that every check it names exists, so that renaming a test or removing a make target cannot
// leave a sentence that is no longer so.
//
//	go -C devtools run ./catalogcheck -root .
//
// It prints one line per problem, `file:line: message`, and exits 1 if there is any. What it checks:
//
//   - the shape of each entry: an anchor and a heading with the same id, then the fields Rule, Why,
//     Design question, Test requirement, Automation and Adversarial probe, in that order, none empty;
//   - that every entry is in the routing table, and that every link of the routing table is an entry;
//   - the tokens of each Automation field (`kind:name`, the kinds are listed in the catalog), and the
//     tokens in the rows of the page's other tables (the routing table excepted; a span there that only
//     looks like a token, an unknown kind or a kind without a name, is prose): a `test:` or `fuzz:` name
//     is a function of a `_test.go` file anywhere in the repository, a `make:` name a target of the
//     Makefile, a `lint:` name a linter enabled in .golangci.yml, an `analyzer:` name a directory of
//     devtools (or the name of an analyzer declared there), a `script:` name a file, a `workflow:` name
//     a file of .github/workflows; `reading:` carries its reason, `proposed:` the issue that tracks it
//     in parentheses, and a proposal whose name already exists is out of date;
//   - that every `DC-xnn` id written anywhere else in the repository, and every link into the catalog,
//     resolves.
//
// # Another repository
//
// The defaults are this repository's layout. A repository with its own catalog copies this directory
// (devtools/catalogcheck, the Go files, with no dependency outside the standard library) and runs it
// from its root with the flags below; the Makefile, .golangci.yml, devtools/ and .github/workflows are
// read from the root, and the tests are found wherever they are in the tree, so no flag is needed for
// a different test layout:
//
//	-catalog contributing/defect-classes.md   the catalog page, relative to the root
//	-ids P                                    the class letters of the ids the page defines, comma-separated
//
// # What is checked across catalogs
//
// A catalog may link to the classes of another repository's catalog instead of copying them. The other
// page is not read, so what points at it is checked for its shape only: an id with a letter that the
// page does not define (DC-C01 in a page whose -ids is P) is not required to be an entry, and a link
// by absolute URL to a page of that name (`https://.../defect-classes.md#dc-c01`) must have a lower case
// anchor of the shape GitHub makes, and an anchor that starts like an id must be a whole one
// (`dc-c01`, `dc-c01-title`, not `dc-c1`). Whether the other page has that id is not checked; a pinned
// copy of it would be needed. Everything local is checked fully: an id with a letter of the page must be
// an entry, and a link by a relative path to a page of that name must go to an anchor of this page.
//
// It reads files only, never runs a test or a tool, and has no dependency outside the standard library.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// main is the process entry point.
func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

// run parses the flags, checks the repository and prints the problems; it returns the exit status:
// 0 for a catalog without problems, 1 for problems, 2 for a usage error or an unreadable repository.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("catalogcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	def := DefaultOptions()
	root := fs.String("root", ".", "the repository root")
	catalog := fs.String("catalog", def.Catalog, "the catalog page, relative to the root")
	ids := fs.String("ids", def.Letters, "the class letters of the ids the page defines, comma-separated (P for DC-P01)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	problems, err := Check(*root, Options{Catalog: *catalog, Letters: *ids})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "catalogcheck:", err)
		return 2
	}
	for _, p := range problems {
		_, _ = fmt.Fprintln(stdout, p)
	}
	if len(problems) > 0 {
		_, _ = fmt.Fprintf(stderr, "catalogcheck: %d problem(s) in the defect catalog\n", len(problems))
		return 1
	}
	return 0
}
