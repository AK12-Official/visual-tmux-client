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

// Rendering a file must not fetch anything the file names, so the module limits
// self-loading attributes to content carried inline. The mock cannot run the
// hook, but it can show that one was installed at all -- which is the part a
// future edit is most likely to drop.
test('renderMarkdown restricts attributes that load on their own', () => {
  const hook = DOMPurify.registeredHooks().find(
    (entry: { name: string }) => entry.name === 'uponSanitizeAttribute',
  )
  assert.ok(hook, 'expected an attribute hook to be registered')

  const data = { attrName: 'src', attrValue: 'https://example.com/track.png', keepAttr: true }
  hook.handler(undefined, data)
  assert.equal(data.keepAttr, false)

  const inline = { attrName: 'src', attrValue: 'data:image/png;base64,AAAA', keepAttr: true }
  hook.handler(undefined, inline)
  assert.equal(inline.keepAttr, true)

  // A link is only followed when the user asks for it, so href is left alone.
  const link = { attrName: 'href', attrValue: 'https://example.com', keepAttr: true }
  hook.handler(undefined, link)
  assert.equal(link.keepAttr, true)

  // The same rule applies to the legacy attributes that load a background image.
  const background = {
    attrName: 'background',
    attrValue: 'https://example.com/track.png',
    keepAttr: true,
  }
  hook.handler(undefined, background)
  assert.equal(background.keepAttr, false)
})

// The hook is only half the policy. A <style> element is what DOMPurify allows
// by default and does not sanitize, so a document can reach a third party with
// @import alone -- the element has to be refused, and the inline `style`
// attribute with it, rather than filtered. Asserted here because the earlier
// tests pass for any configuration at all.
test('renderMarkdown forbids the constructs that load remote content', () => {
  renderMarkdown('# x')
  const calls = DOMPurify.sanitizeCalls()
  const config = calls[calls.length - 1].config as {
    FORBID_TAGS: string[]
    FORBID_ATTR: string[]
  }

  for (const tag of ['iframe', 'frame', 'frameset', 'object', 'embed', 'base', 'style', 'link']) {
    assert.ok(config.FORBID_TAGS.includes(tag), `expected ${tag} to be forbidden`)
  }
  // `srcset` is forbidden whole rather than checked, because a source list is not
  // one value: a leading `#` on its first candidate says nothing about the rest,
  // so a per-value rule would let a remote one through behind an inline one.
  for (const attr of ['style', 'srcset']) {
    assert.ok(config.FORBID_ATTR.includes(attr), `expected ${attr} to be forbidden`)
  }
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
