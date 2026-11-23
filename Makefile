.DEFAULT_GOAL := test

# Development tools (the linter, the vulnerability scanner) are pinned in devtools/go.mod, a module of
# their own so that their dependencies stay out of this module's go.mod; Dependabot bumps them there.
# `go tool` builds the pinned version on first use (cached afterwards): nothing to install by hand.
# GOWORK=off: the tools refuse to run in workspace mode (see `make work`).
DEVTOOL := GOWORK=off go tool -modfile=devtools/go.mod

# Regenerate docs/ from the schema and examples/ (needs the terraform binary on PATH).
generate:
	go generate ./...

# American English in every tracked text file, not only the Go comments the misspell linter reads: the
# Markdown, templates, YAML, shell and the Makefile too. The same word list as the linter (misspell, locale US,
# pinned in devtools). The changelog quotes the past and is left as it was.
# That word list lacks some verbs in their British -ise form (it knows most, not all), so a second pass looks
# for the forms of the ones found missing, as stems written so that this file does not contain the words it
# forbids: add a stem here when a British word gets through.
SPELL_FILES = '*.go' '*.md' '*.yml' '*.yaml' '*.json' '*.sh' '*.tmpl' '*.tf' Makefile ':!:CHANGELOG.md' ':!:go.sum'
BRITISH_VERBS = (normali|generali|speciali|capitali|canonicali|parametri|tokeni)(s|se|sed|ses|sing|sation|sations)

spell:
	@git ls-files -z -- $(SPELL_FILES) \
		| xargs -0 env $(DEVTOOL) misspell -locale US -error
	@hits=$$(git ls-files -z -- $(SPELL_FILES) | xargs -0 grep -nIiEw '$(BRITISH_VERBS)' /dev/null || true); \
		if [ -n "$$hits" ]; then echo "$$hits"; echo "British spelling: use the -ize form (American English)"; exit 1; fi

# The names the defect catalog gives to its checks (a test, a make target, a linter, an analyzer, a
# workflow) all exist: a renamed test or a removed target fails here (devtools/catalogcheck, a copy of
# escp-go's; it exits 1 with one line per problem). The ids of the catalog are the P classes; the
# links to the library's classes are checked for their shape only.
.PHONY: catalogcheck
lint: catalogcheck

catalogcheck:
	GOWORK=off go -C devtools build -o $(CURDIR)/build/catalogcheck ./catalogcheck
	build/catalogcheck -root . -catalog contributing/defect-classes.md -ids P

lint: spell
	GOWORK=off go -C devtools build -o $(CURDIR)/build/subtests ./subtests
	build/subtests ./...
	$(MAKE) analyzers
	$(DEVTOOL) golangci-lint run ./...
	$(DEVTOOL) golangci-lint run --build-tags live ./...
	@bin="$$($(DEVTOOL) -n golangci-lint)"; for os in linux windows; do echo "golangci-lint run (GOOS=$$os)"; GOOS=$$os "$$bin" run ./... || exit 1; done

