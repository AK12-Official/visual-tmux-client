// Open-file state for the file manager, kept free of the DOM so the save and
// conflict rules can be exercised directly.

import { writeFile } from './api'

/** OpenFile is one file open in the editor. */
export interface OpenFile {
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
 * time the hub reports.
 *
 * The caller must adopt the returned value. Guessing it instead -- which is what
 * a client that assumed the current time would do -- dates the file behind
 * itself and makes the very next save look like a conflict.
 *
 * `force` is what the user's confirmation after a conflict asks for: it drops
 * the observed modification time, which is the only thing that asks the hub to
 * overwrite regardless.
 */
export async function saveOpenFile(file: OpenFile, force = false): Promise<number> {
  return writeFile(file.path, file.text, force ? null : file.mtime)
}

/** applySaved folds a successful save back into the file's state. */
export function applySaved(file: OpenFile, mtime: number): OpenFile {
  return { ...file, saved: file.text, mtime }
}
