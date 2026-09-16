// Open-file state for the file manager, kept free of the DOM so the save and
// conflict rules can be exercised directly.

import { writeFile } from './api'

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
  /** mtime is the modification time last observed from the hub. */
  mtime: number
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
export async function saveOpenFile(file: OpenFile, force = false): Promise<number | null> {
  return writeFile(file.path, file.text, force ? null : file.mtime)
}

/** applySaved folds a successful save back into the file's state.
 *
 * `sent` is the exact text the write carried, and it is what `saved` becomes.
 * Taking the text from the live object instead would record keystrokes typed
 * during the round trip as saved -- they never reached the disk, and the tab
 * would report itself clean and let them be discarded without a warning.
 *
 * A null mtime leaves the observed time as it was. The write did happen, so the
 * tab is saved; the next save will be told the file changed -- which it did --
 * and ask before overwriting rather than assuming it did not. */
export function applySaved(file: OpenFile, mtime: number | null, sent: string): OpenFile {
  return { ...file, saved: sent, mtime: mtime ?? file.mtime }
}
