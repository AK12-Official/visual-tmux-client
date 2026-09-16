<script setup lang="ts">
// CodeMirror 6 binding. The editor owns its document; the parent owns the value.
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { EditorView, basicSetup } from 'codemirror'

const props = defineProps<{ modelValue: string }>()
const emit = defineEmits<{ (e: 'update:modelValue', value: string): void }>()

const host = ref<HTMLElement | null>(null)
let view: EditorView | null = null

onMounted(() => {
  if (!host.value) return
  view = new EditorView({
    doc: props.modelValue,
    parent: host.value,
    extensions: [
      basicSetup,
      EditorView.updateListener.of((update) => {
        if (update.docChanged) emit('update:modelValue', update.state.doc.toString())
      }),
    ],
  })
})

watch(
  () => props.modelValue,
  (value) => {
    if (!view) return
    const current = view.state.doc.toString()
    // Only replace the document when the change came from somewhere other than
    // this editor. Writing back what a keystroke just produced would rebuild the
    // document on every character and lose the cursor.
    if (current === value) return
    view.dispatch({ changes: { from: 0, to: current.length, insert: value } })
  },
)

onBeforeUnmount(() => {
  view?.destroy()
  view = null
})
</script>

<template>
  <div ref="host" class="code-editor"></div>
</template>

<style scoped>
.code-editor {
  height: 100%;
  overflow: auto;
}

.code-editor :deep(.cm-editor) {
  height: 100%;
  font-size: 13px;
}

.code-editor :deep(.cm-scroller) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, 'Liberation Mono', monospace;
}
</style>
