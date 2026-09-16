import test from 'node:test'
import assert from 'node:assert/strict'

import {
  basename,
  dirname,
  isRoot,
  joinPath,
  quoteForShell,
  splitPath,
} from './pathUtils'

test('basename handles trailing slashes, the root, and nesting', () => {
  assert.equal(basename('/home/user/a.txt'), 'a.txt')
  assert.equal(basename('/home/user/'), 'user')
  assert.equal(basename('/home/user'), 'user')
  assert.equal(basename('/a'), 'a')
  assert.equal(basename('/'), '')
  assert.equal(basename('/home/user/deep/tree/'), 'tree')
})

test('dirname handles the root, bare names, and nesting', () => {
  assert.equal(dirname('/home/user/a.txt'), '/home/user')
  assert.equal(dirname('/home/user/'), '/home')
  assert.equal(dirname('/a'), '/')
  assert.equal(dirname('/'), '/')
  assert.equal(dirname('/home/user/deep/tree/'), '/home/user/deep')
})

// The root is its own parent, so "go up" from the top of the filesystem stays
// where it is rather than producing a path the hub would refuse.
test('the root is its own parent', () => {
  assert.equal(isRoot('/'), true)
  assert.equal(isRoot('//'), true)
  assert.equal(isRoot('/home'), false)
  assert.equal(dirname('/'), '/')
})

test('joinPath keeps exactly one separator', () => {
  assert.equal(joinPath('/home/user', 'a.txt'), '/home/user/a.txt')
  assert.equal(joinPath('/home/user/', 'a.txt'), '/home/user/a.txt')
  assert.equal(joinPath('/', 'home'), '/home')
  assert.equal(joinPath('/home//', 'user'), '/home/user')
})

test('splitPath reports both halves', () => {
  assert.deepEqual(splitPath('/home/user/a.txt'), { parent: '/home/user', name: 'a.txt' })
  assert.deepEqual(splitPath('/home'), { parent: '/', name: 'home' })
})

test('quoteForShell leaves an ordinary path alone', () => {
  assert.equal(quoteForShell('/home/user/project/src/main.go'), '/home/user/project/src/main.go')
  assert.equal(quoteForShell('/srv/data/file-1_v2.txt'), '/srv/data/file-1_v2.txt')
  assert.equal(quoteForShell('/'), '/')
})

test('quoteForShell quotes what a shell would otherwise interpret', () => {
  assert.equal(quoteForShell('/home/user/my file.txt'), "'/home/user/my file.txt'")
  assert.equal(quoteForShell('/home/user/$HOME'), "'/home/user/$HOME'")
  assert.equal(quoteForShell('/home/user/`whoami`'), "'/home/user/`whoami`'")
  assert.equal(quoteForShell('/home/user/a;rm -rf /'), "'/home/user/a;rm -rf /'")
  assert.equal(quoteForShell('/home/user/a&b'), "'/home/user/a&b'")
  assert.equal(quoteForShell('/home/user/*'), "'/home/user/*'")
  assert.equal(quoteForShell('/home/user/a|b'), "'/home/user/a|b'")
  assert.equal(quoteForShell('/home/user/a>b'), "'/home/user/a>b'")
})

// A single quote cannot be escaped inside single quotes, so the standard
// close-escape-reopen sequence is the only correct form.
test('quoteForShell escapes embedded single quotes', () => {
  assert.equal(quoteForShell("/home/user/it's here"), `'/home/user/it'\\''s here'`)
})

// Insertion must never be able to run a command by itself, so nothing that
// reaches the terminal may contain a line terminator.
test('quoteForShell refuses a path that cannot be written on one line', () => {
  assert.equal(quoteForShell('/home/user/two\nlines'), null)
  assert.equal(quoteForShell('/home/user/carriage\rreturn'), null)
  assert.equal(quoteForShell(''), null)
})

test('quoteForShell never emits a line terminator for any accepted path', () => {
  const paths = [
    '/home/user/plain.txt',
    '/home/user/my file.txt',
    "/home/user/it's here",
    '/home/user/$HOME;rm -rf /',
    '/home/user/tab\there',
  ]
  for (const path of paths) {
    const quoted = quoteForShell(path)
    assert.ok(quoted !== null, `expected ${path} to be insertable`)
    assert.ok(!/[\n\r]/.test(quoted), `quoting ${path} produced a line terminator`)
  }
})
