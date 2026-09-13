# Contributing

Thanks for helping improve Visual Tmux Client.

## Development setup

Install Go 1.26.3+, Node.js 24+, npm, and tmux 3.3+. Then run:

```sh
make test
make lint
make build
./hub/visual-tmux-client
```

The browser UI is under `hub/web`. For frontend development with hot reload, run `npm run dev` there. API requests still need a running Go server or an appropriate local proxy. Run `npm test` in `hub/web` to execute the web test suite.

## Code Quality and Linter

This project enforces strict zero-tolerance linting via `golangci-lint` pinned to version `v2.13.2`.

### Installation

Install the pinned `golangci-lint` binary:

```sh
make install-lint
# or: ./scripts/install-golangci-lint.sh
```

### Checks and Formatting

Before submitting a PR, ensure all checks pass:

```sh
make lint    # Verifies .golangci.yml and runs full linter suite
make fmt     # Applies project formatting rules (gofmt, goimports)
make test    # Runs web tests, Go test suite, and go vet
```

### Linter Rules and Thresholds

Rules are defined in root `.golangci.yml`:
- **`lll`**: Maximum 120 characters per line (tab-width: 4), applicable to all Go source and test files.
- **`funlen`**: Maximum 80 lines and 50 statements per function (excluding comments).
- **`gocyclo`**: Cyclomatic complexity threshold is 15.
- **`mnd`**: Magic numbers in business logic are disallowed. Named semantic constants must be defined. Excluded for test files and `internal/config/defaults.go`.
- **`goconst`**: Repeated strings of length >= 3 occurring 3 or more times require a shared constant.
- **`errcheck`**: Unhandled errors and discarding via blank identifier `_` are strictly checked (`check-blank: true`).
- **`errorlint`**: Strict error wrapping and comparison checks.
- **`nolintlint`**: Any `//nolint` directive must specify the exact rule and include an explanation comment (`//nolint:rule // reason`). Broad suppressions are rejected.

## Pull requests

- Keep each change focused and explain its user-visible impact.
- Add or update tests for behavior changes.
- Ensure `make lint` reports `0 issues` and `make test` passes completely.
- Never commit tokens, `.env` files, `.mcp.json`, or other machine-local configuration.
- Update the README or OpenSpec specifications when public behavior changes.

By contributing, you agree that your contribution is licensed under the MIT License.
