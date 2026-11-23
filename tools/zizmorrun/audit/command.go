// Package audit runs zizmor, the audit of the workflow files, over a repository the way `make zizmor` needs
// it, and judges the answer. It is the library part of the command tools/zizmorrun, a build tool of this
// repository.
//
// zizmor reads some inputs from the tree it audits (a configuration file, ignore files, inline suppression
// comments, the files it can load, the files links lead to), so the result could depend on files in the pull
// request itself. The command line and the environment are therefore built here, in one place, as plain values
// ([Build]) that the tests compare whole:
//
//   - fixed options: no configuration file is read (--no-config), every workflow is collected whatever an ignore
//     file says (--collect=all), a file that cannot be loaded fails the run instead of being skipped with a
//     warning (--strict-collection), ignore comments change nothing (--no-ignores), a finding is not a failure
//     of the tool (--no-exit-codes: the verdict is below, a tool failure still is one), and the format is named
//     (json-v1, which is also what `json` means in zizmor 1.32.0, so that a change of the default is not read
//     as a different answer). The whole repository is the input, so a composite action is audited wherever it is;
//   - a symbolic link under .github/ is refused: it would make the files audited depend on where it points;
//   - every environment variable that changes what zizmor audits, or the host its token is sent to, is removed (see
//     [cleared]), then GH_TOKEN is set from GH_TOKEN or GITHUB_TOKEN (an empty variable counts as no token); with
//     a token the audits that look up an action's history run online, without one the run is --offline;
//   - what zizmor writes on its standard error is untrusted text (it names the files it audits, and a file name can
//     hold a new line and a workflow command): every line is forwarded as "zizmor: " and the line escaped by
//     verdict.Escape (printable ASCII, at most 200 characters), at most 200 lines, so no line of the job log starts
//     with "::";
//   - the findings are judged by package verdict: exactly the expected number of findings of the `self-repository`
//     rule and nothing else. The program that judges them is built from the repository the script lives in
//     (scripts/zizmor.sh runs it with `go -C`). In CI that is the checked-out pull request, which is also the tree
//     that is audited, so this package, package verdict, the probes, go.mod, go.work and vendor/ are part of what
//     the gate runs on: a change to any of them changes the gate, and CONTRIBUTING.md lists what is read at review.
//
// The proxy variables of zizmor's HTTP client (HTTPS_PROXY, https_proxy and ALL_PROXY) are not cleared. They come from
// the machine or from an `env:` of a workflow, which is read at review (devtools/recipecheck compares the `env:`
// lines of the two zizmor steps: a tripwire for an edit of those lines and no more). They change only the route the
// requests take and not what is audited, and clearing them would break a run behind a proxy.
//
// Options that are not passed keep their default: --persona, --min-severity and --min-confidence have no
// environment variable and no configuration file to change them (--no-config); --fix, --lsp, --completions,
// --thanks and --cache-dir do not change what is audited.
package audit

import (
	"slices"
	"strings"
)

// Names used on the command line and in the environment.
const (
	// program is the name of the tool, found on the PATH.
	program = "zizmor"
	// tokenVariable is the variable the tool reads its token from; fallbackTokenVariable is the one a GitHub Actions
	// job sets.
	tokenVariable         = "GH_TOKEN"
	fallbackTokenVariable = "GITHUB_TOKEN" //nolint:gosec // G101: the name of a variable, not a credential
	// formatName is the output format the verdict reads.
	formatName = "json-v1"
)

// cleared are the environment variables removed from the environment of zizmor 1.32.0, so that a variable set
// by a workflow step or by the machine cannot narrow the audit. From `zizmor --help` and the binary:
//
//	ZIZMOR_OFFLINE, ZIZMOR_NO_ONLINE_AUDITS  would turn the online audits off silently.
//	ZIZMOR_CONFIG                            would name a configuration file (it conflicts with --no-config,
//	                                         which is passed).
//	GH_HOST                                  would point the API calls, and the token, at another host.
//	ZIZMOR_GITHUB_TOKEN, GH_TOKEN, GITHUB_TOKEN  the token: removed, then GH_TOKEN is set from the variables
//	                                         above (online) or the run is --offline.
//
// These shape the plain output and the log only (the verdict reads --format json-v1), or the cache and the git
// configuration, so they cannot matter and are left alone: ZIZMOR_RENDER_LINKS, ZIZMOR_SHOW_AUDIT_URLS, RUST_LOG,
// NO_COLOR and the other color variables; HOME, XDG_CACHE_HOME, XDG_CONFIG_HOME and GIT_CONFIG_* (where the HTTP
// cache and the git configuration are: the ignore files are not honored (--collect=all) and a stale cache entry is
// the same answer GitHub gave before).
var cleared = []string{
	"ZIZMOR_OFFLINE", "ZIZMOR_NO_ONLINE_AUDITS", "ZIZMOR_CONFIG", "GH_HOST",
	"ZIZMOR_GITHUB_TOKEN", tokenVariable, fallbackTokenVariable,
}

// Command is a program to run: its name, its arguments and its whole environment, as plain values.
type Command struct {
	Name string
	Args []string
	Env  []string
}

// Token is the token to audit with: the value of GH_TOKEN, or when that is empty or unset the value of
// GITHUB_TOKEN, or "" when neither holds one. getenv is the lookup of the environment.
func Token(getenv func(string) string) string {
	if token := getenv(tokenVariable); token != "" {
		return token
	}
	return getenv(fallbackTokenVariable)
}

// Build is the command that audits the current directory: the fixed options, offline when token is empty, and
// the environment environ without the variables in cleared and, when there is a token, with GH_TOKEN set to it.
func Build(token string, environ []string) Command {
	args := []string{}
	if token == "" {
		args = append(args, "--offline")
	}
	args = append(args,
		"--no-config", "--collect=all", "--strict-collection", "--no-progress",
		"--no-ignores", "--no-exit-codes", "--format", formatName,
		".",
	)
	env := make([]string, 0, len(environ)+1)
	for _, entry := range environ {
		if !isCleared(entry) {
			env = append(env, entry)
		}
	}
	if token != "" {
		env = append(env, tokenVariable+"="+token)
	}
	return Command{Name: program, Args: args, Env: env}
}

// isCleared reports whether the entry ("NAME=value", or a bare "NAME") sets a variable in cleared. A name is
// compared whole, and case-sensitively as the environment of a POSIX system is.
func isCleared(entry string) bool {
	name, _, _ := strings.Cut(entry, "=")
	return slices.Contains(cleared, name)
}
