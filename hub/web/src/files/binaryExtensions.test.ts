import test from 'node:test'
import assert from 'node:assert/strict'

import { classifyExtension, extensionOf } from './binaryExtensions'

test('extensionOf reads the extension and ignores a leading dot', () => {
  assert.equal(extensionOf('main.go'), 'go')
  assert.equal(extensionOf('ARCHIVE.TAR.GZ'), 'gz')
  assert.equal(extensionOf('no-extension'), '')
  assert.equal(extensionOf('.bashrc'), '')
  assert.equal(extensionOf('trailing.'), '')
  assert.equal(extensionOf(''), '')
})

test('images are recognised whatever their case', () => {
  for (const name of ['a.png', 'a.PNG', 'photo.jpeg', 'photo.JPG', 'icon.svg', 'x.webp']) {
    assert.equal(classifyExtension(name), 'image', `${name} should be an image`)
  }
})

test('markdown is recognised', () => {
  for (const name of ['README.md', 'doc.markdown', 'notes.mkd']) {
    assert.equal(classifyExtension(name), 'markdown', `${name} should be markdown`)
  }
})

test('known binary formats are recognised', () => {
  for (const name of ['release.zip', 'lib.dylib', 'archive.tar', 'video.mp4', 'font.woff2']) {
    assert.equal(classifyExtension(name), 'binary', `${name} should be binary`)
  }
})

// The cost of guessing wrong in this direction is a strange-looking editor, not
// a lost file, so an unknown extension is treated as text.
test('source and unknown extensions are text', () => {
  for (const name of ['main.go', 'app.vue', 'notes.txt', 'Makefile', 'data.unknownext']) {
    assert.equal(classifyExtension(name), 'text', `${name} should be text`)
  }
})
