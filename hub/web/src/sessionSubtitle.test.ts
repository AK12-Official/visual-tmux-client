import test from 'node:test'
import assert from 'node:assert/strict'
import { buildSubtitleMap, formatPaneSubtitle, rowLabel } from './sessionSubtitle'
import type { PaneSummary } from './api'

function pane(overrides: Partial<PaneSummary> = {}): PaneSummary {
  return {
    window_name: 'zsh',
    title: '',
    current_command: 'zsh',
    window_active: false,
    ...overrides,
  }
}

test('shows the window name and pane title', () => {
  assert.equal(
    formatPaneSubtitle(pane({ window_name: 'zsh', title: 'editor' })),
    'zsh: editor',
  )
})

test('marks the window name when the window is active', () => {
  assert.equal(
    formatPaneSubtitle(pane({ window_name: 'zsh', title: 'editor', window_active: true })),
    'zsh*: editor',
  )
})

test('falls back to the window name when there is no title', () => {
  assert.equal(formatPaneSubtitle(pane({ window_name: 'vim', title: '' })), 'vim')
})

test('marks the window name when it falls back and the window is active', () => {
  assert.equal(
    formatPaneSubtitle(pane({ window_name: 'vim', title: '', window_active: true })),
    'vim*',
  )
})

test('falls back to the running command when there is no window name or title', () => {
  assert.equal(
    formatPaneSubtitle(pane({ window_name: '', title: '', current_command: 'nvim' })),
    'nvim',
  )
})

test('returns null when there is nothing to show', () => {
  assert.equal(
    formatPaneSubtitle(pane({ window_name: '', title: '', current_command: '' })),
    null,
  )
})

test('returns null for a missing summary', () => {
  assert.equal(formatPaneSubtitle(undefined), null)
  assert.equal(formatPaneSubtitle(null), null)
})

test('treats whitespace-only fields as empty', () => {
  assert.equal(
    formatPaneSubtitle(pane({ window_name: '   ', title: '  ', current_command: ' bash ' })),
    'bash',
  )
})

test('does not trim the interior of a title', () => {
  assert.equal(
    formatPaneSubtitle(pane({ window_name: 'zsh', title: 'my file.txt' })),
    'zsh: my file.txt',
  )
})

test('preserves non-ASCII window names and titles', () => {
  assert.equal(
    formatPaneSubtitle(pane({ window_name: '窗口', title: '编辑器' })),
    '窗口: 编辑器',
  )
})

test('maps subtitles by session name and omits sessions with nothing to show', () => {
  const map = buildSubtitleMap([
    { name: 'work', pane: pane({ window_name: 'vim', title: 'main.go' }) },
    { name: 'idle', pane: pane({ window_name: '', title: '', current_command: '' }) },
    { name: 'bare' },
  ])
  assert.equal(map['work'], 'vim: main.go')
  assert.equal(map['idle'], undefined)
  assert.equal(map['bare'], undefined)
})

// Session names are user-controlled: tmux accepts every one of these. On a plain
// object literal they resolve to inherited Object.prototype members, which are
// truthy and would render as a subtitle on rows that have no pane summary.
test('a session named after an Object.prototype member gets no phantom subtitle', () => {
  const names = ['constructor', 'toString', 'valueOf', 'hasOwnProperty', 'isPrototypeOf']
  const map = buildSubtitleMap(names.map((name) => ({ name })))
  for (const name of names) {
    assert.equal(map[name], undefined, `${name} should have no subtitle`)
    assert.equal(Boolean(map[name]), false, `${name} must not render`)
  }
})

// `__proto__` is the mirror image: assigning to it on a plain object is silently
// swallowed, so a session by that name could never show the subtitle it has.
test('a session named __proto__ keeps its own subtitle', () => {
  const map = buildSubtitleMap([{ name: '__proto__', pane: pane({ window_name: 'zsh' }) }])
  assert.equal(map['__proto__'], 'zsh')
})

test('the subtitle map has no prototype', () => {
  assert.equal(Object.getPrototypeOf(buildSubtitleMap([])), null)
})

test('the row label names the session and, when there is one, its subtitle', () => {
  assert.equal(rowLabel('work', 'zsh*: editor'), 'Session work; zsh*: editor')
})

test('the row label omits the subtitle segment when the row shows none', () => {
  // A screen-reader user must be told what the row displays, and nothing else:
  // the window count and attached state are no longer rendered anywhere.
  assert.equal(rowLabel('work', null), 'Session work')
  assert.equal(rowLabel('work', ''), 'Session work')
  assert.equal(rowLabel('work', undefined), 'Session work')
})

test('the row label carries a session name that collides with Object.prototype', () => {
  assert.equal(rowLabel('__proto__', 'zsh'), 'Session __proto__; zsh')
})
