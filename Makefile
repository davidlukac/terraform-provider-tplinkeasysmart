.DEFAULT_GOAL := test

# Regenerate docs/ from the schema and examples/ (needs the terraform binary on PATH).
generate:
	go generate ./...

lint:
	golangci-lint run ./...

# Unit tests plus Terraform lifecycle tests against fakes. No hardware is touched.
test:
	go test -race -count=1 ./...

# Same, with the Terraform acceptance harness enabled explicitly (as in CI).
testacc:
	TF_ACC=1 go test -race -count=1 ./internal/provider/...

# Fails when docs/ is out of date with the schema, templates or examples.
docs-check: generate
	git diff --compact-summary --exit-code -- docs

# Known vulnerabilities in the code and its dependencies (needs: go install golang.org/x/vuln/cmd/govulncheck@latest).
vuln:
	govulncheck ./...

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
	golangci-lint run ./...
	$(MAKE) cover

# Local dry-run of the signed release pipeline (no signing, no publishing).
snapshot:
	GPG_FINGERPRINT=none goreleaser release --snapshot --clean --skip=sign,publish

.PHONY: generate lint test testacc docs-check validate-examples vuln snapshot hooks pre-commit

# --- coverage -------------------------------------------------------------
# `make cover` runs the whole suite with the race detector, writes cover.out
# and enforces the policy: every statement covered except functions whose doc
# comment says `// coverage: ignore - <reason>` (see README, "Testing and coverage").
.PHONY: cover cover-html cover-selftest

cover: cover-selftest
	go test ./... -race -count=1 -coverpkg=./... -coverprofile=cover.out
	python3 scripts/covercheck.py cover.out .

cover-html: cover
	go tool cover -html=cover.out -o coverage.html

cover-selftest:
	cd scripts && python3 -m unittest test_covercheck test_coverbadge
