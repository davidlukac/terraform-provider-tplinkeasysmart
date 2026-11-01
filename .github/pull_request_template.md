## What and why

<!-- One paragraph. Link the issue if there is one. -->

## Checklist

Full list: [`CONTRIBUTING.md`](../CONTRIBUTING.md) (client, resource and data source changes: code, tests, documentation).

- [ ] Title follows Conventional Commits (`feat:`, `fix:`, `docs:`, `ci:`, ...)
- [ ] `make pre-commit` passes locally (format, vet, lint, tests with `-race`, coverage policy)
- [ ] `make regression` passes locally (the Terraform scenarios; the pre-commit hook skips them without `TF_ACC=1`)
- [ ] Docs regenerated (`make generate`) and examples validated (`make validate-examples`) when the schema changed
- [ ] `CHANGELOG.md` updated for user-visible changes
- [ ] Writes that could cut management access are guarded and covered by tests (zero writes on a refusal)
- [ ] A regression scenario covers a new or changed setting (`internal/regression`), and the fake enforces the rules measured on hardware
- [ ] A client change is made in the [escp-go](https://github.com/davidlukac/escp-go) library first (released, then required in `go.mod`); its documentation and `PROTOCOL.md` are updated there
- [ ] Hardware and firmware verified on are stated (or "not verified live")
- [ ] No credentials, MAC addresses or real addresses in code, tests or docs
