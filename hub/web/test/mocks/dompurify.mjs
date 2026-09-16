// Minimal DOMPurify stand-in for the node test environment, which has no
// document for the real package to work against.
//
// sanitize records what it was given and returns it unchanged, so a test can
// assert that rendered Markdown actually passes through the sanitizer -- the
// property the preview module is responsible for. Sanitization itself is not
// behaviourally exercised here; see the note in the OpenSpec design.

const calls = []

function sanitize(html) {
  calls.push(html)
  return html
}

const DOMPurify = {
  sanitize,
  isSupported: false,
  version: 'mock',
  /** sanitizedInputs returns every input sanitize was given, oldest first. */
  sanitizedInputs: () => calls.slice(),
}

export default DOMPurify
