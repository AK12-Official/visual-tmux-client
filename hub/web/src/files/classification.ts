// Classification is what the hub said about a file's contents, or null when it
// was never asked.
//
// Three states, not two, because "not binary" and "not known" are different
// questions with different answers: a file the hub read as text is shown as text
// whatever it is called, and a file nobody read is shown according to its name.
// A boolean could not tell those apart, and the first thing that went wrong was
// that a text file named `notes.dat` was read, classified as text, and then
// presented as binary anyway -- by its own name, which the classification was
// supposed to have overruled.
//
// It lives in a module of its own rather than beside `presentation`, which is one
// of the two rules that consume it, because the module that owns the save and
// conflict rules needs the type and must not reach the renderer: `preview.ts`
// imports the Markdown sanitizer, and an `import type` that erases at compile time
// is one keyword away from an import that does not.
export type Classification = boolean | null
