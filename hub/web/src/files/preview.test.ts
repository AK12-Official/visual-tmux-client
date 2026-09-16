import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import DOMPurify from 'dompurify'
import {
  MAX_IMAGE_PREVIEW_BYTES,
  MAX_MARKDOWN_RENDER_CHARS,
  choosePreview,
  renderMarkdown,
} from './preview'

test('choosePreview dispatches by kind', () => {
  assert.equal(choosePreview('photo.png', 1024), 'image')
  assert.equal(choosePreview('README.md', 1024), 'markdown')
  assert.equal(choosePreview('main.go', 1024), 'editor')
  assert.equal(choosePreview('release.zip', 1024), 'info')
  assert.equal(choosePreview('no-extension', 1024), 'editor')
})

// The transfer limit and the preview bound are separate: a file can be small
// enough to fetch and still far too large to hand to the image decoder.
test('an oversized image is presented as information instead', () => {
  assert.equal(choosePreview('photo.png', MAX_IMAGE_PREVIEW_BYTES), 'image')
  assert.equal(choosePreview('photo.png', MAX_IMAGE_PREVIEW_BYTES + 1), 'info')
})

test('an oversized markdown file is still markdown, because it truncates', () => {
  assert.equal(choosePreview('README.md', MAX_MARKDOWN_RENDER_CHARS + 1), 'markdown')
})

test('renderMarkdown sanitizes what it parsed', () => {
  const before = DOMPurify.sanitizedInputs().length
  renderMarkdown('# Title\n\nsome **bold** text')

  const produced = DOMPurify.sanitizedInputs()
  assert.equal(produced.length, before + 1)
  const parsed = produced[produced.length - 1]
  assert.match(parsed, /<h1[^>]*>Title<\/h1>/)
  assert.match(parsed, /<strong>bold<\/strong>/)
})

test('renderMarkdown passes the source through marked before sanitizing', () => {
  const before = DOMPurify.sanitizedInputs().length
  renderMarkdown('plain text')

  const produced = DOMPurify.sanitizedInputs()
  assert.equal(produced.length, before + 1)
  assert.match(produced[produced.length - 1], /<p>plain text<\/p>/)
})

// The sanitizer is the only thing standing between a Markdown file and the
// document, so it has to be the call the render makes -- reading the source is a
// weaker check than asserting the call, which the two tests above do, and this
// one records what the module hands over.
test('renderMarkdown hands marked output to DOMPurify.sanitize', () => {
  const source = readFileSync(fileURLToPath(new URL('./preview.ts', import.meta.url)), 'utf8')
  assert.match(source, /DOMPurify\.sanitize\(/)
  assert.match(source, /marked\.parse\(/)
})

test('renderMarkdown truncates an oversized document and says so', () => {
  const small = 'x'.repeat(MAX_MARKDOWN_RENDER_CHARS)
  assert.equal(renderMarkdown(small).truncated, false)

  const large = 'x'.repeat(MAX_MARKDOWN_RENDER_CHARS + 1)
  const rendered = renderMarkdown(large)
  assert.equal(rendered.truncated, true)
})

// The result is the sanitizer's answer rather than the source placed into the
// document unchecked. Only that much is assertable here: the test environment has
// no DOM, so the sanitizer is a stand-in that returns what it was given, and what
// sanitization itself strips is not exercised. That limitation is recorded in the
// OpenSpec design for this change.
test('renderMarkdown returns what the sanitizer produced', () => {
  const before = DOMPurify.sanitizedInputs().length
  const rendered = renderMarkdown('<script>alert(1)</script>')

  const produced = DOMPurify.sanitizedInputs()
  assert.equal(produced.length, before + 1)
  assert.equal(rendered.html, produced[produced.length - 1])
  assert.equal(rendered.truncated, false)
})
