import { pathToFileURL } from 'node:url';

// A deliberately skipped application suite is valid only for confirmed docs.
export function assertCIStatus(needs) {
  if (needs.changes?.result !== 'success') {
    throw new Error('Change classification and lightweight checks must succeed');
  }
  const full = needs.changes.outputs?.full;
  if (full !== 'true' && full !== 'false') {
    throw new Error('Missing or invalid change classification');
  }
  const expected = full === 'true' ? 'success' : 'skipped';
  for (const job of ['validate', 'package', 'assemble']) {
    if (needs[job]?.result !== expected) {
      throw new Error(`${job}: expected ${expected}, got ${needs[job]?.result ?? 'missing'}`);
    }
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  assertCIStatus(JSON.parse(process.env.NEEDS_JSON ?? '{}'));
  console.log('All required CI checks passed');
}
