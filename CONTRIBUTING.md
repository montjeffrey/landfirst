# Contributing to landfirst

Thanks for looking at landfirst. This guide covers how to run the checks
locally and what a pull request should look like. Please read the
[Code of Conduct](CODE_OF_CONDUCT.md) before participating.

## Supported versions

CI (`.github/workflows/ci.yml`) builds and tests with Go 1.27.3, the version in
`go.mod`, on Ubuntu, macOS and Windows. It also runs the tests on Ubuntu 22.04,
whose git (2.34) lacks `merge-tree --write-tree`, to keep the older conflict
detection path working. Tests create real git repositories, so git must be on
your `PATH`.

## Build and test

These are the commands the CI `test` job runs:

```bash
go vet ./...
go build ./...
go test ./... -count=1
```

The tests commit in fixture repositories, so git needs an identity. CI sets one
with:

```bash
git config --global user.email ci@example.invalid
git config --global user.name CI
git config --global init.defaultBranch main
```

Only set these globally if you are happy to change your own git config; a
throwaway `HOME` works too.

CI also runs `govulncheck` and a `gitleaks` scan over the full history, so do
not commit credentials, even ones you delete later.

## Pull requests

- Branch from `main` and open a pull request against `main`.
- Keep each pull request small and focused on one change. Unrelated cleanups
  belong in their own pull request.
- Add or update tests for any behavior change or bug fix.
- Update the README or `docs/` when user-visible behavior changes.
- CI must pass before a pull request is merged.
- Use clear commit messages. Conventional prefixes such as `feat:`, `fix:`,
  `docs:` and `build:` are welcome.
- Explain what changed and why in the pull request description.

## Reporting bugs and requesting features

Use the issue forms under the Issues tab. Include the output of
`landfirst -version`, your git version and your operating system.

## Security

Do not report security problems in a public issue or pull request. This
repository has no SECURITY.md, so use GitHub's private vulnerability reporting:
open the Security tab and choose "Report a vulnerability".
