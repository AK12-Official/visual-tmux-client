<script lang="ts" setup>
// Task 8.1: geometry-accurate pane grid. One xterm.js `Terminal` instance
// per pane, created once and kept alive for the pane's full lifetime
// (design.md: switching windows shows/hides rather than destroys/
// recreates instances). Positioning comes purely from each pane's
// already-populated domain-model X/Y/Width/Height fields (task 2.1/2.3) --
// the frontend must NOT reparse the raw tmux layout string itself
// (design.md explicitly rejects that alternative).
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { domain } from '../../wailsjs/go/models'
import { PaneScrollback, SelectPane, SendKeys } from '../../wailsjs/go/main/App'
import { EventsOn } from '../../wailsjs/runtime/runtime'

const props = defineProps<{
  sessions: domain.Session[]
  selectedWindowId: string | null
}>()

interface TrackedPane {
  term: Terminal
  el: HTMLDivElement
  fitAddon: FitAddon
  resizeObserver: ResizeObserver
  // Task 8.2: a pane's existing scrollback (fetched once, asynchronously,
  // right after the Terminal is created) must land in the terminal
  // *before* any live "engine:pane-output" bytes that arrive while that
  // fetch is still in flight -- otherwise a fast-scrolling pane could show
  // new output followed by stale-looking history underneath it. `ready`
  // stays false and incoming output is queued in `pending` (in arrival
  // order) until the scrollback write completes, then the queue is
  // flushed once, in order, and further output is written straight
  // through.
  ready: boolean
  pending: Uint8Array[]
}

// Wails/JSON-transports a pane's raw output as base64 (see app.go's
// paneOutputPayload doc: arbitrary bytes, not guaranteed valid JSON string
// content) -- decode back to bytes rather than treating it as text so
// xterm.js sees the exact original byte stream.
function base64ToUint8Array(b64: string): Uint8Array {
  if (!b64) return new Uint8Array(0)
  const binary = atob(b64)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i)
  }
  return bytes
}

const gridEl = ref<HTMLDivElement | null>(null)
const holdingEl = ref<HTMLDivElement | null>(null)

// Keyed by pane ID (e.g. "%3") -- guaranteed unique and stable for the
// pane's lifetime, unlike domain.Session.ID (see navigation-tree-
// verification.md's task 7.2 note: that field is never populated
// engine-side, a pre-existing out-of-scope gap). Pane IDs don't have that
// problem -- tmuxconn keys its pane map by this same ID.
const registry = new Map<string, TrackedPane>()

function findSelectedWindow(): domain.Window | null {
  if (!props.selectedWindowId) return null
  for (const session of props.sessions) {
    const win = (session.Windows ?? {})[props.selectedWindowId]
    if (win) return win
  }
  return null
}

function allKnownPaneIDs(): Set<string> {
  const ids = new Set<string>()
  for (const session of props.sessions) {
    for (const win of Object.values(session.Windows ?? {})) {
      for (const paneID of Object.keys(win.Panes ?? {})) {
        ids.add(paneID)
      }
    }
  }
  return ids
}

