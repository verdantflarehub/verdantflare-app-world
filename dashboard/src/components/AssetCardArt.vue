<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useHost } from '../platform/host'
import { assetFileURL } from '../platform/asset-media'
import type { AssetItem } from '../platform/world-model'
// Catalog card media: the fixed version's preview image, loaded once the card
// scrolls into view. Assets without an image keep a neutral picture mark; the
// image area never carries text.
const props = defineProps<{ asset: AssetItem }>()
const { appSDK } = useHost()
const root = ref<HTMLElement>(), url = ref(''), failed = ref(false)
let observer: IntersectionObserver | undefined, alive = true
async function load() {
  const file = props.asset.preview
  if (!file) return
  try {
    const path = await assetFileURL((n, a) => appSDK.capabilities.call(n, a as never), { asset_id: props.asset.asset_id, asset_version_id: props.asset.head_asset_version_id }, file)
    if (alive) url.value = path
  } catch { if (alive) failed.value = true }
}
onMounted(() => {
  if (!props.asset.preview || !root.value) return
  observer = new IntersectionObserver(entries => { if (entries.some(e => e.isIntersecting)) { observer?.disconnect(); void load() } }, { rootMargin: '200px' })
  observer.observe(root.value)
})
onBeforeUnmount(() => { alive = false; observer?.disconnect() })
</script>
<template>
  <span ref="root" class="card-art" :class="{ media: !!asset.preview, loading: !!asset.preview && !url && !failed }" aria-hidden="true">
    <img v-if="url" :src="url" alt="" loading="lazy" @error="failed = true; url = ''">
    <svg v-else viewBox="0 0 24 24" width="30" height="30" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="9" cy="10" r="2"/><path d="m21 16-5-5-9 9"/></svg>
  </span>
</template>
<style scoped>
.card-art{aspect-ratio:4/3;display:grid;place-items:center;background:var(--shell-button);color:var(--muted);border-bottom:1px solid var(--line);overflow:hidden}
.card-art svg{opacity:.45}
.card-art.media{background:#fff}
.card-art img{width:100%;height:100%;object-fit:contain;display:block}
.card-art.loading{background:linear-gradient(105deg,var(--shell-button) 20%,color-mix(in srgb,var(--accent) 10%,var(--shell-button)) 48%,var(--shell-button) 76%);background-size:220% 100%;animation:shimmer 1.4s linear infinite}
@keyframes shimmer{from{background-position:120% 0}to{background-position:-120% 0}}
@media (prefers-reduced-motion:reduce){.card-art.loading{animation:none}}
</style>
