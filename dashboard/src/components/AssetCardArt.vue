<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useHost } from '../platform/host'
import { createReadClient } from '../platform/client'
import { publicationIdentity } from '../platform/publication-client'
import { assetFileURL } from '../platform/asset-media'
import { readAsset, type AssetItem } from '../platform/world-model'
import { loadAssetResults } from '../platform/learning-results'
import { producedMedia } from '../platform/produced-media'
import { readSampleBytes, sampleDownload, sampleFileReference, sampleKind } from '../platform/prompt-samples'
import { queuePreview } from '../platform/preview-queue'
const props = defineProps<{ asset: AssetItem }>()
const { appSDK } = useHost(), client = createReadClient(appSDK, 'world')
const identity = publicationIdentity(appSDK.context.get())
const root = ref<HTMLElement>(), video = ref<HTMLVideoElement>(), url = ref(''), source = ref(''), failed = ref(false)
const isShot = computed(() => props.asset.asset_type === 'shot' || props.asset.asset_type === 'camera_animation')
let observer: IntersectionObserver | undefined, preference: MediaQueryList | undefined
let alive = true, visible = false, hidden = false, revoked = false, generation = 0, controller = new AbortController()
function clear(clearPoster = false) {
  generation++; controller.abort(); controller = new AbortController()
  video.value?.pause()
  if (source.value) URL.revokeObjectURL(source.value)
  source.value = ''
  if (clearPoster) { url.value = ''; failed.value = false }
}
async function load() {
  if (!alive || !visible || hidden || revoked || document.hidden) return
  const current = ++generation, signal = controller.signal, item = props.asset
  const valid = () => alive && visible && !hidden && !revoked && !document.hidden && current === generation && !signal.aborted
  try {
    await queuePreview(async () => {
      if (!valid()) return
      const scope = { asset_id: item.asset_id, asset_version_id: item.head_asset_version_id }
      if (item.preview && !url.value) {
        const path = await assetFileURL(client.call, scope, item.preview)
        if (!valid()) return
        url.value = path
      }
      if (!isShot.value || preference?.matches || source.value || !valid()) return
      const asset = readAsset(await client.call('world.get', scope), scope.asset_id, scope.asset_version_id)
      if (!valid()) return
      const result = await loadAssetResults(client.call, asset, signal)
      if (!valid() || !result) return
      const media = result.results.filter(r => r.type_key === 'shot' || r.type_key === 'camera_animation').map(r => producedMedia(r, asset)).find(Boolean)
      if (!media) return
      const read = await sampleDownload(client.call, asset, sampleFileReference(media.clips[0], result))
      if (!valid() || sampleKind(read.mime) !== 'video') return
      const bytes = await readSampleBytes(read, signal)
      if (!valid()) return
      source.value = URL.createObjectURL(bytes)
      await nextTick()
      if (valid()) { video.value!.muted = true; await video.value!.play().catch(() => {}) }
    }, signal)
  } catch { if (valid()) failed.value = true }
}
function sync() {
  clear()
  if (visible && !hidden && !document.hidden && !revoked) void load()
}
function contextChanged(context: Parameters<typeof publicationIdentity>[0]) {
  if (publicationIdentity(context) !== identity) { revoked = true; clear(true) }
}
const stop = appSDK.lifecycle.subscribe(event => {
  hidden = event === 'hidden' || event === 'dispose'
  if (event === 'dispose') revoked = true
  if (hidden) clear(true); else sync()
})
const stopContext = appSDK.context.subscribe(contextChanged)
watch(() => [props.asset.asset_id, props.asset.head_asset_version_id], () => { clear(true); void load() })
onMounted(() => {
  preference = window.matchMedia('(prefers-reduced-motion: reduce)')
  preference.addEventListener('change', sync)
  document.addEventListener('visibilitychange', sync)
  observer = new IntersectionObserver(entries => {
    visible = entries.some(e => e.isIntersecting)
    sync()
  }, { threshold: .15 })
  if (root.value) observer.observe(root.value)
})
onBeforeUnmount(() => {
  alive = false; clear(true); observer?.disconnect(); preference?.removeEventListener('change', sync)
  document.removeEventListener('visibilitychange', sync); stop(); stopContext(); client.dispose()
})
</script>
<template>
  <span ref="root" class="card-art" :class="{ media: !!url || !!source, loading: !!asset.preview && !url && !failed }" aria-hidden="true">
    <video v-if="source" ref="video" :src="source" :poster="url || undefined" muted loop playsinline preload="none" @error="clear()"/>
    <img v-else-if="url" :src="url" alt="" loading="lazy" @error="failed = true; url = ''">
    <svg v-else viewBox="0 0 24 24" width="30" height="30" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="9" cy="10" r="2"/><path d="m21 16-5-5-9 9"/></svg>
  </span>
</template>
<style scoped>
.card-art{aspect-ratio:4/3;display:grid;place-items:center;background:var(--shell-button);color:var(--muted);border-bottom:1px solid var(--line);overflow:hidden}
.card-art svg{opacity:.45}.card-art.media{background:#fff}
.card-art img,.card-art video{width:100%;height:100%;object-fit:contain;display:block;pointer-events:none}
.card-art.loading{background:linear-gradient(105deg,var(--shell-button) 20%,color-mix(in srgb,var(--accent) 10%,var(--shell-button)) 48%,var(--shell-button) 76%);background-size:220% 100%;animation:shimmer 1.4s linear infinite}
@keyframes shimmer{from{background-position:120% 0}to{background-position:-120% 0}}
@media (prefers-reduced-motion:reduce){.card-art.loading{animation:none}}
</style>
