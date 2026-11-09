# Contributing

Thanks for helping improve Visual Tmux Client.

## Development setup

**Do not develop, test, or debug this project from inside a tmux session,
including when using Codex or Claude Code.** Open a separate terminal window
or tab outside tmux and start the agent there. An accidental tmux shutdown
can otherwise terminate the terminal hosting your development work. Clearing
`TMUX` does not move a process outside its session, and an agent tool may hide
that variable even while its parent is running inside tmux.

This precaution does not isolate commands from other tmux servers on the
machine. The test isolation requirements under **Tmux safety**
remain required. Development continues to use native macOS or Linux tools;
no container runtime is required.

Install Go 1.26.3+, Node.js 24+, npm, and tmux 3.3+. Then run:

```sh
make test
make lint
make build
./hub/visual-tmux-client
```

The browser UI is under `hub/web`. For frontend development with hot reload, run `npm run dev` there. API requests still need a running Go server or an appropriate local proxy. Run `npm test` in `hub/web` to execute the web test suite.

## Tmux safety

Even outside tmux, an unqualified command can reach an existing server.
On tmux 3.7b, explicit `-S`/`-L` takes precedence over `TMUX`; without either,
tmux uses `TMUX`, or `${TMUX_TMPDIR:-/tmp}/tmux-<uid>/default` when TMUX is
unset. Clearing variables alone is not isolation.

Agents must exercise tmux behavior through the existing Go tests and their
isolation helpers, rather than issuing ad hoc tmux or process-name killing
commands. This is a documented instruction, not a shell interception mechanism.

Go tests must create private short socket directories with `testutil.SocketDir(t)`,
clear TMUX/TMUX_PANE, pin TMUX_TMPDIR and explicitly select a socket, including
cleanup. CLI and tmux packages install `testutil.ParentGuard` through TestMain;
new tmux-backed packages need the same gate. The guard uses a disposable server
and detects server/session-list changes after tests. It cannot undo damage or
identify all pane/option changes, and concurrent user activity can trigger it.
It never sweeps another run's directory, and it removes its own unless a tmux
server inside it is still answering and would not stop -- that socket is left in
place so it can be probed by hand. Set `VTC_GUARD_VERBOSE=1` to have the guard
print the stand-in socket and every position it watches. The testutil package
verifies the guard using disposable servers.

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
make test    # Runs web tests, the Go test suite, go vet, and the tmux-safety guard tests
```

### Linter Rules and Thresholds

Rules are defined in root `.golangci.yml`:
- **`lll`**: Maximum 120 characters per line (tab-width: 4), applicable to all Go source and test files.
- **`funlen`**: Maximum 80 lines and 50 statements per function (excluding comments).
- **`gocyclo`**: Cyclomatic complexity threshold is 15.
- **`mnd`**: Magic numbers in business logic are disallowed. Named semantic constants must be defined. Excluded for test files and `internal/config/defaults.go`.
- **`goconst`**: Repeated strings of length >= 3 occurring 3 or more times require a shared constant. Test files are excluded, and their occurrences are not counted towards a product file's total.
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

## Release process

Release through the CI workflow in `.github/workflows/ci.yml`:

1. Update `Makefile`, `scripts/package.sh`, `hub/web/package.json`, and both root version entries in `hub/web/package-lock.json` in a PR. Use a stable `X.Y.Z` version.
2. Wait for the stable `test` summary check and the full CI pipeline, including four-platform packaging and checksum verification, to pass. Merge the PR into `main` and wait for that main commit's CI to pass.
3. Create and push `vX.Y.Z` on that commit. CI verifies that the tag matches every source version default and that the commit belongs to main, then reruns all checks on the tagged commit.
4. The `publish` job consumes only the `release-bundle` produced by its own successful run. It uploads a draft with all four archives and checksums before making it public. Only that job has `contents: write`.

Never manually publish or upload local builds to bypass a failed check. A local green run or an unrelated successful workflow does not authorize a release. Do not move a published tag or overwrite published assets. If draft upload fails, inspect and remove the incomplete draft before rerunning; if the release is already public, fix forward with a new version.

The workflow dependency regression tests run with `go test ./...`; the version validation tests run with `node --test 'scripts/ci-*.test.mjs'`.

## Selective CI

The workflow always starts; it does not use event-level `paths-ignore`. The pinned `dorny/paths-filter` action compares the cumulative PR diff via GitHub's API and the entire main push against its previous SHA. Only root `*.md`, `docs/**`, and `openspec/**` are excluded from application checks. Mixed, unknown, deleted, and runtime help paths take the full path whenever a non-documentation path is present. Release tags bypass filtering and always run full validation and packaging.

Use **`test` as the required status check** in branch protection. It runs with `always()` and explicitly verifies prerequisite results: classification/lightweight checks must succeed; application validation, packaging, and assembly must all succeed for full runs and must be deliberately skipped for confirmed documentation-only runs. A failed or cancelled prerequisite cannot turn into a successful summary merely because dependent jobs were skipped. Conditional jobs alone are unsuitable as the only required check. Existing branch protection rules are not modified by this change.

Only superseded runs of the same PR share a cancelling concurrency group. Main and tag runs use unique run IDs; publication retains its separate, non-cancelling per-tag lock.

References: [GitHub required-check guidance](https://docs.github.com/en/pull-requests/how-tos/merge-and-close-pull-requests/troubleshooting-required-status-checks), [paths-filter documentation](https://github.com/dorny/paths-filter/tree/v4.0.2), and [Sentry's selective backend CI](https://github.com/getsentry/sentry/blob/master/.github/workflows/backend.yml).
