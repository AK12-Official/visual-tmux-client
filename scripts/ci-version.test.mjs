import assert from 'node:assert/strict';
import { test } from 'node:test';
import { buildVersion } from './ci-version.mjs';

const sha = 'a'.repeat(40);
test('release tag must exactly match the stable source version', () => {
  assert.equal(buildVersion('0.5.0', 'refs/tags/v0.5.0', sha), '0.5.0');
  for (const tag of ['v0.4.0', 'v0.5.0-rc.1', 'v0.5.0/path', '0.5.0']) {
    assert.throws(() => buildVersion('0.5.0', `refs/tags/${tag}`, sha));
  }
});
test('branch and PR packages are identified as development builds', () => {
  for (const ref of ['refs/heads/main', 'refs/pull/15/merge']) {
    assert.equal(buildVersion('0.5.0', ref, sha), '0.5.0-dev.aaaaaaa');
  }
});
test('reject malformed source versions and commit identifiers', () => {
  for (const version of ['', '01.5.0', '0.5', '0.5.0/path', '0.5.0\nmalicious', '0.5.0\n']) {
    assert.throws(() => buildVersion(version, 'refs/heads/main', sha));
  }
  assert.throws(() => buildVersion('0.5.0', 'refs/heads/main', 'short'));
});
