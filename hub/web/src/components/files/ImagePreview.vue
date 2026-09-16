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
    release()
    try {
      objectUrl = URL.createObjectURL(await readFileBytes(path))
      url.value = objectUrl
    } catch (err) {
      emit('notice', `Could not preview ${path}: ${String(err)}`, 'error')
    }
  },
  { immediate: true },
)

onBeforeUnmount(release)
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
