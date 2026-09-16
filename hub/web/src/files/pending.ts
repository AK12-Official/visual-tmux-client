// The operations a path has outstanding, and the ability to wait for them.
//
// Kept out of the component for the reason renames.ts is: what goes wrong here
// is an interleaving, and an interleaving is only observable if the state it
// turns on can be driven directly.
//
// Two rules, and both are the whole of it.
//
// A count, not a flag. Two operations of one kind can be in flight on one path
// at once -- two saves of a tab, two renames of an entry -- and the first to be
// answered must not clear the record of the second, which is still outstanding:
// a save that crossed the second would then be recorded as saved, which is the
// wrong answer the record exists to prevent.
//
// A path and everything beneath it, not the path alone. A delete or a rename
// names an entry, and the writes that cross it name what is inside: a save of
// /d/dir/f.txt is one a delete of /d/dir has to wait for, and a save of that
// file is one a delete of /d/dir must not start. Reading this as exact-path was
// the first version's mistake, and it survived because the obvious caller -- the
// delete waiting for the tabs it is about to close -- happens to name the same
// paths. It stops being obvious the moment a tab is closed while its write is
// still travelling: the record is keyed by the path the write named, and the tab
// it came from is gone.
//
// What the record is for, in all three of its uses: an operation the browser has
// sent can be interleaved with another, on the hub, in an order the browser
// cannot see. A rename and a save crossing means the save's answer describes a
// write that may have named a path the rename has since vacated; a delete and a
// save crossing means the file the user was told was deleted may have been
// written back. The browser cannot tell either way, so it neither records the
// save nor claims the delete stuck -- and for the second it does something
// better than recording anything: it does not let its own two requests race at
// all. See deleteTarget in the manager.

/** Pending is the set of in-flight operations of one kind, by path. */
export interface Pending {
  /** begin records that an operation naming `path` has been sent. */
  begin(path: string): void
  /** end records that the operation naming `path` has been answered. */
  end(path: string): void
  /**
   * isPending reports whether an operation naming `path`, or anything beneath
   * it, is in flight.
   */
  isPending(path: string): boolean
  /**
   * idle resolves once nothing is outstanding for `path` or anything beneath it.
   *
   * What it waits for is a subtree that is clear at the moment it looks, not one
   * that can never be busy again: the predicate is asked again after every
   * answer, so an operation begun while the subtree is still busy extends the
   * wait, and one begun after it has been reported clear is simply a later
   * operation. That last case is not covered here and does not need to be: the
   * caller that waits is a delete, which records itself as outstanding before it
   * waits, and a save declines to start against an outstanding delete.
   */
  idle(path: string): Promise<void>
}

export function createPending(): Pending {
  const counts = new Map<string, number>()
  // Waiters are few -- at most one per delete the user has confirmed -- so they
  // are a list that is scanned, rather than a map that has to be kept in step
  // with the counts.
  let waiters: { path: string; settle: () => void }[] = []

  /**
   * covers reports whether an operation naming `candidate` is one that `path`
   * has to account for.
   *
   * A prefix is a path element, not a string: /d/a does not cover /d/ab. The
   * trailing separator is checked rather than appended blindly, because the
   * filesystem root is a path like any other and "/" + "/" names nothing.
   */
  function covers(candidate: string, path: string): boolean {
    if (candidate === path) return true
    return path.endsWith('/') ? candidate.startsWith(path) : candidate.startsWith(`${path}/`)
  }

  function busy(path: string): boolean {
    for (const candidate of counts.keys()) {
      if (covers(candidate, path)) return true
    }
    return false
  }

  function settleIdleWaiters() {
    if (waiters.length === 0) return
    const stillWaiting: typeof waiters = []
    for (const waiter of waiters) {
      // The predicate is read here rather than the waiter being resolved when
      // the operation it waits for began, so an operation begun while the
      // subtree is still busy keeps the wait open instead of slipping past it:
      // nothing resolves except from this scan, and this scan sees a count that
      // a later `begin` has already raised.
      if (busy(waiter.path)) stillWaiting.push(waiter)
      else waiter.settle()
    }
    waiters = stillWaiting
  }

  return {
    begin(path) {
      counts.set(path, (counts.get(path) ?? 0) + 1)
    },
    end(path) {
      const outstanding = (counts.get(path) ?? 0) - 1
      if (outstanding > 0) counts.set(path, outstanding)
      else counts.delete(path)
      settleIdleWaiters()
    },
    isPending(path) {
      return busy(path)
    },
    idle(path) {
      if (!busy(path)) return Promise.resolve()
      return new Promise((settle) => {
        waiters.push({ path, settle })
      })
    },
  }
}
