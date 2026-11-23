package audit

import (
	"bytes"
	"fmt"
	"io"

	"github.com/davidlukac/terraform-provider-tplinkeasysmart/tools/zizmorverdict/verdict" //nolint:depguard // a build tool imports its own and its sibling tools' packages; the rule keeps product code from importing a tool
)

const (
	// linePrefix starts every line of zizmor's output that is forwarded, so that no line of the job log starts
	// with "::" (a workflow command) whatever zizmor wrote.
	linePrefix = "zizmor: "
	// maxForwardedLines is the most lines of zizmor's standard error that are forwarded; the rest is counted.
	maxForwardedLines = 200
	// maxLineBytes is the most bytes of a line that are kept before it is escaped (a character takes at most
	// four, and the escaper keeps 200 characters): a line that is longer ends in "...".
	maxLineBytes = 4 * 200
	// ellipsis ends a line that was cut before it was escaped.
	ellipsis = "..."
)

// lineEscaper is the writer zizmor's standard error goes to. The text is untrusted: zizmor writes the names of
// the files it audits, and a file name can hold a new line and anything after it, such as "::warning::" (a
// workflow command of the job log). Every line is therefore written as linePrefix and the line made safe by
// verdict.Escape (printable ASCII, at most 200 characters), at most maxForwardedLines lines are written, and
// Close says how many more there were. It is not safe for concurrent use.
type lineEscaper struct {
	out io.Writer
	// pending is the start of the line being written, cut at maxLineBytes; cut says it was longer.
	pending []byte
	cut     bool
	// written and dropped count the lines forwarded and the lines left out.
	written, dropped int
}

// Write splits p into lines, escapes each complete one and keeps the rest for the next call. It reports all of
// p as written and never fails: what the output refuses is lost, as for a terminal that went away.
func (l *lineEscaper) Write(p []byte) (int, error) {
	rest := p
	for {
		end := bytes.IndexByte(rest, '\n')
		if end < 0 {
			l.keep(rest)
			return len(p), nil
		}
		l.keep(rest[:end])
		l.flush()
		rest = rest[end+1:]
	}
}

// keep adds text to the line being written, up to maxLineBytes.
func (l *lineEscaper) keep(text []byte) {
	room := maxLineBytes - len(l.pending)
	if len(text) > room {
		text, l.cut = text[:room], true
	}
	l.pending = append(l.pending, text...)
}

// flush writes the line being written, or counts it when the lines forwarded are at the limit.
func (l *lineEscaper) flush() {
	line := string(bytes.TrimSuffix(l.pending, []byte("\r")))
	cut := l.cut
	l.pending, l.cut = l.pending[:0], false
	if l.written >= maxForwardedLines {
		l.dropped++
		return
	}
	l.written++
	text := verdict.Escape(line)
	if cut {
		text += ellipsis
	}
	_, _ = io.WriteString(l.out, linePrefix+text+"\n")
}

// Close writes the line that has no end, and the number of lines that were left out.
func (l *lineEscaper) Close() {
	if len(l.pending) > 0 || l.cut {
		l.flush()
	}
	if l.dropped > 0 {
		_, _ = fmt.Fprintf(l.out, "%sand %d more lines of its output were left out\n", linePrefix, l.dropped)
	}
}
