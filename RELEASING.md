# Releasing

How a version of the provider is cut and published. Maintainers only.

## Conventions

- All changes go through a pull request: branch, commit, push, wait for green CI, **squash merge**. Nothing is pushed to `main` directly.
- Commit messages and PR titles follow [Conventional Commits](https://www.conventionalcommits.org/), which drives the semantic version: `feat:` minor, `fix:` patch, `feat!:` / `BREAKING CHANGE:` major; `docs:`, `ci:`, `test:`, `chore:`, `build(deps):` do not release.
- One new setting is one minor version. `CHANGELOG.md` gets an entry and `main.go` gets the new `version`.

## Cutting a version

1. On a feature branch: bump `var version` in `main.go`, add the `CHANGELOG.md` entry, run `make test lint docs-check validate-examples`.
2. Open the PR, wait for CI, squash merge.
3. On `main`: `git tag -a vX.Y.Z -m vX.Y.Z` and push **that one tag**.

## Publishing to the Terraform Registry

Status: not published yet. The release workflow (`.github/workflows/release.yml`) is manual only (`workflow_dispatch` with a tag input), so pushing a tag does not release anything.

1. Create a GPG signing key and add `GPG_PRIVATE_KEY` and `GPG_PASSPHRASE` as repository secrets; add the public key in the Registry (Publish > Provider).
2. Make the repository public (the Registry ingests public repositories only).
3. Register the provider on registry.terraform.io (sign in with GitHub, Publish > Provider, pick the repository).
4. Release `1.0.0`: bump the version, re-add the `push: tags: ["v*"]` trigger described at the top of `release.yml`, tag and push. The workflow builds every OS/arch, signs the checksums and uploads the assets; the Registry ingests them through its webhook.
5. Optional: submit to the OpenTofu registry with a PR to `opentofu/registry`.

Before the first release re-check the current [Registry publishing requirements](https://developer.hashicorp.com/terraform/registry/providers/publishing), run `make snapshot` and `make lint`. CI runs a snapshot build on every PR as a dry run.
