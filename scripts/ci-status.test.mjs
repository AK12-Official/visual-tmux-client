import assert from 'node:assert/strict';
import { test } from 'node:test';
import { assertCIStatus } from './ci-status.mjs';

function results(full, result) {
  return {
    changes: { result: 'success', outputs: { full } },
    validate: { result }, package: { result }, assemble: { result },
  };
}

test('accept complete validation and deliberately skipped docs builds', () => {
  assert.doesNotThrow(() => assertCIStatus(results('true', 'success')));
  assert.doesNotThrow(() => assertCIStatus(results('false', 'skipped')));
});
test('failed, cancelled, missing and unexpectedly skipped required jobs fail closed', () => {
  for (const name of ['changes', 'validate', 'package', 'assemble']) {
    for (const result of ['failure', 'cancelled', 'skipped', undefined]) {
      const needs = results('true', 'success');
      needs[name].result = result;
      assert.throws(() => assertCIStatus(needs), `${name}: ${result}`);
    }
  }
});
test('docs bypass requires an explicit, successful classification', () => {
  for (const full of ['', undefined, 'unknown']) {
    assert.throws(() => assertCIStatus(results(full, 'skipped')));
  }
  const needs = results('false', 'skipped');
  needs.changes.result = 'failure';
  assert.throws(() => assertCIStatus(needs));
  assert.throws(() => assertCIStatus({}));
});
