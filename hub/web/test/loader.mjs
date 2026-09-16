import { existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

export async function resolve(specifier, context, nextResolve) {
  const browserOnly = {
    '@xterm/xterm': './mocks/xterm.mjs',
    '@xterm/addon-fit': './mocks/xterm.mjs',
    '@xterm/addon-unicode11': './mocks/xterm.mjs',
    // The editor and the sanitizer both need a document, which this test
    // environment does not have.
    codemirror: './mocks/codemirror.mjs',
    dompurify: './mocks/dompurify.mjs',
  }
  if (browserOnly[specifier]) {
    return nextResolve(new URL(browserOnly[specifier], import.meta.url).href, context)
  }

  try {
    return await nextResolve(specifier, context)
  } catch (err) {
    if (specifier.startsWith('.') || specifier.startsWith('/')) {
      const parent = context.parentURL
        ? new URL(specifier, context.parentURL)
        : new URL(specifier, 'file://' + process.cwd() + '/')
      for (const ext of ['.ts', '.js']) {
        try {
          const filePath = fileURLToPath(parent.href + ext)
          if (existsSync(filePath)) {
            return await nextResolve(specifier + ext, context)
          }
        } catch {
          // ignore invalid path
        }
      }
    }
    throw err
  }
}