# The project's own checks beyond golangci-lint (devtools/analyzers, copies of escp-go's): readwrite (a
# function that reads a setting and then writes one), lockwait (a blocking call while a lock is held),
# validatecall (an exported function that takes a value with a Validate method and never calls it) and
# nosleep (a time.Sleep, or in a test a time.After that is not a failure bound, without a `// nosleep: <reason>`).
# The flags are this repository's names for what the checks look for; the sources are never edited here.
#   receivers  only a call on a type of this module or of the library counts: the clients the resources
#              and the regression suite are handed (SwitchClient and the small interfaces each declares),
#              so that framework calls with the same names (state.SetAttribute, set.Ports()) are not reads
#              or writes of the switch.
#   readers    the SwitchClient methods that read a setting; writers: the ones that change one.
#   guards     the provider holds no lock across a read and a write (the library serializes each call and
#              guards its own read-modify-write), so no name opens a critical section and a function that
#              reads and then writes says why in a `// readwrite: <reason>` comment.
#   lockfuncs  empty: the provider takes its locks with sync.Mutex directly, there is no lock wrapper.
#   validatecall.packages  the packages of this module (the check's default matches only the library's root
#              package). The provider validates in ValidateConfig and schema validators, and its exported
#              functions take no library value that has a Validate method, so the check is silent here; it
#              stays on so that a new exported function that takes one and skips Validate is reported.
#   nosleep    has no flag and runs with the others; it is also run alone with the `live` build tag, so a file
#              behind that tag cannot sleep unseen (today the module has no file behind it).
ANALYZER_FLAGS = \
	-readwrite.receivers='terraform-provider-tplinkeasysmart/internal/|escp-go\.' \
	-readwrite.readers='^(Info|LED|Ports|PortStats|LAGs|IGMP|LoopPrevention|PortMirror|MTUVLAN|PortBasedVLAN|Dot1Q|VLANMode|QoS|Bandwidth|StormControl|System|ConfigBackup)$$' \
	-readwrite.writers='^(Set|Delete|Enable)[A-Z]\w*$$' \
	-readwrite.guards='^no-modify-guard-in-the-provider$$' \
	-lockwait.lockfuncs= \
	-validatecall.packages='^github\.com/davidlukac/terraform-provider-tplinkeasysmart(/.*)?$$'

analyzers:
	GOWORK=off go -C devtools build -o $(CURDIR)/build/analyzers ./analyzers
	build/analyzers $(ANALYZER_FLAGS) ./...
	GOFLAGS=-tags=live build/analyzers -nosleep ./...

# Code that only a test uses. `make lint` runs `unused` with the tests on (.golangci.yml), so a function a test
# calls counts as used and ships in the production build for nothing. This runs `unused` alone with the tests
# left out (.golangci-deadcode.yml: `run.tests: false`), with and without the `live` build tag, and fails on any
# finding; internal/regression, the harness, is left out by a path rule of that file. A prerequisite of `lint`.
# `make deadcodefixtures` holds the configuration to a fixture module (devtools/deadcodefixtures: a function
# only a test calls must be reported, one in the harness directory must not), so that a configuration weakened
# is noticed; it is a prerequisite of `lint` too. devtools/lintcheck is a copy made by hand of escp-go's, which is
# its one source: a change to it is made there first. The fixture holds the linter set, `tests: false` and that the
# path rule reaches internal/regression and does not reach internal/provider or the module root. The pin of the `lint:`
# prerequisites and of the `deadcode` recipe is a proposed check (devtools/recipecheck, #196): until it is built a
# change to the `lint:` line or to those recipes is read at review; `catalogcheck` only checks that the targets exist.
.PHONY: deadcode deadcodefixtures
lint: deadcode deadcodefixtures

deadcode:
	$(DEVTOOL) golangci-lint run --config .golangci-deadcode.yml ./...
	$(DEVTOOL) golangci-lint run --config .golangci-deadcode.yml --build-tags live ./...

deadcodefixtures:
	GOWORK=off go -C devtools build -o $(CURDIR)/build/lintcheck ./lintcheck
	build/lintcheck -root . -lint "$$($(DEVTOOL) -n golangci-lint)" -config .golangci-deadcode.yml -fixtures devtools/deadcodefixtures -linters ""

# The workflow files (a pinned actionlint; the pre-commit hook runs it for a staged workflow, CI for every PR).
actionlint:
	$(DEVTOOL) actionlint

