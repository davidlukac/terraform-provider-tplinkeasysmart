package main

import "strings"

const (
	// sectionTitle is the normalized title of the section's heading.
	sectionTitle = "merge readiness"
	// sectionLevel is the level of the section's heading; a heading of this level or a shallower one ends it.
	sectionLevel = 2
)

// splitLines is the description as lines, whatever its line endings.
func splitLines(body string) []line {
	text := strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	parts := strings.Split(text, "\n")
	lines := make([]line, len(parts))
	for i, part := range parts {
		lines[i] = line(part)
	}
	return lines
}

// box is a line that holds an unticked box: its number (counted from 1) and the text after the box.
type box struct {
	line int
	text string
}

// structure is what the structured reading found.
type structure struct {
	headings []int // the line of each heading of a merge readiness section
	unticked []box // the unticked boxes in those sections
	ticked   int   // the number of ticked boxes in them
}

// commentState follows the HTML comments across lines.
type commentState struct {
	// closesLater[i] tells whether a line after line i holds the end of a comment.
	closesLater []bool
	open        bool
}

// newCommentState prepares the look-ahead for the lines.
func newCommentState(lines []line) *commentState {
	state := &commentState{closesLater: make([]bool, len(lines))}
	seen := false
	for i := len(lines) - 1; i >= 0; i-- {
		state.closesLater[i] = seen
		seen = seen || indexFrom(lines[i], 0, commentClose) >= 0
	}
	return state
}

// visibleLines calls visit with the index and the text of each line that the structured reading looks
// at: a line in a fenced code block, or in a comment that is closed later, is not looked at.
func visibleLines(lines []line, visit func(int, line)) {
	comments := newCommentState(lines)
	var fence line
	for i, l := range lines {
		if fence != nil {
			if closesFence(l, fence) {
				fence = nil
			}
			continue
		}
		pos := 0
		if comments.open {
			end := indexFrom(l, 0, commentClose)
			if end == notFound {
				continue
			}
			pos = end + len(commentClose)
			comments.open = false
		} else if fence = fenceMarker(l); fence != nil {
			continue
		}
		var open bool
		pos, open = skipLeadingComments(l, pos, comments.closesLater[i])
		comments.open = open
		if !open {
			visit(i, l[pos:])
		}
	}
}

// skipLeadingComments skips the comments that start at pos, after up to three spaces. It returns the
// position after them and whether the last one stays open on the following lines. A comment that is
// never closed is text: it hides nothing.
func skipLeadingComments(l line, pos int, closesLater bool) (next int, open bool) {
	for {
		indent := countIndent(l[pos:], maxIndent+1)
		start := pos + indent
		if indent > maxIndent || !hasAt(l, start, commentOpen) {
			return pos, false
		}
		end := commentEnd(l, start+len(commentOpen))
		if end == notFound {
			if closesLater {
				return len(l), true
			}
			return pos, false
		}
		pos = end
	}
}

// commentEnd is the position after the end of the comment whose text starts at from, or notFound. The
// renderer reads "<!-->" and "<!--->" as comments that are complete.
func commentEnd(l line, from int) int {
	switch {
	case hasAt(l, from, ">"):
		return from + len(">")
	case hasAt(l, from, "->"):
		return from + len("->")
	}
	if end := indexFrom(l, from, commentClose); end != notFound {
		return end + len(commentClose)
	}
	return notFound
}

// structuredPass reads the headings, fences and comments, and the boxes of the sections.
func structuredPass(lines []line) structure {
	var found structure
	inside := false
	visibleLines(lines, func(index int, l line) {
		if level, title, ok := headingOf(l); ok && level <= sectionLevel {
			inside = level == sectionLevel && title == sectionTitle
			if inside {
				found.headings = append(found.headings, index+1)
			}
			return
		}
		if !inside {
			return
		}
		switch unticked, text, ok := strictBox(l); {
		case !ok:
		case unticked:
			found.unticked = append(found.unticked, box{line: index + 1, text: text})
		default:
			found.ticked++
		}
	})
	return found
}

// naiveTitle is the normalized title a line would have as a heading, whatever its form: the title of an
// ATX heading, also in a quote, and otherwise the whole line without its markup, so that a Setext heading,
// a heading in HTML or a title in bold type is found as well.
func naiveTitle(l line) string {
	rest := l[skipBlanksAndQuotes(l, 0):]
	if _, title, ok := headingOf(rest); ok {
		return title
	}
	return titleOf(rest)
}

// naivePass reads no structure and never ends the section: it returns the line of every line titled
// "merge readiness" in any form, and every line after the first of them that has the shape of an
// unticked box, in code, comments and later sections too.
func naivePass(lines []line) (starts []int, boxes []box) {
	for i, l := range lines {
		if naiveTitle(l) == sectionTitle {
			starts = append(starts, i+1)
			continue
		}
		if len(starts) == 0 {
			continue
		}
		if text, ok := naiveBox(l); ok {
			boxes = append(boxes, box{line: i + 1, text: text})
		}
	}
	return starts, boxes
}
