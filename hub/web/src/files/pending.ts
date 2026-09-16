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
// An operation covers its neighbourhood rather than one exact path, and the two
// questions below are asked from opposite ends of it: a delete names an entry and
// waits for the writes inside it, so it looks *down*; a save names an entry and
// asks whether anything holding it is being deleted or renamed, so it looks *up*.
// Both were written as exact-path first, and that survived because the obvious
// caller -- a delete waiting for the tabs it is about to close -- happens to name
// the same paths. It stops being obvious the moment a tab is closed while its
// write is still travelling: the record is keyed by the path the write named, and
// the tab it came from is gone.
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
   * isPending reports whether an operation naming `path`, or naming a directory
   * above it, is in flight.
   *
   * The direction is *upwards*, and that is what its callers ask. A save is about
   * to name a file, and what makes it unsafe to send is an operation on that file
   * or on something holding it: a delete of the directory it is in, a rename of
   * that directory. Looking downwards instead -- at what is inside the path --
   * would answer a question no caller has, and would miss the one they do.
   */
  isPending(path: string): boolean
  /**
   * idle resolves once nothing is outstanding for `path` or anything beneath it.
   *
   * The direction is *downwards*, deliberately the opposite of isPending, and
   * likewise what its caller asks. A delete names an entry, and the writes it has
   * to wait for are the ones naming that entry or something inside it.
   *
   * Waiting upwards as well would be dead here rather than wrong -- what is
   * outstanding is a save, and a save names a file, so nothing outstanding can
   * ever name a directory above the path being waited for -- but a caller that
   * wanted it would be asking a different question, and should ask isPending.
   */
  idle(path: string): Promise<void>
}

/**
 * within reports whether `path` is `root` itself or an entry beneath it.
 *
 * A prefix is a path element, not a string: /d/a does not hold /d/ab. The
 * trailing separator is checked rather than appended blindly, because the
 * filesystem root is a path like any other and "/" + "/" names nothing.
 */
function within(path: string, root: string): boolean {
  if (path === root) return true
  return root.endsWith('/') ? path.startsWith(root) : path.startsWith(`${root}/`)
}

export function createPending(): Pending {
  const counts = new Map<string, number>()
  // Waiters are few -- at most one per delete the user has confirmed -- so they
  // are a list that is scanned, rather than a map that has to be kept in step
  // with the counts.
  let waiters: { path: string; settle: () => void }[] = []

  /** any reports whether some outstanding path satisfies the predicate. */
  function any(matches: (outstanding: string) => boolean): boolean {
    for (const outstanding of counts.keys()) {
      if (matches(outstanding)) return true
    }
    return false
  }

  function busyBeneath(path: string): boolean {
    return any((outstanding) => within(outstanding, path))
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
      if (busyBeneath(waiter.path)) stillWaiting.push(waiter)
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
      return any((outstanding) => within(path, outstanding))
    },
    idle(path) {
      if (!busyBeneath(path)) return Promise.resolve()
      return new Promise((settle) => {
        waiters.push({ path, settle })
      })
    },
  }
}
