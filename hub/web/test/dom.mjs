// A document for the component tests.
//
// node has none, and mounting a component needs one. This module installs the
// globals Vue's runtime reads, and a test file has to import it *before* anything
// that imports Vue: the runtime captures the document when it is first
// evaluated, not when it renders.
//
// Assignment where the property is writable and definition where it is not --
// node has its own read-only `navigator`, and a bare assignment throws in a
// module, which is always strict.
import { JSDOM } from 'jsdom'

const dom = new JSDOM('<!doctype html><html><body></body></html>', {
  url: 'http://localhost/',
  // The components under test navigate nothing, but jsdom otherwise prints a
  // "not implemented" error for every link it is asked about.
  pretendToBeVisual: true,
})

const fromWindow = {
  window: dom.window,
  document: dom.window.document,
  navigator: dom.window.navigator,
  location: dom.window.location,
  HTMLElement: dom.window.HTMLElement,
  HTMLInputElement: dom.window.HTMLInputElement,
  HTMLTextAreaElement: dom.window.HTMLTextAreaElement,
  Element: dom.window.Element,
  Node: dom.window.Node,
  Text: dom.window.Text,
  Comment: dom.window.Comment,
  DocumentFragment: dom.window.DocumentFragment,
  SVGElement: dom.window.SVGElement,
  Event: dom.window.Event,
  KeyboardEvent: dom.window.KeyboardEvent,
  MouseEvent: dom.window.MouseEvent,
  CustomEvent: dom.window.CustomEvent,
  // @vue/test-utils resolves an element's visibility through this, so a test can
  // ask whether a `v-show`ed element is on screen.
  getComputedStyle: dom.window.getComputedStyle.bind(dom.window),
  // The application keeps its bearer token here; the pure tests stub this per
  // test, and a mounted component needs it to exist before it can be reached.
  localStorage: dom.window.localStorage,
  sessionStorage: dom.window.sessionStorage,
}

for (const [name, value] of Object.entries(fromWindow)) {
  Object.defineProperty(globalThis, name, { value, configurable: true, writable: true })
}

// Vue schedules its next render through the frame API when it has one, and a
// test that has to await a tick is clearer with a timer behind it than with a
// frame that never arrives in a document that is never painted.
Object.defineProperty(globalThis, 'requestAnimationFrame', {
  value: (callback) => setTimeout(() => callback(Date.now()), 0),
  configurable: true,
  writable: true,
})
Object.defineProperty(globalThis, 'cancelAnimationFrame', {
  value: (handle) => clearTimeout(handle),
  configurable: true,
  writable: true,
})
