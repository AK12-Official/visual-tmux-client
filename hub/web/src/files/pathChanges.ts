// What happened to a path while a read of it was travelling: the name a rename
// gave it, or the fact that a delete took it away.
//
// Kept out of the component because the thing that goes wrong here is an
// interleaving, and an interleaving is only observable if the state it turns on
// can be driven directly. The rules are small:
//
//   - Every change is recorded with the generation it happened in.
//   - A read remembers the generation it started in, and resolves only against
//     changes that happened *after* that.
//
// The second rule is the whole point. Consulting the map without it makes the
// record a history rather than this read's business: a rename nobody was reading
// through would then redirect whichever read happened to overlap some later,
// unrelated rename -- to a different file, under a name the user did not open.
//
// A removal is recorded for the same reason and at the same moment as a rename:
// when the hub answers that it happened. A read the hub has already granted goes
// on returning the file's contents, and can arrive after the delete has swept
// the tabs it found -- so the sweep is a moment rather than a rule, and this is
// what makes it a rule. What a removal means to a read is only that its answer
// must not be installed: the name may have been taken again by somebody else by
// the time the answer lands, and the contents in hand describe whatever was
// there when the hub opened it, which is not necessarily what is there now. A
// delete that fails records nothing: nothing was removed, and a read of that
// file is a read of a file that is still there.

/** PathChanges records what has happened to paths, and what a given read should
 * know. */
export interface PathChanges {
  /** record notes that a rename moved `from` to `to`, at the current generation. */
  record(from: string, to: string): void
  /**
   * recordRemoval notes that a delete removed `path`, at the current generation.
   *
   * It is recorded for the entry the delete named, and covers everything beneath
   * it: a directory that was deleted takes its contents with it.
   */
  recordRemoval(path: string): void
  /**
   * generation is the number a read captures before it starts, and passes back
   * to resolve.
   */
  generation(): number
  /**
   * resolve maps a path to where it is now, considering only what happened after
   * `since`. A path that was removed on the way resolves to null, which no path
   * is: it says the read has nothing it may install.
   */
  resolve(path: string, since: number): string | null
  /** clear forgets everything, which is safe once no read is travelling. */
  clear(): void
}

interface Change {
  from: string
  /** to is where a rename moved it to, or empty for a removal. */
  to: string
  /** removed distinguishes the two, and is what `to` cannot say: a rename to no
   * name is not a thing. */
  removed: boolean
  generation: number
}

/**
 * holds reports whether `path` is the recorded path or an entry beneath it.
 *
 * A prefix is a path element, not a string: /d/a does not hold /d/ab. The
 * trailing separator is checked rather than appended blindly, because the
 * filesystem root is a path like any other and "/" + "/" names nothing.
 */
function holds(recorded: string, path: string): boolean {
  if (path === recorded) return true
  return recorded.endsWith('/') ? path.startsWith(recorded) : path.startsWith(`${recorded}/`)
}

export function createPathChanges(): PathChanges {
  // A list, not a map keyed by the source path: a name can be vacated and taken
  // again inside one read's flight, and the two changes are then both part of
  // what happened to it. Keyed by source, the second would replace the first and
  // send the read to the newer file's destination carrying the older file's
  // bytes. Appended, the list is already in the order they happened.
  const changes: Change[] = []
  let current = 0

  return {
    record(from, to) {
      changes.push({ from, to, removed: false, generation: ++current })
    },
    recordRemoval(path) {
      changes.push({ from: path, to: '', removed: true, generation: ++current })
    },
    generation() {
      return current
    },
    resolve(path, since) {
      let at = path
      for (const change of changes) {
        if (change.generation <= since) continue
        if (change.removed) {
          // Nothing that happens after this revives the read: a name taken again
          // is a different file, and the contents in hand are not its.
          if (holds(change.from, at)) return null
          continue
        }
        // Applied in the order they happened, one at a time -- which is the only
        // order that is faithful. Picking the deepest match instead gets the
        // wrong answer when a shallow rename and a deeper one both apply: the
        // shallow one moved the path out from under the deeper one, so the
        // deeper one no longer names it.
        if (at === change.from) {
          at = change.to
        } else if (at.startsWith(`${change.from}/`)) {
          at = change.to + at.slice(change.from.length)
        }
      }
      return at
    },
    clear() {
      changes.length = 0
    },
  }
}