# The security of the workflow files, with zizmor at its default severity and persona (what a workflow
# may do and how untrusted input reaches it; CONTRIBUTING.md). It is a Python program, not a Go tool, so
# it is not in devtools/go.mod: the version is pinned, with hashes, in devtools/zizmor/requirements.txt, which
# CI installs and Dependabot bumps. tools/zizmorrun (a Go program, started by scripts/zizmor.sh)
# builds the command line, with fixed options and a cleaned environment so that no input the audited tree
# can carry (a configuration file, an ignore file, a comment) changes the result; the gate's own files are
# part of that tree (CONTRIBUTING.md). It runs online (the audits that look up an action's history) when a
# GitHub token is in GH_TOKEN or GITHUB_TOKEN, offline otherwise. Not part of the pre-commit hook.
#
# The findings are an exact allow-list: zizmor is run with --no-ignores (an ignore comment changes
# nothing) and the run passes only when it reports this many `self-repository` findings (one per use of
# the local setup action, which the workflows write as `./.github/actions/setup`) and nothing else. The
# rule is counted, not located: a use of another local action that the rule also reports counts the same,
# and is audited itself because the whole repository is the input. A new finding, a new use or a
# removed one is a deliberate edit of this number in the same pull request. The answer (--format json-v1)
# is read by the same program (package tools/zizmorverdict/verdict), which fails closed on anything it
# does not understand. The number goes away, with the comments on those uses, when actionlint accepts the
# `$/` form of a local action (rhysd/actionlint#711).
ZIZMOR_SELF_REPOSITORY_FINDINGS := 13

.PHONY: zizmor
zizmor:
	@command -v zizmor >/dev/null 2>&1 || { \
		echo "zizmor is not installed. The pinned version, with hashes:"; \
		echo "  python3 -m venv ~/.cache/provider-zizmor && ~/.cache/provider-zizmor/bin/pip install --require-hashes -r devtools/zizmor/requirements.txt"; \
		echo "then put ~/.cache/provider-zizmor/bin on PATH."; \
		exit 1; }
	@pin="$$(sed -n 's/^zizmor==\([0-9][0-9.]*\).*/\1/p' devtools/zizmor/requirements.txt)"; \
	have="$$(zizmor --version | awk '{print $$2}')"; \
	[ "$$have" = "$$pin" ] || echo "warning: zizmor $$have is installed, devtools/zizmor/requirements.txt pins $$pin"
	@ZIZMOR_SELF_REPOSITORY_FINDINGS=$(ZIZMOR_SELF_REPOSITORY_FINDINGS) scripts/zizmor.sh

# scripts/zizmor.sh, the command `make zizmor` runs (a few lines that start tools/zizmorrun), still reports
# a deliberate finding next to each of these inputs of the audited tree: a configuration file, an ignore
# file, an extra inline suppression, a file zizmor cannot load, a link; and a stub zizmor records the command
# line and the environment (tools/zizmorprobe, a Go program, offline). It needs the pinned zizmor: without it
# the check says so and passes, and the `zizmor` job of test.yml, which installs it, sets ZIZMOR_REQUIRED so that
# it cannot be skipped there (the job of weekly.yml runs `make zizmor` only; CONTRIBUTING.md says what each job
# runs). A prerequisite of `lint`.
.PHONY: zizmorcheck
lint: zizmorcheck

zizmorcheck:
	ZIZMOR_SELF_REPOSITORY_FINDINGS=$(ZIZMOR_SELF_REPOSITORY_FINDINGS) go run ./tools/zizmorprobe

# The module files: tidy would change nothing (this module and devtools), and every downloaded
# dependency matches go.sum.
modcheck:
	GOWORK=off go mod tidy -diff
	GOWORK=off go -C devtools mod tidy -diff
	GOWORK=off go mod verify
	GOWORK=off go -C devtools mod verify

# Every fuzz target for SECONDS (default 30) each; the seed corpora run in `make test` anyway. A failing
# input is written to the package's testdata/fuzz: commit it with the fix. Go fuzzes one target at a time, so
# a new target is a new line. The minimize time is short because a new input is minimized before the run
# goes on (the default is a minute).
fuzz:
	go test ./internal/regression -run '^$$' -fuzz '^FuzzParseEnvFile$$' -fuzztime $(or $(SECONDS),30)s -fuzzminimizetime 5s
	go test ./tools/mergereadiness -run '^$$' -fuzz '^FuzzJudge$$' -fuzztime $(or $(SECONDS),30)s -fuzzminimizetime 5s
	go test ./tools/zizmorverdict/verdict -run '^$$' -fuzz '^FuzzJudge$$' -fuzztime $(or $(SECONDS),30)s -fuzzminimizetime 5s
	go test ./tools/zizmorverdict/verdict -run '^$$' -fuzz '^FuzzParseExpected$$' -fuzztime $(or $(SECONDS),30)s -fuzzminimizetime 5s
	go test ./tools/zizmorverdict/verdict -run '^$$' -fuzz '^FuzzEscape$$' -fuzztime $(or $(SECONDS),30)s -fuzzminimizetime 5s
	go test ./tools/zizmorrun/audit -run '^$$' -fuzz '^FuzzLineEscaper$$' -fuzztime $(or $(SECONDS),30)s -fuzzminimizetime 5s

