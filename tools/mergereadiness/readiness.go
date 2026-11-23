package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"unicode"
)

// Limits of the output and of the input.
const (
	// maxListed is the most boxes a verdict lists.
	maxListed = 50
	// maxLine is the longest text of a box that is printed, in characters.
	maxLine = 200
	// maxNotes is the most lines a verdict lists that hold the title of the section without being its heading.
	maxNotes = 3
	// maxBody is the largest description the program reads, in bytes. The description of a pull request is
	// at most 65536 characters, which is at most 256 KiB.
	maxBody = 1 << 20
	// ellipsis ends a text that was cut.
	ellipsis = "..."
	// boxPrefix starts every line that names a box.
	boxPrefix = "  - [ ] "
	// notePrefix starts every line that explains a verdict.
	notePrefix = "  note: "
)

// Messages of the verdicts.
const (
	msgNoSection = "no merge readiness section: nothing to check"
	msgAllTicked = "every box of the merge readiness section is ticked"
	msgNoBoxes   = "merge readiness: the section has no boxes that this check reads: restore the boxes of the template, or delete the section"
	msgTooLarge  = "merge readiness: the description is larger than 1 MiB: shorten it"
	msgUsage     = "usage: mergereadiness -body-file FILE"
	// msgAmbiguousNote says what the lines of an ambiguous verdict may be.
	msgAmbiguousNote = notePrefix + "a box-shaped line that this check cannot attribute to the section is a copy of the section in a code block, " +
		"a comment or an HTML block, a box in a later section (the section must be the last one), or markup it does not read: " +
		"tick it, remove it, or write it as a plain \"- [ ]\" item"
)

// Verdict is the outcome of the check for one description.
type Verdict struct {
	// Pass is true when the pull request may pass the check.
	Pass bool
	// Unticked is the number of boxes the verdict names, listed or not.
	Unticked int
	// Lines is the output, without line ends, in the order it is printed.
	Lines []string
}

// Judge reads the description and returns the verdict.
func Judge(body string) Verdict {
	lines := splitLines(body)
	structured := structuredPass(lines)
	starts, naive := naivePass(lines)
	notes := titleNotes(starts, structured.headings)
	switch {
	case len(naive) > len(structured.unticked):
		heading := fmt.Sprintf("merge readiness: ambiguous markup: %d possible box(es) not ticked, %d recognized:",
			len(naive), len(structured.unticked)+structured.ticked)
		return red(heading, naive, append([]string{msgAmbiguousNote}, notes...))
	case len(structured.unticked) > 0:
		return red(fmt.Sprintf("merge readiness: %d box(es) not ticked:", len(structured.unticked)), structured.unticked, notes)
	case len(starts) == 0:
		return Verdict{Pass: true, Lines: []string{msgNoSection}}
	case structured.ticked == 0:
		return Verdict{Lines: append([]string{msgNoBoxes}, notes...)}
	default:
		return Verdict{Pass: true, Lines: []string{msgAllTicked}}
	}
}

// titleNotes explains each line that holds the title of the section without being a heading that the
// structured reading read: a copy of the section in a code block or a comment, or a form of heading it does
// not read. At most maxNotes lines are explained.
func titleNotes(starts, headings []int) []string {
	var notes []string
	for _, start := range starts {
		if len(notes) == maxNotes {
			break
		}
		if !slices.Contains(headings, start) {
			notes = append(notes, fmt.Sprintf(
				"%sline %d holds the title of the section but is not read as its heading (a copy of the section in a code block, "+
					"a comment or an HTML block, or a form of heading this check does not read): "+
					"remove the checkboxes of the copy, or the copy itself", notePrefix, start))
		}
	}
	return notes
}

// red is the verdict that lists boxes, at most maxListed of them, followed by the notes. Every box is
// printed after the fixed prefix, so a line of the output never starts with the text of a box, and its
// characters are made printable.
func red(heading string, boxes []box, notes []string) Verdict {
	lines := []string{heading}
	for _, b := range boxes[:min(len(boxes), maxListed)] {
		lines = append(lines, fmt.Sprintf("%sline %d: %s", boxPrefix, b.line, printable(b.text)))
	}
	if len(boxes) > maxListed {
		lines = append(lines, fmt.Sprintf("... and %d more", len(boxes)-maxListed))
	}
	return Verdict{Unticked: len(boxes), Lines: append(lines, notes...)}
}

// printable is the text with each control or other unprintable character replaced with "?", and cut
// to maxLine characters.
func printable(text string) string {
	runes := []rune(text)
	for i, r := range runes {
		if !unicode.IsPrint(r) {
			runes[i] = '?'
		}
	}
	if len(runes) > maxLine {
		return string(runes[:maxLine-len(ellipsis)]) + ellipsis
	}
	return string(runes)
}

// Exit codes of Run.
const (
	exitPass  = 0
	exitRed   = 1
	exitUsage = 2
)

// errTooLarge is the error of a description over maxBody bytes.
var errTooLarge = errors.New("the description is too large")

// readBody reads the description from the file, up to maxBody bytes.
func readBody(path string) (string, error) {
	file, err := os.Open(path) //nolint:gosec // the path is the one the operator names on the command line
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maxBody+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxBody {
		return "", errTooLarge
	}
	return string(data), nil
}

// Run is the whole command: args are the command line without the program name, which names the file that
// holds the description (-body-file). It prints the verdict to out and returns the exit status: 0 when the
// pull request passes, 1 when it does not, when the description cannot be read or is too large, or when
// the verdict cannot be written, 2 for a command line it does not understand. Nothing else is read: no
// environment variable, no other file.
func Run(args []string, out io.Writer) int {
	flags := flag.NewFlagSet("mergereadiness", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	path := flags.String("body-file", "", "the file that holds the description")
	if err := flags.Parse(args); err != nil || *path == "" || flags.NArg() != 0 {
		_, _ = io.WriteString(out, msgUsage+"\n")
		return exitUsage
	}
	body, err := readBody(*path)
	if err != nil {
		message := msgTooLarge
		if !errors.Is(err, errTooLarge) {
			message = "merge readiness: the description could not be read: " + printable(err.Error())
		}
		_, _ = io.WriteString(out, message+"\n")
		return exitRed
	}
	verdict := Judge(body)
	text := strings.Join(verdict.Lines, "\n") + "\n"
	if _, err := io.WriteString(out, text); err != nil || !verdict.Pass {
		return exitRed
	}
	return exitPass
}
