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
