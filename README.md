# Visual Tmux Client

[简体中文](README.zh-CN.md) | English

**Visual Tmux Client** is a small, self-hosted web interface for managing and using tmux sessions from a browser. The Vue frontend is embedded in a single Go binary, so deployment only needs the binary and a local `tmux` installation.

> The project is currently an early `v0.2` release. It manages tmux on the same machine where Visual Tmux Client runs; remote-host aggregation is not implemented.

## Features

- List, create (one click, auto-named), rename, and terminate local tmux sessions — individually or in batches.
- Attach to a session in a full browser terminal powered by xterm.js, and re-attach after a detach or a disconnect.
- See which session is talking: background sessions with recent output get a highlighted card; the viewed session is marked in the terminal header and the browser tab title.
- Terminal header with font-size controls, fullscreen, and panel close.
- Manual session ordering with drag reorder and pin-to-top, plus a collapsible session sidebar.
- Leveled, auto-expiring toast notifications for action results and terminal events.
- Built-in Chinese tmux guide available from the in-app help button.
- Session names accept non-ASCII text such as Chinese; only `:` and `.` are reserved.
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
tar -xzf visual-tmux-client_0.2.0_darwin_arm64.tar.gz
cd visual-tmux-client_0.2.0_darwin_arm64
./visual-tmux-client
```

The server listens on `http://127.0.0.1:7690` by default. At startup it prints its access token and the URL to open — whether the token was generated or taken from the environment — so you can always sign in. Open the URL and paste that token into the sign-in screen.

To use a stable token:

```sh
VISUAL_TMUX_CLIENT_TOKEN="$(openssl rand -base64 32)" ./visual-tmux-client
```

Run `./visual-tmux-client --help` to see all flags and environment variables.

## Configuration

| Setting | Default | Description |
| --- | --- | --- |
| `--addr` | `127.0.0.1:7690` | HTTP listen address |
| `VISUAL_TMUX_CLIENT_TOKEN` | generated at startup | Shared bearer token used by the browser UI |
| `VISUAL_TMUX_CLIENT_ORIGIN` | `http://<listen-address>` | Exact browser origin allowed for WebSocket upgrades |
| `VISUAL_TMUX_CLIENT_TMUX_PATH` | resolved from `PATH` | Explicit path to the tmux executable |

The default loopback binding is intentional. If you expose the service to another machine, put it behind HTTPS, use a strong stable token, and set `VISUAL_TMUX_CLIENT_ORIGIN` to the exact public origin. See [SECURITY.md](SECURITY.md) before exposing it to a network.

## Build from source

```sh
make build
./hub/visual-tmux-client
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