# Records GitHub's own rendering of the descriptions in tools/mergereadiness/testdata/oracle
# (NN-name.md -> NN-name.html), the ground truth the tests of the "Merge readiness" check compare the
# parser with (it must report at least as many unticked boxes as the renderer shows). A copy of the target
# of the same name in escp-go's Makefile. It needs `gh auth` and the network, posts nothing (the markdown
# endpoint only renders), and is not run in CI: run it when a description is added or GitHub's renderer
# changes, and commit the HTML that changed. The renderer puts a random hash in the ids of a footnote; it
# is cut, so that a re-recording is identical.
.PHONY: oracle-refresh
oracle-refresh:
	@for f in tools/mergereadiness/testdata/oracle/*.md; do \
		gh api -X POST /markdown -F text=@"$$f" -f mode=gfm > "$${f%.md}.html.tmp" || exit 1; \
		sed -E 's/-[0-9a-f]{32}"/"/g' "$${f%.md}.html.tmp" > "$${f%.md}.html"; rm -f "$${f%.md}.html.tmp"; \
	done

# Unit tests plus Terraform lifecycle tests against fakes. No hardware is touched.
test:
	go test -race -count=1 ./...

# Same, with the Terraform acceptance harness enabled explicitly (as in CI); includes the regression
# scenarios against the stateful fake, so every Terraform version in the matrix runs them.
testacc:
	TF_ACC=1 go test -race -count=1 ./internal/provider/... ./internal/regression/...

# Fails when docs/ is out of date with the schema, templates or examples.
docs-check: generate
	git diff --compact-summary --exit-code -- docs

# Known vulnerabilities in the code and its dependencies.
vuln:
	$(DEVTOOL) govulncheck ./...

# Validates every documentation example against the schemas (no switch needed).
validate-examples:
	scripts/validate-examples.sh

# Enable the repository's git hooks (pre-commit runs `make pre-commit`).
hooks:
	git config core.hooksPath .githooks
	@echo "git hooks enabled: .githooks/pre-commit"

# Develop against a checkout of the ESCP library (github.com/davidlukac/escp-go, ../escp-go; override
# with ESCP_DIR) instead of the version go.mod requires: `make work` writes a go.work that uses both
# modules, `make unwork` removes it. The file is not committed and CI never sees it. A change that
# needs a library change is committed against a *released* library version: the pre-commit hook
# builds and tests without the workspace, as CI does.
ESCP_DIR ?= ../escp-go

work:
	@test -d "$(ESCP_DIR)" || { echo "$(ESCP_DIR) is not the escp-go checkout (set ESCP_DIR)"; exit 1; }
	@rm -f go.work go.work.sum
	go work init . $(ESCP_DIR)
	@echo "workspace on: go build and go test, and so every build of the provider binary, now use $(ESCP_DIR); make unwork to stop"

unwork:
	rm -f go.work go.work.sum

# What the pre-commit hook runs: format, vet, lint, then the whole suite with the
# race detector and the coverage policy. Same checks CI enforces. Without a local workspace
# (GOWORK=off), because CI has none: it builds against the library version go.mod requires.
pre-commit: export GOWORK = off
pre-commit:
	@test -z "$$(gofmt -l .)" || { echo "gofmt needed on:"; gofmt -l .; exit 1; }
	go vet ./...
	go vet -tags live ./...
	$(MAKE) lint
	$(MAKE) cover

# Local dry-run of the signed release pipeline (no signing, no publishing).
snapshot:
	GPG_FINGERPRINT=none goreleaser release --snapshot --clean --skip=sign,publish

# The regression suite (internal/regression) through a real Terraform binary against the stateful
# fake switch: nothing to configure, runs in CI. `make regression-live` aims the same scenarios at
# switch 03 and needs the gitignored .test.env (see .test.env.example); it refuses without it.
regression:
	TF_ACC=1 go test -count=1 -race ./internal/regression/...

regression-live:
	@test -f .test.env || { echo ".test.env is missing: copy .test.env.example and fill it in (never commit it)"; exit 1; }
	@test "$(CONFIRM)" = 1 || { echo "this writes to the real switch named in .test.env: rerun with CONFIRM=1"; exit 1; }
	TPLINK_REGRESSION_ENV_FILE=$(CURDIR)/.test.env TF_ACC=1 go test -count=1 -timeout 60m -v ./internal/regression/...

# Rewrites the compatibility table in README.md from the table of the ESCP library the provider is built
# with (github.com/davidlukac/escp-go, package safety; the firmware allow-list reads the same table);
# a test fails when the README is stale.
compat-table:
	go test ./internal/provider -run TestReadmeCompatibilityTable -update-readme

.PHONY: work unwork spell analyzers fuzz actionlint modcheck compat-table regression regression-live generate lint test testacc docs-check validate-examples vuln snapshot hooks pre-commit

# --- coverage -------------------------------------------------------------
# `make cover` runs the whole suite with the race detector, writes cover.out
# and enforces the policy: every statement covered except functions whose doc
# comment says `// coverage: ignore - <reason>` (see README, "Testing and coverage").
.PHONY: cover cover-html

# The policy checker is Go (tools/coverpolicy) and is tested, and measured, by the same run.
cover:
	go test ./... -race -count=1 -coverpkg=./... -coverprofile=cover.out
	GOWORK=off go -C devtools test -count=1 ./...
	go run ./tools/coverpolicy check cover.out .

cover-html: cover
	go tool cover -html=cover.out -o coverage.html

# --- mutation testing -----------------------------------------------------
# gremlins (pinned in devtools/go.mod) flips the conditionals, boundaries and arithmetic of the code
# one at a time and the tests of its package must fail for each: coverage says a line ran, a surviving
# mutant says no test would notice if it were wrong. Left out: the programs under tools/ and main.go
# (gremlins reports every mutant in a `package main` as surviving whatever the tests do).
#
# The tests of internal/provider run Terraform lifecycles and take about two minutes, and gremlins runs
# the whole package once per mutant, so the module cannot be mutated as a whole (about 400 mutants):
#   make mutation        the packages with fast tests (internal/regression, about a minute); run weekly in CI.
#                        EFFICACY=90 fails below that share of caught mutants, WORKERS=n sets the parallelism.
#   make mutation-diff   only the lines changed since BASE (default origin/main), in any package; what a pull
#                        request runs when the change is small. Count on about a minute per changed line.
# The report is build/mutation.json.
.PHONY: mutation mutation-diff

# gremlins does not create the directory of its report. GOFLAGS=-count=1: gremlins sizes the timeout of a
# mutant from the time of its first test run, and a cached result takes no time, so without it every
# mutant of a package whose tests ran before "times out" (seen: 59 of 63).
mutate = @mkdir -p build && GOFLAGS=-count=1 $(DEVTOOL) gremlins unleash $(1) --workers $(or $(WORKERS),4) --coverpkg $(2) \
	--output build/mutation.json -E '^tools/' -E '^main\.go$$'

mutation:
	$(call mutate,./internal/regression,./internal/regression/...) $(if $(EFFICACY),--threshold-efficacy $(EFFICACY))

mutation-diff:
	$(call mutate,.,./...) --diff $(or $(BASE),origin/main)
