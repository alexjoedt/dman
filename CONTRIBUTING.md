# Contributing to dman

Thanks for your interest in contributing! This document describes how to set up
your environment, the conventions the project follows, and how to submit changes.

## Development setup

dman is a standard Go module. You need:

- Go (see the version pinned in [`go.mod`](go.mod))
- [Task](https://taskfile.dev) for the common workflows (optional but recommended)

Clone the repository and verify the build:

```bash
git clone https://github.com/alexjoedt/dman.git
cd dman
task build   # builds ./bin/dman
task test    # runs the test suite with coverage
```

If you prefer plain `go`:

```bash
go build ./...
go test --cover ./...
```

## Before you open a pull request

Please make sure the following pass locally:

```bash
go build ./...
go vet ./...
gofmt -l .        # should print nothing
go test ./...
```

The README contains an auto-validated command matrix. If you add, remove, or
change a CLI command or flag, update the table between the
`<!-- COMMAND_MATRIX_START -->` and `<!-- COMMAND_MATRIX_END -->` markers in
[`README.md`](README.md). The `TestReadmeCommandMatrixMatchesCLI` test enforces
that it stays in sync.

## Commit messages

This project uses [Conventional Commits](https://www.conventionalcommits.org/).
Release notes are generated automatically from commit messages, so a clear,
consistent history matters. Examples:

- `feat: add snapshot prune command`
- `fix: handle missing profile directory on apply`
- `docs: clarify overlay model`
- `test: cover git clone error path`
- `chore: bump dependencies`

## Pull request guidelines

- Keep changes focused; one logical change per pull request.
- Add or update tests for behavior changes.
- Update documentation (README, command matrix) when user-facing behavior changes.
- Describe what changed and why in the pull request description.

## Reporting bugs and requesting features

Please open an issue with as much detail as possible: what you expected, what
happened, your OS, and the dman version (`dman version`).

## License

By contributing, you agree that your contributions will be licensed under the
[MIT License](LICENSE).
