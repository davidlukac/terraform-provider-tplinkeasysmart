# Releasing

How a version of the provider is cut and published. Maintainers only.

## Conventions

- All changes go through a pull request: branch, commit, push, wait for green CI, **squash merge**. Nothing is pushed to `main` directly.
- Commit messages and PR titles follow [Conventional Commits](https://www.conventionalcommits.org/), which drives the semantic version: `feat:` minor, `fix:` patch, `feat!:` / `BREAKING CHANGE:` major; `docs:`, `ci:`, `test:`, `chore:`, `build(deps):` do not release.
- One new setting is one minor version. `CHANGELOG.md` gets an entry and `main.go` gets the new `version`.
- **Which kind of version:** a new setting or resource is a minor version (`feat:`). A bug fix is a patch (`fix:`). Internal changes (`refactor:`, `docs:`, `test:`, `ci:`, `chore:`) do not release by themselves: they ride along in the next version. When enough of them have piled up to be worth a version, cut a patch whose PR only bumps `main.go` and adds a `CHANGELOG.md` line saying what changed for users (for an internal restructure: "no change in behavior"), titled `chore: ...`. A release never claims more than it contains. A minor version can also be chosen for fixes that change what users see (a new plan-time refusal, new error texts) even though no setting is added (v0.29.0): the release PR is then titled `feat:` on purpose, and the CHANGELOG entry lists every visible change, taken from the diff (`git diff vX.Y.Z..HEAD`), not from memory.

## Cutting a version

1. On a feature branch: bump `var version` in `main.go`, add the `CHANGELOG.md` entry, run `make test lint docs-check validate-examples`. For a minor version with more than about ten fixes, hold an integration review before the release PR: build and test the provider against the library candidate (`make work`) and read the combined diff of the release as one change ([DC-C04](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c04)). For a release that touches how a setting is written to the switch, also run the regression suite against the test switch (`make regression-live CONFIRM=1`, see the README; it needs hardware and is never part of CI) and attach its report to the PR.
2. Open the PR, wait for CI. Before the tag is pushed, read the release's changes together, as one change ([DC-C04](https://github.com/davidlukac/escp-go/blob/main/docs/defect-classes.md#dc-c04)), and run the adversarial pass over the release's diff ([contributing/adversarial-review.md](contributing/adversarial-review.md) and Part 3 of the [library's](https://github.com/davidlukac/escp-go/blob/main/docs/adversarial-review.md)). Then squash merge.
3. On `main`, once the release PR is merged and pulled: check that `main.go` and the `CHANGELOG.md` entry both say `X.Y.Z`, then `git tag -a vX.Y.Z -m vX.Y.Z` and push **that one tag**.

## Publishing to the Terraform Registry

Status: **published** at <https://registry.terraform.io/providers/davidlukac/tplinkeasysmart>. The first version on the Registry is 0.26.1 (2026-10-12), released with the manual workflow. The Registry ingests every GitHub release that has the signed `SHA256SUMS` and the manifest, through its webhook.

The release workflow (`.github/workflows/release.yml`) is manual only (`workflow_dispatch` with a tag input), so pushing a tag does not release anything. To publish a version:

1. Cut the version as above (the PR bumps `main.go` and moves the `CHANGELOG.md` entries under the new version; the GitHub release notes are that CHANGELOG section).
2. Push the tag, then dispatch the workflow: `gh workflow run release.yml -f tag=vX.Y.Z`. It builds every OS/arch, signs the checksums with the release key (`GPG_PRIVATE_KEY`, `GPG_PASSPHRASE` secrets; the public key is registered in the Registry) and uploads the assets.
3. Check the new version on the Registry page and in an empty directory with `terraform init` and `tofu init` (OpenTofu: `required_providers` with `source = "davidlukac/tplinkeasysmart"` and the version pinned): both print `Installed davidlukac/tplinkeasysmart vX.Y.Z (... key ID 41A5C36234739359)`. The workflow `registry-check.yml` does the same for both tools after the Release workflow, weekly, and on demand (`gh workflow run registry-check.yml -f version=X.Y.Z`), retrying for an hour because the registries ingest at their own pace; it reports and never blocks a release, and warns when the signing key has less than 90 days to its expiry (2028-10-11, rotate it before).

The provider is also on the OpenTofu registry (`registry.opentofu.org`), which builds from the same GitHub releases; the signing key is registered there (`opentofu/registry#6347`, merged in PR #6348), and a new version appears with the registry's own sync, not through a step of ours.

Registry versions cannot be deleted by the maintainer, only deprecated, so verify a build (`make snapshot`) before releasing. CI runs a snapshot build on every PR as a dry run.
