// Package safety holds the rules that decide whether a write to a switch is
// prudent: which firmware builds the client was verified against, and (later)
// which ports are in use. It has no Terraform types and no logging; it reads
// from a switch only through the small interfaces it is given, and reports
// what it finds as typed errors and values so each front end (the provider,
// a command line) words the refusal in its own vocabulary.
package safety
