## What and why

<!-- One paragraph. Link the issue if there is one. -->

## Checklist

- [ ] Title follows Conventional Commits (`feat:`, `fix:`, `docs:`, `ci:`, ...)
- [ ] `make pre-commit` passes locally (format, vet, lint, tests with `-race`, coverage policy)
- [ ] Docs regenerated (`make generate`) and examples validated (`make validate-examples`) when the schema changed
- [ ] `CHANGELOG.md` updated for user-visible changes
- [ ] Writes that could cut management access are guarded and covered by tests
- [ ] No credentials, MAC addresses or real addresses in code, tests or docs
