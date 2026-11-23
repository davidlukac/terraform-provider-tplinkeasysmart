// Command mergereadiness is the check behind the "Merge readiness" job: it judges the "Merge readiness"
// section of a pull request description. It reads the description from the file named by -body-file (at
// most 1 MiB), prints which boxes of the section are not ticked and says whether the pull request may pass
// the check: it exits 1 when there are unticked boxes, when the markup is ambiguous, or when the section
// has no boxes it reads, and 0 otherwise. A description without the section passes with a message. It is a
// build tool of this repository, not part of the API.
//
//	go run ./tools/mergereadiness -body-file description.md
//
// The logic, the tests and the recorded renderings in testdata/oracle are a copy made by hand of the
// program of the same name in the escp-go library (github.com/davidlukac/escp-go, internal/mergereadiness),
// which is their source: a change to the rules is made there first, then copied here, so that both
// repositories judge a description the same way. The program is one package here, because the layering rule
// of this repository's linter (depguard) forbids a tool to import another package under tools/.
//
// The description is untrusted input and is read as text: no regular expression is applied to it, and
// every scan is a loop over the characters. The time is not claimed to be linear in every case: the
// notes that name a title which is not read as a heading look each line titled "Merge readiness" up in
// the list of headings found, so on a description made of many such lines the time is quadratic (1 MiB of
// them took about half a second when measured). The limit of 1 MiB bounds it. The command reads the
// description from the file named by -body-file, at most 1 MiB. A test sets the environment variable
// PR_BODY and shows that the program does not read it; that is all the tests show, and the program makes
// no wider promise about the environment.
//
// # What counts
//
// The section starts at the heading "## Merge readiness" (the title is compared without regard to case,
// without its inline markup such as emphasis, code marks and link brackets, and with its whitespace
// collapsed; up to three spaces may precede the heading) and ends at the next heading of level 1 or 2, so
// a "### Pending work" subsection belongs to it. A box is a list item, with the marker -, * or +, whose
// text starts with [ ] (unticked) or [x] or [X] (ticked). Lines end with LF, CRLF or CR.
//
// # Stricter than the renderer, and cross-checked
//
// The renderer of the hosting service is the only judge of what a reader sees and this package is not
// it, so the description is read twice, and a disagreement in the direction of more unticked boxes is
// red. The structured reading follows headings, fenced code blocks and HTML comments: a fence is opened by
// a line that starts with it, a comment hides text only when it starts a line and is closed later (the
// forms "<!-->" and "<!--->" are complete comments), and neither combines with the other on one line. The
// naive reading knows no structure and does not end: from the first line titled "Merge readiness" in any
// form (an ATX heading at any level, a quoted heading, a Setext heading, a heading in HTML, a title in bold
// type) to the end of the description it counts every line that has the shape of an unticked task list
// item, with any list marker, nested markers, a quote prefix, a footnote label in front of it, a numbered
// item, a tab or a no-break space, in code, comments, HTML blocks and later sections as well. When the naive reading finds more unticked
// boxes than the structured one, the markup is ambiguous and the verdict is red: a box that this package
// does not read must not turn the check green. A section with no box that it reads is red too, because it
// is the template's section with its markup damaged, or a heading it does not read.
//
// The consequences are the price of failing closed, and the output says what to do about each:
//
//   - The section must be the last one of the description: a box in a section after it is reported.
//   - A copy of the section in a code block, in a comment or in an HTML block is reported, with the line
//     that holds its title; remove the checkboxes of the copy, or the copy.
//   - A box in an indented code block, in an HTML block, in a fence or a comment inside the section, an
//     empty "- [ ]" item, or a Setext heading that ends the section in the renderer, is reported where the
//     renderer shows none.
//
// A description without a line titled "Merge readiness", an empty one and an empty file pass with a
// message. A description with the section twice is judged section by section and is red when any box of
// any of them is unticked. Only ATX headings are read as the heading of the section by the structured
// reading; a section with another form of heading is judged by the naive reading, which is red unless
// every box is ticked and then reports a section with no boxes it reads.
//
// # Output
//
// A verdict is a few lines. A box is printed after the fixed prefix "  - [ ] line N: ", so a line of the
// output never starts with the text of a box; the runner of the hosting service trims leading blanks
// before it looks for a workflow command, so the fixed prefix is what protects, together with the
// sanitizing: every control character in a box's text is replaced with "?" and a text is cut at 200
// characters. At most 50 boxes are listed, then a line "... and N more". A file that cannot be read, or
// is larger than 1 MiB, is a red verdict with a message.
package main