function ensureTracked(paneID: string): TrackedPane {
  let entry = registry.get(paneID)
  if (entry) return entry

  const term = new Terminal({
    convertEol: true,
    scrollback: 5000,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace',
    fontSize: 13,
    // Matches TmuxHub's (git.woa.com/tmuxhub/tmuxhub) "default-dark" theme
    // (web/src/theme.ts) so panes read as the same near-black terminal
    // surface as the rest of the shell's chrome, rather than xterm.js's
    // stock black-on-white default.
    theme: {
      background: '#0a0a0a',
      foreground: '#e5e5e5',
      cursor: '#e5e5e5',
      black: '#1a1a1a',
      brightBlack: '#525252',
      red: '#f87171',
      brightRed: '#fca5a5',
      green: '#4ade80',
      brightGreen: '#86efac',
      yellow: '#facc15',
      brightYellow: '#fde047',
      blue: '#60a5fa',
      brightBlue: '#93c5fd',
      magenta: '#c084fc',
      brightMagenta: '#d8b4fe',
      cyan: '#22d3ee',
      brightCyan: '#67e8f9',
      white: '#e5e5e5',
      brightWhite: '#ffffff',
    },
  })
  const fitAddon = new FitAddon()
  term.loadAddon(fitAddon)

  const el = document.createElement('div')
  el.className = 'pane-grid__term'
  el.dataset.paneId = paneID
  holdingEl.value?.appendChild(el)
  term.open(el)

  // Task 9.1: forward every keystroke/paste xterm.js captures for this
  // pane straight to the engine. `data` is already a plain string (xterm
  // guarantees valid UTF-8 text, including raw ASCII escape/control
  // sequences for arrow keys, Ctrl-C, etc. -- see app.go's SendKeys doc),
  // so no encoding is needed here.
  term.onData((data) => {
    void SendKeys(paneID, data).catch((err) => {
      console.error(`PaneGrid: SendKeys failed for pane ${paneID}`, err)
    })
  })

  // Task 9.2: clicking a pane's terminal view focuses it (so subsequent
  // keystrokes go to xterm's internal textarea for *this* pane, since
  // several Terminal instances share the page) and tells the engine this
  // pane should become active. The domain model's `pane.Active` flag --
  // which drives both this component's active-border styling and
  // NavigationTree's highlighting -- is updated via the normal
  // `pane.focus-changed` -> refresh() -> props round-trip (see App.vue),
  // so it also picks up focus changes from an external `tmux select-pane`.
  el.addEventListener('mousedown', () => {
    term.focus()
    void SelectPane(paneID).catch((err) => {
      console.error(`PaneGrid: SelectPane failed for pane ${paneID}`, err)
    })
  })

  const resizeObserver = new ResizeObserver(() => {
    // The container can transiently report a zero-size box mid-layout
    // (e.g. right after being reparented); fitAddon.fit() throws if the
    // terminal has no renderable dimensions yet, so guard it.
    if (el.clientWidth > 0 && el.clientHeight > 0) {
      try {
        fitAddon.fit()
      } catch {
        // ignore transient layout glitches
      }
    }
  })
  resizeObserver.observe(el)

  entry = { term, el, fitAddon, resizeObserver, ready: false, pending: [] }
  registry.set(paneID, entry)

  // Seed the terminal with whatever this pane has already produced (task
  // 8.2's "Scrollback shown on first display" scenario) -- backed
  // engine-side by the pane's ring buffer, which by now already contains
  // pre-existing content even for a pane that was discovered before this
  // frontend ever subscribed (see host.go's seedPaneScrollbackIfUnbuffered).
  // Runs once per pane, exactly when its TrackedPane is first created.
  void (async () => {
    try {
      const b64 = await PaneScrollback(paneID)
      const bytes = base64ToUint8Array(b64)
      if (bytes.length > 0) term.write(bytes)
    } catch (err) {
      console.error(`PaneGrid: failed to fetch scrollback for pane ${paneID}`, err)
    } finally {
      // Re-look-up rather than closing over `entry`: the pane may have
      // been pruned (killed) while this fetch was in flight, in which
      // case its terminal is already disposed and must not be written to.
      const current = registry.get(paneID)
      if (current) {
        for (const chunk of current.pending) {
          current.term.write(chunk)
        }
        current.pending = []
        current.ready = true
      }
    }
  })()

  return entry
}

function pruneDeadPanes() {
  const known = allKnownPaneIDs()
  for (const [paneID, entry] of registry) {
    if (!known.has(paneID)) {
      entry.resizeObserver.disconnect()
      entry.term.dispose()
      entry.el.remove()
      registry.delete(paneID)
    }
  }
}

