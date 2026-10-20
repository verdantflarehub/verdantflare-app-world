<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { closeSamplePreview, openSamplePreview } from '../platform/sample-preview'
import { useHost } from '../platform/host'
import { createReadClient } from '../platform/client'
import { publicationIdentity } from '../platform/publication-client'
import type { AssetOpen } from '../platform/world-model'
import type { LearningDocument } from '../platform/learning-results'
import { sampleFileReference, sampleDownload, sampleKind, readSampleBytes, type SampleDownload } from '../platform/prompt-samples'
const props = defineProps<{ asset: AssetOpen; document: LearningDocument; reference: unknown; title: string }>()
const { appSDK } = useHost(), client = createReadClient(appSDK, 'world')
const identity = publicationIdentity(appSDK.context.get()), source = ref(''), text = ref(''), error = ref(''), loading = ref(false)
const kind = ref(''), ready = ref(false), metadata = ref<SampleDownload>()
let blob: Blob | undefined, controller = new AbortController(), generation = 0, hidden = false, disposed = false, revoked = false
const previewKey = {}
function clear() {
  generation++; controller.abort(); controller = new AbortController()
  if (source.value) URL.revokeObjectURL(source.value)
  source.value = ''; text.value = ''; blob = undefined; ready.value = false; loading.value = false; metadata.value = undefined
  closeSamplePreview(appSDK, previewKey)
}
async function load(bytes = false) {
  if (hidden || disposed || revoked || loading.value) return
  const current = ++generation; loading.value = true; error.value = ''
  const valid = () => !hidden && !disposed && !revoked && current === generation
  try {
    const read = metadata.value || await sampleDownload(client.call, props.asset, sampleFileReference(props.reference, props.document))
    if (!valid()) return
    metadata.value = read; kind.value = sampleKind(read.mime)
    if (!bytes && kind.value !== 'image') return
    const value = await readSampleBytes(read, controller.signal)
    if (!valid()) return
    if (kind.value === 'text') {
      const body = await value.text()
      if (!valid()) return
      text.value = body
    } else source.value = URL.createObjectURL(value)
    blob = value; ready.value = true
  } catch { if (valid()) error.value = '样例暂不可读取：文件、权限、格式或完整性检查未通过。可重试读取，原始引用仍保留。' }
  finally { if (valid()) loading.value = false }
}
async function preview() {
  if (!blob || kind.value !== 'image' || hidden || revoked || disposed) return
  if (!appSDK.media) { error.value = '当前宿主尚未提供图片放大能力。'; return }
  const current = generation
  try {
    await openSamplePreview(appSDK, previewKey, blob, props.title)
  } catch { if (current === generation && !hidden && !disposed) error.value = '图片放大暂不可用。' }
}
const stop = appSDK.lifecycle.subscribe(event => {
  if (event === 'hidden' || event === 'dispose') { hidden = true; clear() }
  else { hidden = false; void load() }
})
const stopContext = appSDK.context.subscribe(context => {
  if (publicationIdentity(context) === identity) return
  revoked = true; clear(); error.value = '会话已变化，请重新打开资产。'
})
onBeforeUnmount(() => { disposed = true; clear(); client.dispose(); stop(); stopContext() })
void load()
</script>
<template><div class="sample-media">
  <p v-if="loading" role="status">正在读取真实样例…</p>
  <button v-if="source && kind === 'image'" class="sample-image" :aria-label="`放大：${title}`" @click="preview"><img :src="source" :alt="title" @error="error='图片无法解码，请重新读取。'"></button>
  <video v-else-if="source && kind === 'video'" :src="source" controls playsinline preload="metadata" @error="error='当前浏览器无法播放此视频。'"/>
  <audio v-else-if="source && kind === 'audio'" :src="source" controls preload="metadata" @error="error='当前浏览器无法播放此音频。'"/>
  <pre v-else-if="ready && kind === 'text'">{{text}}</pre>
  <p v-if="error" role="alert">{{error}}</p>
  <button v-if="error && !revoked && !loading && !ready" class="btn" @click="load(true)">重新读取样例</button>
  <template v-else-if="metadata && !ready && !loading && !error">
    <p v-if="kind === 'unsupported'">此文件格式暂不支持预览，固定引用保留在执行记录中。</p>
    <button v-else class="btn" @click="load(true)">{{kind === 'audio' ? '加载音频样例' : kind === 'video' ? '加载视频样例' : '查看输出正文'}}</button>
  </template>
</div></template>
<style scoped>
.sample-media{min-width:0;padding:12px 0}.sample-media p{font-size:12px;color:var(--muted);line-height:1.7}.sample-image{display:block;width:100%;padding:0;border:1px solid var(--line);border-radius:8px;background:var(--shell-button);cursor:zoom-in}.sample-image img{display:block;width:100%;max-height:360px;object-fit:contain}video{display:block;width:100%;max-height:400px;border-radius:8px;background:var(--shell-button)}audio{width:100%}pre{white-space:pre-wrap;overflow-wrap:anywhere;font:inherit;font-size:13px;line-height:1.8;padding:16px;background:var(--shell-button);border-radius:8px}
</style>
