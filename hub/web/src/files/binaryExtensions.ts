// Extension-based classification for the file manager. The hub transfers exact
// bytes and says nothing about their type, so deciding how to present a file is
// the browser's job -- and an extension is the only signal available before the
// bytes arrive.

/** FileKind is how the manager presents a file. */
export type FileKind = 'image' | 'markdown' | 'binary' | 'text'

const IMAGE_EXTENSIONS = new Set([
  'png',
  'jpg',
  'jpeg',
  'gif',
  'webp',
  'bmp',
  'ico',
  'avif',
  'svg',
])

const MARKDOWN_EXTENSIONS = new Set(['md', 'markdown', 'mdown', 'mkd', 'mdx'])

// Formats that are not text and would be shown as mojibake if they were opened
// in the editor. The list is deliberately conservative: an extension that is not
// here is treated as text, because the cost of being wrong in that direction is
// a strange-looking editor rather than a lost file.
const BINARY_EXTENSIONS = new Set([
  'pdf',
  'zip',
  'gz',
  'tgz',
  'bz2',
  'xz',
  'zst',
  '7z',
  'rar',
  'tar',
  'jar',
  'class',
  'o',
  'a',
  'so',
  'dylib',
  'dll',
  'exe',
  'bin',
  'dat',
  'db',
  'sqlite',
  'sqlite3',
  'pyc',
  'wasm',
  'woff',
  'woff2',
  'ttf',
  'otf',
  'eot',
  'mp3',
  'wav',
  'flac',
  'ogg',
  'mp4',
  'mov',
  'avi',
  'mkv',
  'webm',
])

/** extensionOf returns a name's lower-case extension without the dot. */
export function extensionOf(name: string): string {
  const index = name.lastIndexOf('.')
  // A leading dot is part of the name, not an extension: ".bashrc" has none.
  if (index <= 0 || index === name.length - 1) return ''
  return name.slice(index + 1).toLowerCase()
}

/** classifyExtension decides how a file is presented, from its name alone. */
export function classifyExtension(name: string): FileKind {
  const ext = extensionOf(name)
  if (ext === '') return 'text'
  if (IMAGE_EXTENSIONS.has(ext)) return 'image'
  if (MARKDOWN_EXTENSIONS.has(ext)) return 'markdown'
  if (BINARY_EXTENSIONS.has(ext)) return 'binary'
  return 'text'
}