function render() {
  pruneDeadPanes()
  if (!gridEl.value || !holdingEl.value) return

  // Task 9.2: detaching/re-appending a pane's element below (needed so
  // panes that left the selected window don't linger positioned per the
  // previous layout) blurs any focused descendant -- e.g. xterm's hidden
  // input textarea for the pane the user just clicked -- since moving a
  // node in the DOM always drops its focus, even when it lands back in
  // the same logical container. Every SelectPane call round-trips through
  // `pane.focus-changed` -> App.vue's refresh() -> this same watch/render,
  // so without this save/restore, clicking a pane to focus it would
  // immediately un-focus it again once that round-trip's re-render ran.
  const activeEl = document.activeElement
  const focusedPaneId =
    activeEl instanceof Node && gridEl.value.contains(activeEl)
      ? (activeEl as HTMLElement).closest<HTMLElement>('[data-pane-id]')?.dataset.paneId
      : undefined

  // Detach everything currently placed in the grid back to the (hidden)
  // holding area first, so panes that left the selected window don't
  // linger positioned per the previous layout.
  while (gridEl.value.firstChild) {
    holdingEl.value.appendChild(gridEl.value.firstChild)
  }

  const win = findSelectedWindow()
  if (!win) return
  const panes = Object.values(win.Panes ?? {})
  if (panes.length === 0) return

  const totalWidth = Math.max(...panes.map((p) => p.X + p.Width))
  const totalHeight = Math.max(...panes.map((p) => p.Y + p.Height))
  if (totalWidth <= 0 || totalHeight <= 0) return

  for (const pane of panes) {
    const { el, fitAddon, term } = ensureTracked(pane.ID)
    el.style.left = `${(pane.X / totalWidth) * 100}%`
    el.style.top = `${(pane.Y / totalHeight) * 100}%`
    el.style.width = `${(pane.Width / totalWidth) * 100}%`
    el.style.height = `${(pane.Height / totalHeight) * 100}%`
    el.classList.toggle('pane-grid__term--active', !!pane.Active)
    el.classList.toggle('pane-grid__term--dead', !!pane.Dead)
    gridEl.value.appendChild(el)
    if (pane.ID === focusedPaneId) {
      term.focus()
    }
    requestAnimationFrame(() => {
      if (el.clientWidth > 0 && el.clientHeight > 0) {
        try {
          fitAddon.fit()
        } catch {
          // ignore transient layout glitches
        }
      }
    })
  }
}

watch(() => [props.sessions, props.selectedWindowId], render, { deep: true, immediate: true })

// Single subscription for the component's lifetime (mirrors App.vue's
// connect()-scoped "engine:lifecycle" subscription -- PaneGrid itself is
// only ever mounted while connected, per App.vue's `v-if="connected"`).
// Subscribing unconditionally to *all* pane output up front, then
// filtering by pane ID downstream, is deliberate: it sidesteps the
// ordering hazard flagged in xterm-bridge-verification.md's task 6.2
// finding, where a pane's first output could otherwise race ahead of the
// point this component starts listening for it specifically.
let unsubscribeOutput: (() => void) | null = null

onMounted(() => {
  unsubscribeOutput = EventsOn('engine:pane-output', (evt: { paneId: string; data: string }) => {
    const entry = registry.get(evt.paneId)
    if (!entry) return // not currently tracked/displayed -- nothing to update
    const bytes = base64ToUint8Array(evt.data)
    if (bytes.length === 0) return
    if (entry.ready) {
      entry.term.write(bytes)
    } else {
      // Scrollback seeding for this pane hasn't finished yet; queue in
      // arrival order so it's flushed after (not interleaved with) the
      // seeded history once that write lands.
      entry.pending.push(bytes)
    }
  })
})

onBeforeUnmount(() => {
  if (unsubscribeOutput) {
    unsubscribeOutput()
    unsubscribeOutput = null
  }
  for (const entry of registry.values()) {
    entry.resizeObserver.disconnect()
    entry.term.dispose()
  }
  registry.clear()
})
</script>

<template>
  <div class="pane-grid">
    <div v-if="!selectedWindowId" class="pane-grid__placeholder">
      Select a window to view its panes.
    </div>
    <div ref="gridEl" class="pane-grid__grid"></div>
    <div ref="holdingEl" class="pane-grid__holding"></div>
  </div>
</template>

<style scoped>
.pane-grid {
  position: relative;
  flex: 1;
  height: 100%;
  min-width: 0;
  background: var(--app-bg);
}
.pane-grid__placeholder {
  padding: 1rem;
  color: var(--th-text-lo);
  font-size: 0.85rem;
  font-family: var(--app-ui-font);
}
.pane-grid__grid {
  position: relative;
  width: 100%;
  height: 100%;
}
.pane-grid__holding {
  position: absolute;
  width: 0;
  height: 0;
  overflow: hidden;
}
/* Pane-chrome pattern modeled on TmuxHub's ApplicationPaneFrame
   (web/src/components/PaneFrame.tsx): rounded bordered box, low-contrast
   border by default, a lighter border plus a subtle 1px glow ring on the
   active pane. Border is drawn just inside the box (box-sizing) so it
   never perturbs the percentage geometry verified in task 8.1. */
:deep(.pane-grid__term) {
  position: absolute;
  box-sizing: border-box;
  border: 1px solid var(--th-border);
  border-radius: 6px;
  background: #0a0a0a;
  overflow: hidden;
  padding: 2px;
}
:deep(.pane-grid__term--active) {
  border-color: var(--th-text-lo);
  box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.08);
}
:deep(.pane-grid__term--dead) {
  opacity: 0.5;
}
</style>
