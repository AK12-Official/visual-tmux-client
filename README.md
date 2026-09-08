# tmux-hub

`tmux-hub` is a small, self-hosted web interface for managing and using tmux sessions from a browser. The Vue frontend is embedded in a single Go binary, so deployment only needs the binary and a local `tmux` installation.

> The project is currently an early `v0.1` release. It manages tmux on the same machine where `tmux-hub` runs; remote-host aggregation is not implemented.

## Features

- List, create, rename, and terminate local tmux sessions.
- Attach to a session in a full browser terminal powered by xterm.js.
- Preserve terminal state across browser disconnects by leaving tmux sessions running.
- Use bearer-token authentication and short-lived, single-use WebSocket tickets.
- Ship the web UI inside one dependency-free application binary.
- Shut down cleanly without killing the underlying tmux sessions.

## Requirements

- macOS or Linux
- tmux 3.3 or newer (3.7b is the tested version)
- A modern browser

Building from source additionally requires Go 1.26.3+ and Node.js 24+.

## Quick start

Download the archive for your platform from the repository's Releases page, then:

```sh
tar -xzf tmux-hub_0.1.0_darwin_arm64.tar.gz
cd tmux-hub_0.1.0_darwin_arm64
./tmux-hub
```

The server listens on `http://127.0.0.1:7690` by default. When no token is configured, it prints a freshly generated token to stderr. Open the URL and paste that token into the sign-in screen.

To use a stable token:

```sh
TMUX_HUB_TOKEN="$(openssl rand -base64 32)" ./tmux-hub
```

Run `./tmux-hub --help` to see all flags and environment variables.

## Configuration

| Setting | Default | Description |
| --- | --- | --- |
| `--addr` | `127.0.0.1:7690` | HTTP listen address |
| `TMUX_HUB_TOKEN` | generated at startup | Shared bearer token used by the browser UI |
| `TMUX_HUB_ORIGIN` | `http://<listen-address>` | Exact browser origin allowed for WebSocket upgrades |
| `TMUX_HUB_TMUX_PATH` | resolved from `PATH` | Explicit path to the tmux executable |

The default loopback binding is intentional. If you expose the service to another machine, put it behind HTTPS, use a strong stable token, and set `TMUX_HUB_ORIGIN` to the exact public origin. See [SECURITY.md](SECURITY.md) before exposing it to a network.

## Build from source

```sh
make build
./hub/tmux-hub
```

`make build` installs locked frontend dependencies, builds the Vue application, and embeds it into the Go binary. Other useful commands:

```sh
make test       # frontend type/build check, Go tests, and go vet
make package    # package the current OS/architecture under release/
make clean      # remove generated build output
```

To create multiple archives locally, pass space-separated Go targets:

```sh
TARGETS="darwin/arm64 darwin/amd64 linux/arm64 linux/amd64" make package
```

Pushing a `v*` tag runs the GitHub Actions release workflow, builds those four targets, creates checksums, and publishes a GitHub Release.

## Architecture

The Go service exposes a small authenticated JSON API for session management and a WebSocket endpoint backed by a pseudo-terminal. The browser first exchanges its bearer token for a 30-second, single-use ticket; the long-lived token is never placed in a WebSocket URL. The compiled Vue assets are served from Go's embedded filesystem.

Project layout:

```text
hub/              Go server, tmux integration, and tests
hub/web/          Vue + TypeScript frontend
openspec/specs/   behavior and design specifications
scripts/          release packaging helpers
```

## Contributing

Bug reports and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for the development workflow and [SECURITY.md](SECURITY.md) for vulnerability reports.

## License

MIT © 2026 AK12-Official and contributors. See [LICENSE](LICENSE).
