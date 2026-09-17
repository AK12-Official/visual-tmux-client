// The loaded part of the directory tree. Kept free of the DOM so the on-demand
// loading rule can be exercised directly.

import { listDirectory, type Entry } from './api'

/** TreeState holds what has been fetched so far, keyed by directory path. */
export interface TreeState {
  children: Map<string, Entry[]>
  truncated: Set<string>
  expanded: Set<string>
  /**
   * loads is the newest wait for a directory's listing, by path.
   *
   * A wait that goes to the hub can be answered out of order, so the answer a
   * superseded wait carries describes the disk as it was before a later answer
   * does -- and writing it would move the cache backwards. The newest wait for a
   * path is therefore the only one allowed to write it, which is a rule about
   * this map rather than about any caller: a caller that checked a ticket of its
   * own could only check it after the write had already happened.
   */
  loads: Map<string, number>
}

export function createTreeState(): TreeState {
  return { children: new Map(), truncated: new Set(), expanded: new Set(), loads: new Map() }
}

export function cachedChildren(state: TreeState, path: string): Entry[] | undefined {
  return state.children.get(path)
}

export function isTruncated(state: TreeState, path: string): boolean {
  return state.truncated.has(path)
}

/**
 * isExpanded reports whether a directory's contents are showing.
 *
 * A directory counts as open only while its listing is known. An entry left in
 * the set after its listing was dropped would otherwise render as open with
 * nothing beneath it -- which reads as an empty directory -- and the next click
 * would try to collapse something that was not showing.
 */
export function isExpanded(state: TreeState, path: string): boolean {
  return state.expanded.has(path) && state.children.has(path)
}

/** invalidateDirectory drops one directory's listing and nothing else.
 *
 * It is what a change *inside* a directory calls for: that directory's own
 * contents no longer match the disk, and the directories beneath it are not
 * affected. `forgetDirectory` is the wider tool, for something that is gone. */
export function invalidateDirectory(state: TreeState, path: string): void {
  state.children.delete(path)
  state.truncated.delete(path)
  supersede(state, path)
}

/**
 * forgetDirectory drops a directory's cached listing, and every listing cached
 * beneath it.
 *
 * A directory that was deleted or renamed must not be served from the cache the
 * next time that name is opened. Dropping only the named path is not enough: a
 * new directory of the same name would inherit the old one's subtree and render
 * rows for entries that are gone, with nothing the user could do to clear it.
 */
export function forgetDirectory(state: TreeState, path: string): void {
  const prefix = path === '/' ? '/' : `${path}/`
  const under = (key: string) => key === path || key.startsWith(prefix)

  for (const key of [...state.children.keys()]) {
    if (under(key)) state.children.delete(key)
  }
  for (const key of [...state.truncated]) {
    if (under(key)) state.truncated.delete(key)
  }
  // Nothing beneath a forgotten directory can still be showing: its listing is
  // what said so, and that is what just went.
  for (const key of [...state.expanded]) {
    if (under(key)) state.expanded.delete(key)
  }
  // A wait for anything this covered is a wait for a listing that no longer
  // describes anything, so its answer must not put one back.
  for (const key of [...state.loads.keys()]) {
    if (under(key)) supersede(state, key)
  }
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
      const expanded = entry.is_dir && isExpanded(state, path)
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

/** issue records that a wait for a directory's listing is beginning, and returns
 * the ticket that wait has to present to write it. */
function issue(state: TreeState, path: string): number {
  const ticket = (state.loads.get(path) ?? 0) + 1
  state.loads.set(path, ticket)
  return ticket
}

/** supersede ends whatever wait is outstanding for a directory.
 *
 * Every ticket a later wait for that path can be given is higher than the ones
 * already issued, so a wait that is still travelling finds its ticket stale and
 * keeps its answer to itself. */
function supersede(state: TreeState, path: string): void {
  issue(state, path)
}

/**
 * loadDirectory fetches a directory's children, or returns the ones already
 * known. Loading on first use rather than walking the tree up front is what
 * keeps opening the manager bounded on a directory with thousands of children.
 *
 * Pass force to re-read a directory whose contents may have changed.
 *
 * Only the newest wait for a path may write it. Two waits overlap whenever a
 * refresh and a navigation name the same directory -- or two navigations do --
 * and the header stays live throughout, so the older answer can arrive last.
 * Its listing describes the disk as it was when it was read, so storing it would
 * leave the manager showing a directory it has already moved past, and the
 * caller's own check cannot prevent that: by the time a caller has the answer,
 * this write has happened.
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
  // Issued after the cache check, because a call that answers from the cache
  // makes no claim about the disk and must not retire a wait that does.
  const ticket = issue(state, path)
  const result = await listDirectory(path)
  if (state.loads.get(path) !== ticket) return result.entries
  state.children.set(path, result.entries)
  if (result.truncated) {
    state.truncated.add(path)
  } else {
    state.truncated.delete(path)
  }
  return result.entries
}
