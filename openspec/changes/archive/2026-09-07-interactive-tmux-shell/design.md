## Context

See proposal.md for motivation. The engine (`engine/domain`, `engine/eventbus`, `engine/ringbuffer`, `engine/tmuxcm`, `engine/tmuxconn`) is read-only today: `HostConn` (tmuxconn/host.go) issues admin commands only for discovery (`list-sessions`, `list-windows`, `list-panes`, `capture-pane`) via the private `issueAdminCommand`, and `tmuxcm.ParseLayoutPaneIDs` (tmuxcm/layout.go) discards the position/size fields of tmux's layout grammar, keeping only pane IDs for death-detection diffing. The only existing UI code is a throwaway Wails throughput-spike project (per `bootstrap-tmux-engine`'s design.md and tasks.md 2.5), not shipped product code.

Constraints carried forward from `bootstrap-tmux-engine`:
- No HTTP server / browser-accessed port — the shell is an embedded Wails webview.
- The engine stays a pure Go library with no UI framework dependency.
- One control-mode subprocess per tracked session (not per host); any live subprocess can issue admin commands via `-t` targeting (`issueAdminCommand`'s retry-across-connections behavior).

## Goals / Non-Goals

**Goals:**
- Turn the engine's `HostConn` into a read-write interface: session/window/pane management operations, plus raw input forwarding to a pane.
- Extract full layout geometry (position, size, split orientation per pane) from tmux's layout string, feeding both the domain model and the shell's pane grid.
- Ship a real Wails product shell: navigation tree, geometry-accurate pane grid with one xterm.js instance per pane, bidirectional wiring (output in, keystrokes out), and UI entry points for the new management operations.

**Non-Goals:**
- Drag-to-resize panes, custom tmux prefix-key rebinding, persisted UI settings/themes (explicitly deferred per proposal.md).
- SSH/multi-host (deferred; the shell targets a single local host connection for this change, same as `bootstrap-tmux-engine`).
- Headless terminal emulation in the engine (still out of scope; the shell's xterm.js instances remain the only interpreted terminal state, per `bootstrap-tmux-engine`'s design.md).

## Decisions

### Decision: Input forwarding via `send-keys -H` (hex byte forwarding), not `-l` or paste-buffer
**What**: `HostConn.SendKeys(paneID string, data []byte)` forwards raw bytes by issuing `send-keys -H <hex-byte-pairs...> -t <paneID>` — each input byte encoded as two hex digits, space-separated, matching tmux's `-H` flag semantics (hex byte arguments, not a literal string).
**Why**: xterm.js's `onData` callback already emits the exact bytes a real terminal would send (arrow-key escape sequences, `Ctrl-C` as `0x03`, pasted multi-line text as-is). Hex forwarding requires no client-side translation from "this is an arrow key" to a tmux key name — the bytes pass through unmodified. `send-keys -l` would require that translation for non-literal keys (arrows, control chars needing escaping); `paste-buffer` is designed for bulk paste, not low-latency per-keystroke interaction.
**Alternatives considered**: `send-keys -l` with a client-side keymap (arrow keys, Ctrl-combos → tmux key names) — rejected, adds a translation layer that hex forwarding makes unnecessary and that risks subtle mismatches (e.g. terminal modes affecting what escape sequence an arrow key actually produces, which the client would need to replicate). `paste-buffer`/`load-buffer` — rejected for interactive typing due to per-command overhead unsuited to per-keystroke latency; still applicable in the future for explicit "paste" actions but not the general input path.
**Verification**: no dedicated spike; a manual verification task (arrow keys, Ctrl-C, multi-line paste behave identically to a real attached terminal) is included in tasks.md before the shell's input path is considered done.

### Decision: `ParseLayout` returns a full geometry tree; `ParseLayoutPaneIDs` becomes a thin wrapper over it
**What**: Add `tmuxcm.ParseLayout(layout string) *LayoutNode`, where `LayoutNode` carries `X, Y, Width, Height int`, and either `PaneID string` (leaf) or `Split Orientation` + `Children []*LayoutNode` (internal node), covering the same grammar `ParseLayoutPaneIDs` already parses (`WxH,x,y[,pane_id]` or bracketed child lists). `ParseLayoutPaneIDs` is reimplemented as a post-order walk over `ParseLayout`'s result, collecting leaf pane IDs — same output, same call sites, no behavior change for existing callers.
**Why**: `bootstrap-tmux-engine` deliberately parsed only pane-ID presence because that change had no rendering consumer; this change's whole point is rendering panes at their real positions, which requires the geometry `ParseLayoutPaneIDs` already discards. Reimplementing as a wrapper avoids maintaining two separate parsers for the same grammar.
**Alternatives considered**: Leave `ParseLayoutPaneIDs` as its own parser and write a second, independent geometry parser — rejected, duplicates non-trivial recursive-descent parsing logic (see layout.go's `parseLayoutNode`) for the same grammar, doubling the maintenance surface for tmux layout-string quirks.

### Decision: Domain model gains geometry fields on `Pane`, populated at the same point layout is already parsed
**What**: `domain.Pane` gains `X, Y, Width, Height int` (Width/Height already exist, populated from `list-panes`; X/Y are new). `HostConn` populates all four from `tmuxcm.ParseLayout` at the same call sites that already handle layout (`discoverWindowsAndPanes`'s initial seed, and the `%layout-change` handler in `handleNotification`), rather than the UI re-deriving geometry from the raw layout string itself.
**Why**: The layout string is already being parsed engine-side for the pane-ID diff that drives death detection; extending that same parse to populate geometry is strictly additive and keeps the domain model the single source of truth consumers query, consistent with the existing model design (`bootstrap-tmux-engine`'s "Domain model shape" decision).
**Alternatives considered**: Ship the raw layout string to the shell and parse it in TypeScript — rejected, duplicates the layout grammar in a second language and moves a correctness-sensitive parse out of the tested Go engine into unqualified frontend code.

### Decision: One xterm.js instance per pane, kept mounted for the lifetime of its pane
**What**: The shell creates one xterm.js instance per tracked pane (not per window or globally shared), mounted/positioned per that pane's domain-model geometry, and disposed only when the pane dies or its session closes. Switching windows in the navigation tree shows/hides the relevant set of instances rather than destroying and recreating them.
**Why**: Matches the engine's per-pane event bus/ring-buffer granularity (`pane.output` events and `PaneScrollback` are already keyed by pane ID) and avoids re-seeding scrollback on every window switch — an instance that's already been fed history stays correct without a fresh `PaneScrollback` call each time it's revisited.
**Alternatives considered**: A single shared terminal view that re-renders per selected pane — rejected, forces a full scrollback re-seed and loses independent scroll position per pane when switching focus, which is a worse experience than tmux's own behavior of each pane keeping its own scroll state.

### Decision: Session rename and pane focus become first-class lifecycle events (`session.renamed`, `pane.focus-changed`)
**What**: Add `SessionRenamed` and `PaneFocusChanged` to `eventbus.LifecycleEventType`, published when `%session-renamed` is observed (engine- or externally-initiated rename) and when the active pane in a window changes (via `%window-pane-changed`, already parsed by tmuxcm but not currently acted on in `handleNotification`).
**Why**: `bootstrap-tmux-engine`'s event set covers discovery/topology/output/death but has no rename or focus-change event; the shell's navigation tree and pane-grid focus state both need to react to these without polling. `%window-pane-changed` is already parsed into a `Notification` field by tmuxcm (parser.go's `NotifWindowPaneChanged` case) but `handleNotification` in host.go currently has no case for it — this change adds one.
**Alternatives considered**: Infer rename/focus changes only from the next `list-sessions`/`list-panes` poll — rejected, reintroduces polling latency the control-mode protocol and existing notification-driven design otherwise avoid.

### Decision: Management operations are direct `HostConn` methods over `issueAdminCommand`, not a new command queue/abstraction
**What**: Each new operation (`CreateSession`, `KillSession`, `RenameSession`, `NewWindow`, `KillWindow`, `RenameWindow`, `SplitPane`, `KillPane`, `SelectPane`, `SendKeys`) is a public method on `HostConn` that formats the corresponding tmux command string and calls the existing `issueAdminCommand`, returning its error directly to the caller (Wails Go-side binding) with no intermediate queue or retry policy beyond what `issueAdminCommand` already does.
**Why**: `issueAdminCommand` already provides the needed properties (issues on any live connection, tolerates a connection dying mid-command by retrying across connections) — per `bootstrap-tmux-engine`'s design, this was built generically enough that admin/write commands need no new plumbing. Errors surfacing directly (Requirement: "Management action fails" in tmux-shell-ui's spec) is simpler than introducing a queue whose own failure modes would need separate handling.
**Alternatives considered**: A write-operation queue with its own retry/backoff — rejected as unnecessary complexity; tmux commands are synchronous request/response over a connection `issueAdminCommand` already manages, and domain-model updates for a successful write arrive via the same notification path as any externally-triggered change (no separate "apply the write to the model" step needed beyond what already happens for e.g. `%layout-change`).

## Risks / Trade-offs

- [Risk] Hex-forwarded `send-keys -H` behavior for edge cases (very large pastes, bracketed-paste mode, IME composition input) is unverified against tmux's actual handling → Mitigation: manual verification task in tasks.md covering arrows/Ctrl-C/multi-line paste before considering the input path done; if bracketed-paste or IME issues surface, address as a follow-up rather than blocking this change on exhaustive terminal-input-edge-case coverage.
- [Risk] Keeping one xterm.js instance mounted per pane for the pane's full lifetime increases the shell's memory/DOM footprint as tracked-pane count grows → Mitigation: acceptable for this change's single-host, interactively-sized session scope (matches `bootstrap-tmux-engine`'s analogous per-session-subprocess trade-off); revisit (e.g. virtualizing off-screen panes) if real usage shows it's a problem.
- [Trade-off] `%window-pane-changed` drives focus state, but a user's click-to-focus in the shell must itself call `SelectPane` (via `select-pane -t <paneID>`) to change tmux's own active pane — otherwise the shell's focus indicator would drift from tmux's actual active pane as seen by other clients (e.g. a concurrent terminal attach). This change's `SelectPane` operation exists specifically to keep those in sync.

## Migration Plan

Additive to `bootstrap-tmux-engine`'s engine code (new methods, new domain fields, a reimplemented-but-compatible `ParseLayoutPaneIDs`) — no existing engine behavior changes for existing callers. The throwaway Wails spike-2 project is replaced outright by the new shell project; nothing from it is migrated (per its own tasks.md 2.5, it was never meant to be kept as product code).

## Open Questions

None — all decisions above are resolved for this change's scope; multi-host, drag-resize, and prefix-key rebinding are deferred per proposal.md, not left as open questions within this change's scope.
