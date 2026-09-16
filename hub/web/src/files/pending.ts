// The operations a path has outstanding, and the ability to wait for them.
//
// Kept out of the component for the reason renames.ts is: what goes wrong here
// is an interleaving, and an interleaving is only observable if the state it
// turns on can be driven directly.
//
// A count, not a flag, and that is the whole of the first rule. Two operations
// of one kind can be in flight on one path at once -- two saves of a tab, two
// renames of an entry -- and the first to be answered must not clear the record
// of the second, which is still outstanding: a save that crossed the second
// would then be recorded as saved, which is the wrong answer the record exists
// to prevent.
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
  /** begin records that an operation on `path` has been sent. */
  begin(path: string): void
  /** end records that the operation on `path` has been answered. */
  end(path: string): void
  /** isPending reports whether an operation on exactly `path` is in flight. */
  isPending(path: string): boolean
  /**
   * idle resolves once nothing is outstanding for exactly `path`.
   *
   * Exactly, not for anything under it: only the caller knows which paths it
   * cares about, and a delete of a directory knows the files beneath it because
   * they are the ones it has open. A caller with a subtree to wait for asks
   * about each path in it, which is what deleteTarget does.
   *
   * What it waits for is a path that is clear at the moment it looks, not one
   * that can never be busy again: the count is asked again after every answer,
   * so an operation begun while the path is still busy extends the wait, and an
   * operation begun after the path has been reported clear is simply a later
   * operation. That last case is not covered here and does not need to be: the
   * caller that waits is a delete, which records itself as outstanding before it
   * waits, and a save refuses to start against an outstanding delete.
   */
  idle(path: string): Promise<void>
}

export function createPending(): Pending {
  const counts = new Map<string, number>()
  // Waiters are few -- at most one per delete the user has confirmed -- so they
  // are a list that is scanned, rather than a map that has to be kept in step
  // with the counts.
  let waiters: { path: string; settle: () => void }[] = []

  function settleIdleWaiters() {
    if (waiters.length === 0) return
    const stillWaiting: typeof waiters = []
    for (const waiter of waiters) {
      // The count is read here rather than the waiter being resolved when the
      // operation it waits for began, so an operation begun while the path is
      // still busy keeps the wait open instead of slipping past it: nothing
      // resolves except from this scan, and this scan sees a count that a later
      // `begin` has already raised.
      if ((counts.get(waiter.path) ?? 0) > 0) stillWaiting.push(waiter)
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
      return (counts.get(path) ?? 0) > 0
    },
    idle(path) {
      if ((counts.get(path) ?? 0) === 0) return Promise.resolve()
      return new Promise((settle) => {
        waiters.push({ path, settle })
      })
    },
  }
}
