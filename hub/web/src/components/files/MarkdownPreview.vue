<script setup lang="ts">
// Rendered Markdown. renderMarkdown sanitizes the parsed output before it gets
// here, which is what makes v-html safe: the hub serves whatever bytes the file
// holds, and a Markdown file in this workflow is frequently attacker-influenced.
import { computed } from 'vue'
import { renderMarkdown } from '../../files/preview'

const props = defineProps<{ source: string }>()
const rendered = computed(() => renderMarkdown(props.source))
</script>

<template>
  <div class="markdown-preview">
    <p v-if="rendered.truncated" class="markdown-preview__notice" role="status">
      This file is larger than the preview limit, so only the beginning is rendered. Switch to
      Source to see the rest.
    </p>
    <div class="markdown-preview__body" v-html="rendered.html"></div>
  </div>
</template>

<style scoped>
.markdown-preview {
  height: 100%;
  padding: 16px 20px;
  overflow: auto;
  font-size: 13px;
  line-height: 1.6;
}

.markdown-preview__notice {
  margin: 0 0 12px;
  padding: 8px 10px;
  border: 1px solid var(--th-warning);
  border-radius: 4px;
  color: var(--th-warning);
  font-size: 12px;
}

.markdown-preview__body :deep(h1),
.markdown-preview__body :deep(h2),
.markdown-preview__body :deep(h3) {
  margin-top: 1.2em;
  margin-bottom: 0.5em;
  line-height: 1.3;
}

.markdown-preview__body :deep(code) {
  padding: 1px 4px;
  background: var(--th-surface);
  border-radius: 3px;
  font-family: var(--app-code-font);
}

.markdown-preview__body :deep(pre) {
  padding: 10px;
  overflow-x: auto;
  background: var(--th-surface);
  border-radius: 4px;
}

.markdown-preview__body :deep(a) {
  color: var(--th-accent);
}

.markdown-preview__body :deep(table) {
  border-collapse: collapse;
}

.markdown-preview__body :deep(th),
.markdown-preview__body :deep(td) {
  padding: 4px 8px;
  border: 1px solid var(--th-border);
}

.markdown-preview__body :deep(img) {
  max-width: 100%;
}
</style>
