// Minimal DOMPurify stand-in for the node test environment, which has no
// document for the real package to work against.
//
// sanitize records what it was given and returns it unchanged, so a test can
// assert that rendered Markdown actually passes through the sanitizer -- the
// property the preview module is responsible for. The configuration is recorded
// as well, because the policy is what keeps a preview from reaching a third
// party, and asserting only that a sanitizer ran would pass for any policy.
//
// Sanitization itself is not behaviourally exercised here; the hook the module
// installs can be invoked directly, which the tests do.

const calls = []
const hooks = []

function sanitize(html, config) {
  calls.push({ html, config })
  return html
}

const DOMPurify = {
  sanitize,
  addHook(name, handler) {
    hooks.push({ name, handler })
  },
  removeHook(name) {
    const index = hooks.findIndex((hook) => hook.name === name)
    if (index >= 0) hooks.splice(index, 1)
  },
  isSupported: false,
  version: 'mock',
  /** sanitizedInputs returns every input sanitize was given, oldest first. */
  sanitizedInputs: () => calls.map((call) => call.html),
  /** sanitizeCalls returns what each call was given, including its config. */
  sanitizeCalls: () => calls.slice(),
  /** registeredHooks returns the hooks the module under test installed. */
  registeredHooks: () => hooks.slice(),
}

export default DOMPurify
