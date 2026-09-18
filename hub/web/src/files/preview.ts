// Preview dispatch and the sanitizing Markdown render.
//
// The dispatch is pure and DOM-free so it can be unit-tested directly. The
// render is the only part that touches the document, and it is the reason
// preview.ts imports a sanitizer at all.

import DOMPurify from 'dompurify'
import { marked } from 'marked'
import { classifyExtension } from './binaryExtensions'
import type { Classification } from './classification'

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

/**
 * wantsText reports whether a name asks for the editor, before anything has been
 * read.
 *
 * It is the open-time question -- `choosePreview` -- asked again later, which is
 * what a rename needs: what changes is the name, and a name is all this has ever
 * had to go on. See `presentation` for what is done with the answer.
 */
export function wantsText(name: string, size: number): boolean {
  const byName = choosePreview(name, size)
  return byName === 'editor' || byName === 'markdown'
}

/**
 * namesAnImage reports whether a name says image, whatever its size.
 *
 * Size is not part of the answer, unlike `choosePreview`'s: this is asked about
 * a tab the manager has not read, to tell a picture it is refusing for its size
 * from a name it has no contents for at all. `choosePreview` answers the second
 * one with 'info' too, which is why it cannot be used here.
 */
export function namesAnImage(name: string): boolean {
  return classifyExtension(name) === 'image'
}

/**
 * presentation decides how a file's contents are shown, given the hub's
 * classification of them (see Classification in ./classification).
 *
 * The classification answers one question -- may these bytes be decoded as text?
 * -- and it is the answer to that question, not to "what is this file?", which
 * is why it outranks the file's name in both directions. Something the hub read
 * as binary is not offered as editable text whatever the name suggests, because
 * decoding its bytes is what would replace them on the next save; something it
 * read as text opens as text even when the name suggests otherwise.
 *
 * An image is the case where the two questions come apart. Every image is
 * binary, so a hub asked about one answers "binary" -- correctly, and about the
 * wrong question. An image within the bound is handed to the image decoder and
 * never decoded as text, which is what the classification exists to prevent, so
 * the name still decides. Reading it as "not previewable" is what presented a
 * picture as a file's details whenever the listing had called it too large and
 * the read found it under the bound: the file had shrunk since the listing, the
 * hub's answer was true, and the preview was still refused.
 *
 * Null is a third answer, and a different one: nobody asked, because the manager
 * opened the file without reading it. The name still decides what can be
 * *shown* -- an image, which the preview fetches for itself -- but it cannot
 * decide what may be *edited*, because the editor would be holding an empty
 * document where the file's bytes are, and saving it would write that emptiness
 * over them. A rename is what makes the two come apart: it replaces the name,
 * and a file opened as an image under one name can end up under a name that asks
 * for the editor. So nothing unread is offered as text. The read a rename starts
 * is what settles it, and until that answer lands the file is shown for what it
 * is here -- an image, or a file whose contents nobody can show.
 *
 * It lives here rather than beside the open-file state because it is a
 * presentation rule: the module that owns the save and conflict rules has no
 * business importing the renderer to answer a question about a file name.
 */
export function presentation(name: string, size: number, binary: Classification): PreviewKind {
  const byName = choosePreview(name, size)
  if (binary === false) {
    // Text the hub read, so the only thing the name still decides is whether it
    // is Markdown -- which is a rendering choice rather than a guess about the
    // contents.
    return byName === 'markdown' ? 'markdown' : 'editor'
  }
  // Binary, or nobody has read it at all. The two agree here, for neighbouring
  // reasons: bytes the hub read as binary may not be decoded, and bytes nobody
  // read are not in hand to decode -- so neither may be offered as text, and
  // what is shown is information rather than a rendering. An image within the
  // bound is still an image either way, because the image preview decodes
  // nothing: it hands the response to the browser as an image.
  //
  // The unread case is the one a name can move: a file opened as an image is
  // never read, and renamed to a name the editor would hold it would otherwise
  // be handed an empty document to save over the original bytes. The read that
  // follows a rename fills this in; this is what holds until it lands.
  return byName === 'image' ? 'image' : 'info'
}

/**
 * editable reports whether a file's contents belong in the editor.
 *
 * The file manager asks this of every open file, not only the one on screen: an
 * editor that is merely hidden has to stay mounted, because unmounting one is
 * what discards the undo history of the file the user switched away from.
 */
export function editable(name: string, size: number, binary: Classification): boolean {
  const kind = presentation(name, size, binary)
  return kind === 'editor' || kind === 'markdown'
}

export interface RenderedMarkdown {
  html: string
  truncated: boolean
}

/**
 * FORBIDDEN_TAGS are omitted from rendered output outright. An embedded document
 * is the one thing that turns a preview into a fetch the user never asked for,
 * and the specification requires it to be absent rather than neutralised.
 *
 * `style` and `link` are on the list for the same reason, less obviously: the
 * sanitizer does not sanitize CSS, and CSS carries `@import` and `url()`, which
 * fetch whatever they name. A document that wants to reach a third party would
 * otherwise only have to say so in a stylesheet rather than in an element.
 */
const FORBIDDEN_TAGS = [
  'iframe',
  'frame',
  'frameset',
  'object',
  'embed',
  'portal',
  'base',
  'style',
  'link',
  'meta',
]

/**
 * FORBIDDEN_ATTRS are attributes whose value can itself load remote content,
 * beyond what the inline-source rule below can decide.
 *
 * `srcset` is here rather than in that rule because a source list is not one
 * value: its candidates are comma-separated, so a leading `#` on the first one
 * says nothing about the second. There is no Markdown construct that produces
 * the attribute, so refusing it whole costs nothing.
 */
const FORBIDDEN_ATTRS = ['style', 'srcset']

/** SELF_LOADING_ATTRS are the attributes that make a browser fetch something. */
const SELF_LOADING_ATTRS = new Set(['src', 'poster', 'background'])

/** INLINE_SRC_RE matches a source the document already carries. */
const INLINE_SRC_RE = /^(?:data:image\/|blob:|#)/i

// A rendered file must not reach a third party merely because it named one, so
// anything that loads on its own is limited to content carried inline. Links are
// left alone: an href is only followed when the user asks for it.
//
// Registered on the module-level instance, which nothing else in the application
// uses. A second consumer of DOMPurify would inherit this restriction, which is
// harmless but worth knowing.
DOMPurify.addHook('uponSanitizeAttribute', (_node, data) => {
  if (!SELF_LOADING_ATTRS.has(data.attrName.toLowerCase())) return
  if (INLINE_SRC_RE.test(data.attrValue.trim())) return
  data.keepAttr = false
})

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
    html: DOMPurify.sanitize(parsed, {
      USE_PROFILES: { html: true },
      FORBID_TAGS: FORBIDDEN_TAGS,
      FORBID_ATTR: FORBIDDEN_ATTRS,
    }),
    truncated,
  }
}
