// Where a rename moved a path, for the reads that were travelling when it
// happened.
//
// Kept out of the component because the thing that goes wrong here is an
// interleaving, and an interleaving is only observable if the state it turns on
// can be driven directly. The rules are small:
//
//   - Every rename is recorded with the generation it happened in.
//   - A read remembers the generation it started in, and resolves only against
//     renames that happened *after* that.
//
// The second rule is the whole point. Consulting the map without it makes the
// record a history rather than this read's business: a rename nobody was reading
// through would then redirect whichever read happened to overlap some later,
// unrelated rename -- to a different file, under a name the user did not open.

/** Renames records where paths have moved, and what a given read should know. */
export interface Renames {
  /** record notes that a rename moved `from` to `to`, at the current generation. */
  record(from: string, to: string): void
  /**
   * generation is the number a read captures before it starts, and passes back
   * to resolve.
   */
  generation(): number
  /** resolve maps a path to where it is now, considering only renames that
   * happened after `since`. */
  resolve(path: string, since: number): string
  /** clear forgets everything, which is safe once no read is travelling. */
  clear(): void
}

interface Moved {
  from: string
  to: string
  generation: number
}

export function createRenames(): Renames {
  // A list, not a map keyed by the source path: a name can be vacated and taken
  // again inside one read's flight, and the two renames are then both part of
  // what happened to it. Keyed by source, the second would replace the first and
  // send the read to the newer file's destination carrying the older file's
  // bytes. Appended, the list is already in the order they happened.
  const moved: Moved[] = []
  let current = 0

  return {
    record(from, to) {
      current++
      moved.push({ from, to, generation: current })
    },
    generation() {
      return current
    },
    /**
     * Applied in the order they happened, one at a time -- which is the only
     * order that is faithful. Picking the deepest match instead gets the wrong
     * answer when a shallow rename and a deeper one both apply: the shallow one
     * moved the path out from under the deeper one, so the deeper one no longer
     * names it.
     */
    resolve(path, since) {
      let at = path
      for (const entry of moved) {
        if (entry.generation <= since) continue
        if (at === entry.from) {
          at = entry.to
        } else if (at.startsWith(`${entry.from}/`)) {
          at = entry.to + at.slice(entry.from.length)
        }
      }
      return at
    },
    clear() {
      moved.length = 0
    },
  }
}
