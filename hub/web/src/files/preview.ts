// Preview dispatch and the sanitizing Markdown render.
//
// The dispatch is pure and DOM-free so it can be unit-tested directly. The
// render is the only part that touches the document, and it is the reason
// preview.ts imports a sanitizer at all.

import DOMPurify from 'dompurify'
import { marked } from 'marked'
import { classifyExtension } from './binaryExtensions'

/** PreviewKind is what the manager should show for a file. */
export type PreviewKind =
  // Rendered as an image.
  | 'image'
  // Editable source with an offered rendered view.
  | 'markdown'
  // Opened in the editor.
  | 'editor'
  // Information about the file and a download action, never its contents.
  | 'info'

/**
 * MAX_IMAGE_PREVIEW_BYTES bounds what will be decoded as an image. A file can be
 * within the transfer limit and still be far too large to hand to the browser's
 * image decoder without freezing the tab.
 */
export const MAX_IMAGE_PREVIEW_BYTES = 8 * 1024 * 1024

/**
 * MAX_MARKDOWN_RENDER_CHARS bounds what will be rendered. Markdown previews by
 * parsing the whole source into a DOM, so an enormous document costs far more to
 * render than it did to download.
 */
export const MAX_MARKDOWN_RENDER_CHARS = 200 * 1024

/** choosePreview decides how to present a file from its name and size. */
export function choosePreview(name: string, size: number): PreviewKind {
  switch (classifyExtension(name)) {
    case 'image':
      return size > MAX_IMAGE_PREVIEW_BYTES ? 'info' : 'image'
    case 'markdown':
      return 'markdown'
    case 'binary':
      return 'info'
    default:
      return 'editor'
  }
}

export interface RenderedMarkdown {
  html: string
  truncated: boolean
}

/**
 * renderMarkdown turns Markdown source into HTML safe to insert into the
 * document.
 *
 * The source is sanitized after parsing and before it is returned: a Markdown
 * file is frequently attacker-influenced in this workflow, and it can carry
 * script elements, event-handler attributes, or embedded documents that would
 * run or load on preview. Nothing here ever returns the source as markup, and
 * only the HTML profile is allowed through.
 *
 * An oversized document is rendered as a bounded prefix with `truncated` set,
 * rather than refused: the user still gets most of the file.
 */
export function renderMarkdown(source: string): RenderedMarkdown {
  const truncated = source.length > MAX_MARKDOWN_RENDER_CHARS
  const slice = truncated ? source.slice(0, MAX_MARKDOWN_RENDER_CHARS) : source
  const parsed = marked.parse(slice, { async: false })
  return {
    html: DOMPurify.sanitize(parsed, { USE_PROFILES: { html: true } }),
    truncated,
  }
}
