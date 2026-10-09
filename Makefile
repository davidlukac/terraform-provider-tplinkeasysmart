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
	cd scripts && python3 -m unittest test_covercheck
