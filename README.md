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
- Session names accept non-ASCII text such as Chinese (up to 64 characters); `:`/`.`, `/`/`\`, full-width lookalikes, edge whitespace, and control characters are reserved/disallowed.
- Preserve terminal state across browser disconnects by leaving tmux sessions running.
- Use bearer-token authentication and short-lived, single-use WebSocket tickets.
- Ship the web UI inside one dependency-free application binary.
- Structured external YAML configuration with environment variable and CLI flag overrides.
- Shut down cleanly with a two-phase graceful budget without killing underlying tmux sessions.

## Requirements

- macOS or Linux
- tmux 3.3 or newer (3.7b is the tested version)
- A modern browser

Building from source additionally requires Go 1.26.3+ and Node.js 24+.

## Quick start

Download the archive for your platform from the repository's Releases page, then:

```sh
tar -xzf visual-tmux-client_0.2.0_linux_amd64.tar.gz
cd visual-tmux-client_0.2.0_linux_amd64
./visual-tmux-client
```

The server listens on `http://127.0.0.1:7690` by default. At startup it prints its access token and the URL to open — whether the token was generated, loaded from a configuration file, or taken from the environment — so you can always sign in. Open the URL and paste that token into the sign-in screen.

To use a stable token via environment variable:

```sh
VISUAL_TMUX_CLIENT_TOKEN="$(openssl rand -base64 32)" ./visual-tmux-client
```

Or pass a custom YAML configuration file:

```sh
./visual-tmux-client --config configs/visual-tmux-client.example.yaml
```

Run `./visual-tmux-client --help` to see all flags and environment variables.

## Configuration

Visual Tmux Client supports external YAML configuration, environment variables, and CLI flags.

### Precedence

Settings are resolved in strict priority order:
1. **Command-line flags** (highest precedence, e.g. `--addr`, `--config`)
2. **Environment variables** (`VISUAL_TMUX_CLIENT_*`)
3. **Configuration file** (`--config <path>`, `VISUAL_TMUX_CLIENT_CONFIG`, or `./visual-tmux-client.yaml`)
4. **Built-in defaults** (lowest precedence)

If configuration values are changed in YAML, restart the server to apply backend changes; active browser sessions receive updated web configuration upon page refresh.

### Command-line Flags

| Flag | Type | Default | Description |
| --- | --- | --- | --- |
| `-addr` | `string` | `127.0.0.1:7690` | Listen address (`host:port`) |
| `-config` | `string` | `""` | Path to YAML configuration file |
| `-version` | `bool` | `false` | Print version and exit |

### Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `VISUAL_TMUX_CLIENT_CONFIG` | Unset | Path to the YAML configuration file |
| `VISUAL_TMUX_CLIENT_ADDR` | `127.0.0.1:7690` | Listen address (`host:port`) |
| `VISUAL_TMUX_CLIENT_TOKEN` | Generated at startup | Shared bearer token used by the browser UI |
| `VISUAL_TMUX_CLIENT_ORIGIN` | Unset | Allowed WebSocket origin; defaults to matching request host |
| `VISUAL_TMUX_CLIENT_TMUX_PATH` | Resolved from `PATH` | Path to the tmux executable binary |

### YAML Configuration File

A fully commented template is provided in `configs/visual-tmux-client.example.yaml`:

```yaml
version: 1

server:
  addr: "127.0.0.1:7690"
  read_header_timeout: "10s"
  idle_timeout: "120s"
  max_request_body_bytes: 65536 # 64 KiB

auth:
  token: "" # Generated at startup if empty
  ticket_ttl: "30s"
  ticket_sweep_interval: "10s"

tmux:
  path: "" # Resolved from $PATH if empty

websocket:
  origin: "" # Strict same-host if empty; or set exact origin (e.g. "https://tmux.example.com")
  max_input_message_bytes: 8388608 # 8 MiB
  control_write_timeout: "5s"
  output_write_timeout: "10s"
  exit_write_timeout: "3s"

terminal:
  max_dimension: 1000
  staging_buffer_bytes: 2097152 # 2 MiB
  output_high_water_bytes: 1048576 # 1 MiB
  output_low_water_bytes: 131072 # 128 KiB
  backpressure_poll_interval: "100ms"

shutdown:
  attachment_timeout: "3s"
  http_timeout: "10s"

web:
  session_poll_interval: "5s"
  activity_decay: "2s"
  activity_throttle: "500ms"
  resize_debounce: "100ms"
  reconnect:
    initial_delay: "500ms"
    max_delay: "3s"
  terminal:
    scrollback: 5000
    font_size: 13
    min_font_size: 8
    max_font_size: 24
  notifications:
    max_toasts: 5
    error_lifetime: "8s"
    warning_lifetime: "5s"
    info_lifetime: "3s"
```

The default loopback binding is intentional. If you expose the service to another machine, put it behind HTTPS, use a strong stable token, and set `origin` to the exact public origin. See [SECURITY.md](SECURITY.md) before exposing it to a network.

## Build from source

```sh
make build
./hub/visual-tmux-client
```

`make build` installs locked frontend dependencies, builds the Vue application, and compiles `hub/cmd/visual-tmux-client` with embedded static assets.

Other useful commands:

```sh
make test       # Frontend tests, Go unit/integration tests, and go vet
make lint       # golangci-lint quality checks (pinned v2.13.2)
make fmt        # Format Go source files with project conventions
make package    # Build release tarballs under release/
make clean      # Remove generated build output and caches
```

To create multiple archives locally, pass space-separated Go targets:

```sh
TARGETS="darwin/arm64 darwin/amd64 linux/arm64 linux/amd64" make package
```

Pushing a `v*` tag runs the GitHub Actions release workflow, builds those four targets, creates checksums, and publishes a GitHub Release.

## Architecture

Visual Tmux Client is organized into clean functional packages with strict dependency directions:

```text
cmd/visual-tmux-client/  Entry point, CLI flag parsing, and exit code handling
internal/app/            Application composition root, signals, and two-phase shutdown
internal/config/         Configuration models, strict YAML decoding, loader, and provenance
internal/auth/           Bearer token verification, timing-safe equality, ticket store
internal/session/        Session domain model, name validation, service, Backend interface
internal/tmux/           Tmux command runner, PTY allocation, socket isolation, environment scrubbing
internal/terminal/       Terminal backpressure buffer, ring staging, attachment pump
internal/transport/http/ REST API routing, DTO mapping, bearer middleware, SPA static handler
internal/transport/ws/   WebSocket handshake, origin validation, framing, peer adapter
web/                     Vue 3 frontend (Vite + TypeScript + xterm.js)
configs/                 Example configuration templates
```

The browser UI fetches public runtime parameters from `GET /api/client-config` prior to mounting. Session authentication exchanges the long-lived Bearer token for a single-use 30-second ticket, ensuring credentials are never exposed in WebSocket URLs or process arguments.

### Rollback Strategy

Visual Tmux Client retains full backwards compatibility:
- Starting the binary without a YAML configuration file automatically operates with the documented built-in defaults.
- All command-line flags and environment variables from previous versions continue to function identically.
- If rolling back to an earlier binary release, simply stop the server, replace the executable, and restart. Running tmux sessions are fully preserved across restarts and downgrades.

## Contributing

Bug reports and pull requests are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for the development workflow and [SECURITY.md](SECURITY.md) for vulnerability reports.

## License

MIT © 2026 AK12-Official and contributors. See [LICENSE](LICENSE).
