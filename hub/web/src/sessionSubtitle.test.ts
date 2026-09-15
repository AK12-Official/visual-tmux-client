import test from 'node:test'
import assert from 'node:assert/strict'
import { formatPaneSubtitle } from './sessionSubtitle'
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
