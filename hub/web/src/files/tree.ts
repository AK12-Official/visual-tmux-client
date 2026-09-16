// The loaded part of the directory tree. Kept free of the DOM so the on-demand
// loading rule can be exercised directly.

import { listDirectory, type Entry } from './api'

/** TreeState holds what has been fetched so far, keyed by directory path. */
export interface TreeState {
  children: Map<string, Entry[]>
  truncated: Set<string>
  expanded: Set<string>
}

export function createTreeState(): TreeState {
  return { children: new Map(), truncated: new Set(), expanded: new Set() }
}

export function cachedChildren(state: TreeState, path: string): Entry[] | undefined {
  return state.children.get(path)
}

export function isTruncated(state: TreeState, path: string): boolean {
  return state.truncated.has(path)
}

export function isExpanded(state: TreeState, path: string): boolean {
  return state.expanded.has(path)
}

export function forgetDirectory(state: TreeState, path: string): void {
  state.children.delete(path)
  state.truncated.delete(path)
}

/** TreeRow is one visible line of the tree. */
export interface TreeRow {
  entry: Entry
  path: string
  depth: number
  /** expanded is whether a directory's own contents are showing beneath it. */
  expanded: boolean
}

/**
 * visibleRows flattens the tree into the rows a user can currently see: the
 * contents of `root`, plus the contents of every directory expanded beneath it.
 *
 * A flat list rather than nested components, because the tree is a rendering of
 * state that is already flat and keyed by path -- and because a recursive
 * component would have to refer to itself, which is the kind of indirection that
 * silently renders nothing when the reference does not resolve.
 */
export function visibleRows(state: TreeState, root: string): TreeRow[] {
  const rows: TreeRow[] = []

  const walk = (dir: string, depth: number) => {
    for (const entry of state.children.get(dir) ?? []) {
      const path = join(dir, entry.name)
      const expanded = entry.is_dir && state.expanded.has(path)
      rows.push({ entry, path, depth, expanded })
      if (expanded) {
        walk(path, depth + 1)
      }
    }
  }

  walk(root, 0)
  return rows
}

/** join appends a child name to a directory path. */
function join(dir: string, name: string): string {
  return dir === '/' ? `/${name}` : `${dir}/${name}`
}

/**
 * loadDirectory fetches a directory's children, or returns the ones already
 * known. Loading on first use rather than walking the tree up front is what
 * keeps opening the manager bounded on a directory with thousands of children.
 *
 * Pass force to re-read a directory whose contents may have changed.
 */
export async function loadDirectory(
  state: TreeState,
  path: string,
  force = false,
): Promise<Entry[]> {
  if (!force) {
    const cached = state.children.get(path)
    if (cached) return cached
  }
  const result = await listDirectory(path)
  state.children.set(path, result.entries)
  if (result.truncated) {
    state.truncated.add(path)
  } else {
    state.truncated.delete(path)
  }
  return result.entries
}
