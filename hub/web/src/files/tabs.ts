// Open-file state for the file manager, kept free of the DOM so the save and
// conflict rules can be exercised directly.

import { writeFile, type Stamp } from './api'
import { choosePreview } from './preview'

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
   * binary is the hub's classification of the contents, for a file that was read
   * as text. It is what decides whether the file may be shown as editable text,
   * and it outranks the file's name: a name is a guess, and a wrong guess either
   * mojibakes a file or refuses to open one that is text. A file that was never
   * read -- an image, or one already known to be uneditable -- is false, and its
   * presentation is decided by its kind instead.
   */
  binary: boolean
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
 * editable reports whether a file's contents belong in the editor.
 *
 * It exists so the answer is in one place: the overlay has to know which files
 * the editor is holding before it renders, because an editor that is merely off
 * screen must stay mounted rather than be unmounted and rebuilt.
 */
export function editable(file: OpenFile): boolean {
  const kind = presentation(file)
  return kind === 'editor' || kind === 'markdown'
}

/**
 * presentation decides how a file's contents are shown.
 *
 * The hub's classification outranks the file's name: something it reports as
 * binary is presented as information whatever the name suggests, and decoding
 * its bytes as text is what would corrupt them on the next save.
 */
export function presentation(file: OpenFile): ReturnType<typeof choosePreview> {
  if (file.binary) return 'info'
  return choosePreview(file.name, file.size)
}

/**
 * saveOpenFile writes a file's current contents and returns the modification
 * time the hub reports, or null when it reported none.
 *
 * The caller must adopt a returned value. Guessing it instead -- which is what a
 * client that assumed the current time would do -- dates the file behind itself
 * and makes the very next save look like a conflict.
 *
 * `force` is what the user's confirmation after a conflict asks for: it drops the
 * observed modification time, which is the only thing that asks the hub to
 * overwrite regardless.
 */
export async function saveOpenFile(file: OpenFile, force = false): Promise<Stamp | null> {
  return writeFile(file.path, file.text, force ? null : file.stamp)
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
   */
  outcome: 'saved' | 'closed' | 'moved'
}

/**
 * settleSave folds a save's answer back into the open files.
 *
 * `sentPath` is the path the write named, captured before it was sent, and this
 * is why it has to be. Everything here happens around an await, and a rename
 * completes inside that window: it moves the tab to the new name, and the answer
 * that arrives afterwards describes the file the write named -- which the tab no
 * longer holds. Applying it anyway is worse than doing nothing, because it
 * records the tab as saved, at the new path, having never written a byte there:
 * the edit is left only at the old name, and the tab reports no unsaved changes.
 * So the answer is passed back to the caller as `moved` instead, and the user is
 * told to save again -- which writes the edit to the name the tab now holds.
 *
 * The tab is identified by session rather than by path throughout, because a
 * rename replaces the tab object: the one captured before the await is a
 * snapshot whose path is the old one, and comparing against it would agree with
 * itself and mark the wrong file saved.
 */
export function settleSave(
  files: OpenFile[],
  id: number,
  sent: string,
  sentPath: string,
  stamp: Stamp | null,
): SaveSettlement {
  const tab = files.find((candidate) => candidate.id === id)
  if (!tab) return { files, outcome: 'closed' }
  if (tab.path !== sentPath) return { files, outcome: 'moved' }
  return {
    files: files.map((candidate) =>
      candidate.id === id ? applySaved(candidate, stamp, sent) : candidate,
    ),
    outcome: 'saved',
  }
}
