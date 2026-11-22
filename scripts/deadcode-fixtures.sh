#!/bin/sh
# Holds .golangci-deadcode.yml to its fixture module (devtools/deadcodefixtures): runs the linter with that
# configuration over a copy of the module and compares the findings with the module's expected.txt. Every
# expected finding must be reported and nothing else may be, so that a configuration that no longer finds
# code only a test uses (tests: true, another linter, no linter, a path rule that stopped matching) fails.
# Run it through `make deadcodefixtures`, which passes the pinned linter.
#
#   scripts/deadcode-fixtures.sh <golangci-lint binary>
#
# The copy sits in a temporary directory next to a copy of the configuration: the paths of the exclusions
# are relative to the directory of the configuration file, as they are for the real tree.
set -eu

lint=${1:?usage: deadcode-fixtures.sh <golangci-lint binary>}
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT INT TERM

cp -R "$root/devtools/deadcodefixtures/." "$work/"
cp "$root/.golangci-deadcode.yml" "$work/"
grep -v '^#' "$work/expected.txt" | sort >"$work/want.txt"

cd "$work"
# Exit status 1 is "findings"; anything else (2 and up: a broken configuration, a failed build) is an error.
status=0
GOWORK=off "$lint" run --config .golangci-deadcode.yml --show-stats=false \
	--output.text.path=stdout --output.text.print-issued-lines=false --output.text.colors=false \
	./... >raw.txt 2>err.txt || status=$?
if [ "$status" -gt 1 ]; then
	cat err.txt raw.txt >&2
	echo "deadcode fixtures: the linter did not run (exit status $status)" >&2
	exit 2
fi
# "file:line:col: text (linter)" becomes "file: linter: text", the form of expected.txt.
sed -E 's/^([^:]+):[0-9]+:[0-9]+: (.*) \(([a-z]+)\)$/\1: \3: \2/' raw.txt | sort >got.txt
if ! diff -u want.txt got.txt; then
	echo "deadcode fixtures: .golangci-deadcode.yml does not report what devtools/deadcodefixtures/expected.txt lists (- missing, + unexpected)" >&2
	exit 1
fi
echo "deadcode fixtures: the configuration reports exactly the expected findings"
