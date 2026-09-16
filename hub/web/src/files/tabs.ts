// Open-file state for the file manager, kept free of the DOM so the save and
// conflict rules can be exercised directly.
//
// Free of the renderer too, deliberately: how a file's contents should be shown
// is a presentation rule and lives in preview.ts, which imports the sanitizer.
// A save has no opinion about either, and importing the renderer to answer a
// question about a file name would drag it in for nothing.

import { writeFile, type Stamp } from './api'
import type { Classification } from './preview'

/** OpenFile is one file open in the editor. */
export interface OpenFile {
  /**
   * id identifies the editing session for this tab, not the file.
   *
   * It survives a rename, which moves the tab to a new path, and is not reused
   * when a tab is closed and reopened: an answer to a save belongs to the
   * session that asked for it, and a reopened file is a different session even
   * when the path is the same.
   */
  id: number
  path: string
  name: string
  /** text is the current editor contents. Empty for a file never read as text. */
  text: string
  /** saved is what was last read or written, which dirty is measured against. */
  saved: string
  /**
   * stamp is the modification time last observed from the hub, at both the
   * precisions it reports. It is what the next save is checked against, so it is
   * the observation and not a display value: taking it from anywhere but the
   * hub's own answer is what makes a save look like a conflict that is not one.
   */
  stamp: Stamp
  /** size is the byte size the directory listing reported. */
  size: number
  /**
   * binary is the hub's classification of the contents: true for binary, false
   * for text, and null for a file it was never asked about.
   *
   * It outranks the file's name, in both directions, because a name is a guess
   * and a wrong guess either mojibakes a file or refuses to open one that is
   * text. Null is a different answer from false, and has to be: an image is
   * never read, and a file the hub read as text is shown as text however it is
   * named. See Classification in preview.ts.
   */
  binary: Classification
}

/** isDirty reports whether a file differs from what was last read or saved. */
export function isDirty(file: OpenFile): boolean {
  return file.text !== file.saved
}

/** anyDirty reports whether any open file has unsaved edits. */
export function anyDirty(files: OpenFile[]): boolean {
  return files.some(isDirty)
}

/**
 * SaveRequest is everything one save is sent with, captured together.
 *
 * Captured together, and not read again later, is the whole point. Everything
 * here happens around an await, and a rename completes inside that window: the
 * tab moves to a new name, and an answer that arrives afterwards describes the
 * write that named the old one. A save that re-read the tab at answer time would
 * compare the tab against itself and agree, and would mark the file at the new
 * name saved having never written a byte there. So what is sent and what the
 * answer is matched against are the same object, taken once.
 */
export interface SaveRequest {
  /** id is the editing session that asked, which survives a rename. */
  id: number
  /** path is the file the write names. */
  path: string
  /** text is the exact contents the write carries. */
  text: string
  /** expected is what the write is compared against, or null to force it. */
  expected: Stamp | null
}

/**
 * beginSave captures what a save will send.
 *
 * `force` is what the user's confirmation after a conflict asks for: it drops the
 * observed modification time, which is the only thing that asks the hub to
 * overwrite regardless.
 */
export function beginSave(file: OpenFile, force = false): SaveRequest {
  return {
    id: file.id,
    path: file.path,
    text: file.text,
    expected: force ? null : file.stamp,
  }
}

/** saveOpenFile sends a captured save and reports the modification time the hub
 * answered with, or null when it reported none.
 *
 * The caller must adopt a returned value. Guessing it instead -- which is what a
 * client that assumed the current time would do -- dates the file behind itself
 * and makes the very next save look like a conflict. */
export async function saveOpenFile(request: SaveRequest): Promise<Stamp | null> {
  return writeFile(request.path, request.text, request.expected)
}

/** applySaved folds a successful save back into the file's state.
 *
 * `sent` is the exact text the write carried, and it is what `saved` becomes.
 * Taking the text from the live object instead would record keystrokes typed
 * during the round trip as saved -- they never reached the disk, and the tab
 * would report itself clean and let them be discarded without a warning.
 *
 * A null stamp leaves the observed time as it was. The write did happen, so the
 * tab is saved; the next save will be told the file changed -- which it did --
 * and ask before overwriting rather than assuming it did not. */
export function applySaved(file: OpenFile, stamp: Stamp | null, sent: string): OpenFile {
  return { ...file, saved: sent, stamp: stamp ?? file.stamp }
}

/** SaveSettlement is what became of a save's answer. */
export interface SaveSettlement {
  files: OpenFile[]
  /**
   * outcome is what the caller still has to say about it:
   *
   *   `saved`  -- the answer describes the file the tab holds, and the tab is now
   *               clean at the text that was sent.
   *   `closed` -- the tab is gone, so there is nothing to fold the answer into.
   *   `moved`  -- the tab is open under a different path than the write named.
   *   `raced`  -- a rename of that path was outstanding when the answer arrived.
   */
  outcome: 'saved' | 'closed' | 'moved' | 'raced'
}

/**
 * settleSave folds a save's answer back into the open files.
 *
 * The request carries the path the write named, and this is why it has to. A
 * rename completes while the write is travelling: it moves the tab to the new
 * name, and the answer that arrives afterwards describes the file the write
 * named -- which the tab no longer holds. Applying it anyway is worse than doing
 * nothing, because it records the tab as saved, at the new name, having never
 * written a byte there: the edit is left only at the old name, and the tab
 * reports no unsaved changes. So the answer is passed back to the caller as
 * `moved` instead, and the user is told to save again -- which writes the edit to
 * the name the tab now holds.
 *
 * The tab is identified by session rather than by path throughout, because a
 * rename replaces the tab object: the one captured before the await is a
 * snapshot whose path is the old one, and comparing against it would agree with
 * itself and mark the wrong file saved. That is why the request is an argument
 * rather than a set of loose values -- there is nothing here for the caller to
 * pass the wrong way round.
 */
export function settleSave(
  files: OpenFile[],
  request: SaveRequest,
  stamp: Stamp | null,
  racing = false,
): SaveSettlement {
  const tab = files.find((candidate) => candidate.id === request.id)
  if (!tab) return { files, outcome: 'closed' }
  if (tab.path !== request.path) return { files, outcome: 'moved' }
  // A rename of this path is outstanding, so this answer and that rename crossed
  // on the wire and the browser cannot tell which reached the hub first. The
  // write may have landed before the rename carried the entry to its new name --
  // in which case the tab is saved and this is only a nag -- or the rename may
  // have landed first, in which case the write recreated the old name and the
  // entry the tab now holds was never written. Recording the tab as saved would
  // be a lie in the second case and merely cautious in the first, so the answer
  // is not recorded. See design.md on the window POSIX rename cannot close.
  if (racing) return { files, outcome: 'raced' }
  return {
    files: files.map((candidate) =>
      candidate.id === request.id ? applySaved(candidate, stamp, request.text) : candidate,
    ),
    outcome: 'saved',
  }
}
