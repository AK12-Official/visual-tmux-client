// Compiles a single-file component for the node test runner.
//
// The runner has no bundler, so a .vue file has to become plain ESM before node
// will import it. This does the part of @vitejs/plugin-vue that these tests
// need: <script setup> compiled with its own template inlined as the render
// function, which is one self-contained module with no second file to resolve.
//
// <style> is dropped. Nothing here asserts on styling, and a scoped style block
// has no meaning without a document to attach it to.
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { compileScript, parse } from '@vue/compiler-sfc'

/** compileVue turns one single-file component into the source of an ES module. */
export function compileVue(url) {
  const filename = fileURLToPath(url)
  const source = readFileSync(filename, 'utf8')

  const { descriptor, errors } = parse(source, { filename })
  if (errors.length > 0) {
    throw new Error(`${filename}: ${errors[0].message}`)
  }

  // The compiled script keeps its own TypeScript annotations, which node strips
  // for a module-typescript load.
  return compileScript(descriptor, {
    id: filename,
    inlineTemplate: true,
  }).content
}
