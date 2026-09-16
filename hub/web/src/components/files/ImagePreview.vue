<script setup lang="ts">
// Image preview. The bytes become a blob URL handed to an <img>, which never
// executes anything the file contains -- an SVG previewed here is a picture, not
// a document.
import { onBeforeUnmount, ref, watch } from 'vue'
import { readFileBytes } from '../../files/api'
import { basename } from '../../files/pathUtils'
import { MAX_IMAGE_PREVIEW_BYTES } from '../../files/preview'

const props = defineProps<{ path: string }>()
const emit = defineEmits<{
  (e: 'notice', text: string, level: 'error' | 'warning'): void
  // too-large says the bytes on disk are past the bound this component renders
  // within, whatever the listing said about the size when the tab was opened.
  (e: 'too-large', path: string, size: number): void
  // download asks the manager for the file, which is the only way out of a
  // preview that cannot render what it was given.
  (e: 'download'): void
}>()

const url = ref('')
// undecodable records that the bytes arrived and the image decoder refused them.
// A name is a guess about contents, and this is the guess being wrong: the file
// is named like an image and is not one -- a truncated download, a pointer file
// from a large-file store, something encrypted. The fetch cannot fail on that
// account, because the fetch succeeded; only rendering can, and nothing else in
// this component would notice.
//
// That a browser raises `error` at all for a blob URL that has been revoked is a
// claim about the platform, not something these tests can check -- they dispatch
// the event themselves. See design.md's residuals.
const undecodable = ref(false)
const image = ref<HTMLImageElement | null>(null)
let objectUrl: string | null = null
// loadAt is the ticket of the most recent load. A read that resolves after a
// newer one started -- or after the component is gone -- must not adopt its blob,
// because the handle to the URL it replaced would be lost and never revoked.
let loadAt = 0
// shownAt is the load the URL on screen came from, and the decode handler needs
// it for the same reason the fetch has a ticket: revoking a blob to make room for
// the next file aborts that file's decode, and the browser then raises `error`
// for a URL this component has already let go of.
let shownAt = -1

function release() {
  if (objectUrl !== null) {
    URL.revokeObjectURL(objectUrl)
    objectUrl = null
  }
  url.value = ''
  undecodable.value = false
  shownAt = -1
}

function onDecodeFailed(event: Event) {
  // Two guards, because that stale event can arrive at either moment. Before the
  // next file's bytes land, the ticket is what says so; after, `shownAt` matches
  // the live load and only the element does. The element is keyed on its URL, so
  // a superseded one is a different node rather than the same node re-pointed --
  // which is what makes identity a faithful answer to "is this event about what
  // is on screen now".
  if (shownAt !== loadAt) return
  if (event.target !== image.value) return
  undecodable.value = true
  emit(
    'notice',
    `${basename(props.path)} could not be rendered as an image; it is not one this browser can decode.`,
    'warning',
  )
}

watch(
  () => props.path,
  async (path) => {
    const attempt = ++loadAt
    release()
    try {
      const fetched = await readFileBytes(path, MAX_IMAGE_PREVIEW_BYTES)
      if (attempt !== loadAt) return
      if ('tooLarge' in fetched) {
        // The file is larger than the listing said, or larger than it was when
        // the listing was read. Rendering it would freeze the tab, so it is not
        // rendered -- and the manager is told, because a file that cannot be
        // previewed is presented as information with a download action rather
        // than as an empty frame.
        emit('too-large', path, fetched.tooLarge)
        return
      }
      objectUrl = URL.createObjectURL(fetched.blob)
      url.value = objectUrl
      shownAt = attempt
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
    <!--
      The bytes were fetched and the decoder refused them. Saying so and offering
      the download is the whole of the recovery: the alternative is an empty
      frame whose only text is the alt attribute, and no way to get the file.
    -->
    <div v-if="undecodable" class="image-preview__failed">
      <p class="image-preview__state">
        {{ basename(path) }} is named like an image, but its contents are not one
        this browser can decode.
      </p>
      <button
        class="image-preview__download"
        type="button"
        @click="emit('download')"
      >Download</button>
    </div>
    <img
      v-else-if="url"
      :key="url"
      ref="image"
      class="image-preview__img"
      :src="url"
      :alt="path"
      @error="onDecodeFailed"
    />
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

.image-preview__failed {
  display: flex;
  flex-direction: column;
  gap: 10px;
  align-items: center;
}

.image-preview__state {
  margin: 0;
  color: var(--th-warning);
  font-size: 12px;
  text-align: center;
}

.image-preview__download {
  padding: 3px 8px;
  color: var(--th-text-hi);
  font-size: 12px;
  background: var(--th-raised);
  border: 1px solid var(--th-border);
  border-radius: 4px;
  cursor: pointer;
}
</style>
