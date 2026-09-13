import { existsSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

export async function resolve(specifier, context, nextResolve) {
  if (
    specifier === '@xterm/xterm' ||
    specifier === '@xterm/addon-fit' ||
    specifier === '@xterm/addon-unicode11'
  ) {
    return nextResolve(new URL('./mocks/xterm.mjs', import.meta.url).href, context)
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
