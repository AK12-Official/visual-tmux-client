<script setup lang="ts">
// Image preview. The bytes become a blob URL handed to an <img>, which never
// executes anything the file contains -- an SVG previewed here is a picture, not
// a document.
import { onBeforeUnmount, ref, watch } from 'vue'
import { readFileBytes } from '../../files/api'

const props = defineProps<{ path: string }>()
const emit = defineEmits<{ (e: 'notice', text: string, level: 'error' | 'warning'): void }>()

const url = ref('')
let objectUrl: string | null = null
// loadAt is the ticket of the most recent load. A read that resolves after a
// newer one started -- or after the component is gone -- must not adopt its blob,
// because the handle to the URL it replaced would be lost and never revoked.
let loadAt = 0

function release() {
  if (objectUrl !== null) {
    URL.revokeObjectURL(objectUrl)
    objectUrl = null
  }
  url.value = ''
}

watch(
  () => props.path,
  async (path) => {
    const attempt = ++loadAt
    release()
    try {
      const blob = await readFileBytes(path)
      if (attempt !== loadAt) return
      objectUrl = URL.createObjectURL(blob)
      url.value = objectUrl
    } catch (err) {
      if (attempt !== loadAt) return
      emit('notice', `Could not preview ${path}: ${String(err)}`, 'error')
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  // Anything still in flight belongs to a component that no longer exists.
  loadAt++
  release()
})
</script>

<template>
  <div class="image-preview">
    <img v-if="url" class="image-preview__img" :src="url" :alt="path" />
  </div>
</template>

<style scoped>
.image-preview {
  display: flex;
  align-items: center;
  justify-content: center;
  height: 100%;
  padding: 16px;
  overflow: auto;
}

.image-preview__img {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
}
</style>
