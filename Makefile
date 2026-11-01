.DEFAULT_GOAL := test

# Development tools (the linter, the vulnerability scanner) are pinned in devtools/go.mod, a module of
# their own so that their dependencies stay out of this module's go.mod; Dependabot bumps them there.
# `go tool` builds the pinned version on first use (cached afterwards): nothing to install by hand.
DEVTOOL := go tool -modfile=devtools/go.mod

# Regenerate docs/ from the schema and examples/ (needs the terraform binary on PATH).
generate:
	go generate ./...

lint:
	$(DEVTOOL) golangci-lint run ./...

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

# What the pre-commit hook runs: format, vet, lint, then the whole suite with the
# race detector and the coverage policy. Same checks CI enforces.
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

.PHONY: compat-table regression regression-live generate lint test testacc docs-check validate-examples vuln snapshot hooks pre-commit

# --- coverage -------------------------------------------------------------
# `make cover` runs the whole suite with the race detector, writes cover.out
# and enforces the policy: every statement covered except functions whose doc
# comment says `// coverage: ignore - <reason>` (see README, "Testing and coverage").
.PHONY: cover cover-html

# The policy checker is Go (tools/coverpolicy) and is tested, and measured, by the same run.
cover:
	go test ./... -race -count=1 -coverpkg=./... -coverprofile=cover.out
	go run ./tools/coverpolicy check cover.out .

cover-html: cover
	go tool cover -html=cover.out -o coverage.html
