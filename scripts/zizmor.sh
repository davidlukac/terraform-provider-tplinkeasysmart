#!/bin/sh
# Audits a repository with zizmor (the root given, default: the current directory) and judges what it finds,
# with fixed options and a cleaned environment so that no input the audited tree can carry (a configuration file,
# an ignore file, a comment) changes the result. A thin
# wrapper: the work is done by tools/zizmorrun (Go, tested; its package audit documents the options, the
# variables that are cleared and why, the refusal of symbolic links under .github/ and the exact expectation).
# `make zizmor` calls it, and so do the probes of tools/zizmorprobe.
#
# It needs the go tool. The program is built and run from the repository this script lives in. When the root is
# another tree (as in the probes) no file of that tree (a go.mod, a go.work, a vendor directory) can change which
# program runs; in CI the script lives in the audited tree, so the program, go.mod, go.work and vendor/ of the
# checked-out tree are what runs: the gate's own files are part of the tree it runs in, and a change to any of
# them changes the gate (CONTRIBUTING.md lists what is read at review). The root is made absolute first because
# `go -C` changes the directory.
set -eu

repo=$(cd "$(dirname "$0")/.." && pwd)
root=$(cd "${1:-.}" && pwd)
expected=${ZIZMOR_SELF_REPOSITORY_FINDINGS:?set it to the number of self-repository findings the workflows may carry}
exec go -C "$repo" run ./tools/zizmorrun "$root" "$expected"
