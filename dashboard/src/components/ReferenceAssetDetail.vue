<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useHost } from '../platform/host'
import { assetFileText, assetFileURL } from '../platform/asset-media'
import { readReferenceDomain, referenceViews, RIGHTS_LABELS, type ReferenceDomain } from '../platform/reference-assets'
import type { AssetFile, AssetOpen } from '../platform/world-model'
import WorldTrail, { type TrailItem } from './WorldTrail.vue'
// Detail view for pose-reference / costume-reference assets: large image, view
// strip for poses, overview, source and rights, and the domain JSON itself.
const props = defineProps<{ asset: AssetOpen; parents: TrailItem[] }>()
const { appSDK } = useHost()
const call = (n: string, a: Record<string, unknown>) => appSDK.capabilities.call(n, a as never)
const scope = computed(() => ({ asset_id: props.asset.asset_id, asset_version_id: props.asset.asset_version_id }))
const files = computed(() => new Map(props.asset.manifest.files.map(f => [f.path, f])))
const domain = ref<ReferenceDomain | null>(null), raw = ref(''), error = ref(''), loading = ref(true)
const urls = ref<Record<string, string>>({}), current = ref('')
let alive = true
onBeforeUnmount(() => { alive = false })

async function url(file: AssetFile | undefined) {
  if (!file || urls.value[file.path]) return
  try { const path = await assetFileURL(call, scope.value, file); if (alive) urls.value = { ...urls.value, [file.path]: path } } catch { /* the slot keeps its placeholder */ }
}
async function load() {
  loading.value = true; error.value = ''; domain.value = null
  try {
    const file = props.asset.manifest.files.find(f => f.role === 'domain-json')
    if (!file) throw new Error('这份资产缺少领域文件。')
    const text = await assetFileText(call, scope.value, file)
    const parsed = readReferenceDomain(JSON.parse(text))
    if (!alive) return
    raw.value = JSON.stringify(JSON.parse(text), null, 2); domain.value = parsed
    const views = referenceViews(parsed)
    current.value = views.find(v => v.key === parsed.default)?.key || views[0]?.key || ''
    for (const v of views) { void url(files.value.get(v.thumb || v.image)) }
    void url(files.value.get(views.find(v => v.key === current.value)?.image || ''))
  } catch (e) { if (alive) error.value = e instanceof Error ? e.message : '无法读取资产。' }
  finally { if (alive) loading.value = false }
}
const views = computed(() => domain.value ? referenceViews(domain.value) : [])
const active = computed(() => views.value.find(v => v.key === current.value))
watch(active, v => { if (v) void url(files.value.get(v.image)) })
void load()
</script>
<template>
  <section class="reference-detail" aria-label="资产详情">
    <WorldTrail :items="[...parents, { label: asset.manifest.name }]" />
    <header class="head">
      <div>
        <h2>{{ domain?.title || asset.manifest.name }}</h2>
        <p>{{ domain?.summary || asset.manifest.subjects.join(' · ') }}</p>
      </div>
      <slot />
    </header>
    <div v-if="loading" class="state" role="status">正在读取固定版本…</div>
    <div v-else-if="error" class="state error" role="alert"><p>{{ error }}</p><button class="btn" @click="load">重新读取</button></div>
    <div v-else-if="domain" class="layout">
      <div class="panel media">
        <div class="stage" :class="{ loading: !urls[active?.image || ''] }">
          <img v-if="active && urls[active.image]" :src="urls[active.image]" :alt="`${domain.title} ${active.label}`">
        </div>
        <div v-if="views.length > 1" class="strip" role="tablist" aria-label="视角">
          <button v-for="v in views" :key="v.key" role="tab" :aria-selected="v.key === current" :class="{ on: v.key === current }" @click="current = v.key">
            <span class="thumb"><img v-if="urls[v.thumb || v.image]" :src="urls[v.thumb || v.image]" alt=""></span>
            <span>{{ v.label }}</span>
          </button>
        </div>
      </div>
      <div class="side">
        <div class="panel">
          <h3>概览</h3>
          <dl><template v-for="row in domain.overview" :key="row[0]"><dt>{{ row[0] }}</dt><dd>{{ row[1] }}</dd></template>
            <dt>固定版本</dt><dd class="mono">{{ asset.asset_version_id }}</dd></dl>
        </div>
        <div class="panel">
          <h3>来源与权利</h3>
          <dl>
            <dt>来源</dt><dd>{{ domain.source.collection }}</dd>
            <template v-if="domain.source.author"><dt>作者</dt><dd>{{ domain.source.author }}</dd></template>
            <dt>权利</dt><dd><span class="pill" :class="{ warn: domain.source.rights === 'third-party-internal-only' }">{{ RIGHTS_LABELS[domain.source.rights] || domain.source.rights }}</span></dd>
            <dt>来源条目</dt><dd class="mono">{{ domain.source.source_ids.join('、') }}</dd>
          </dl>
        </div>
        <details class="panel">
          <summary>领域 JSON</summary>
          <pre>{{ raw }}</pre>
        </details>
      </div>
    </div>
  </section>
</template>
<style scoped>
.reference-detail{margin:0 0 24px;color:var(--ink);min-width:0}
.head{display:flex;justify-content:space-between;align-items:flex-end;gap:16px;flex-wrap:wrap;margin-bottom:18px}
h2{font-size:21px;margin:0 0 6px}.head p{margin:0;font-size:12px;color:var(--muted)}
.layout{display:grid;grid-template-columns:minmax(0,1.25fr) minmax(0,1fr);gap:20px;align-items:start}
.panel{border:1px solid var(--line);border-radius:12px;background:var(--shell-panel);padding:16px}
.panel h3{font-size:14px;margin:0 0 10px}
.side{display:grid;gap:16px;min-width:0}
.stage{aspect-ratio:3/4;max-height:680px;width:100%;display:grid;place-items:center;background:#fff;border-radius:10px;overflow:hidden}
.stage img{width:100%;height:100%;object-fit:contain}
.stage.loading,.thumb:empty{background:linear-gradient(105deg,var(--shell-button) 20%,color-mix(in srgb,var(--accent) 10%,var(--shell-button)) 48%,var(--shell-button) 76%);background-size:220% 100%;animation:shimmer 1.4s linear infinite}
.strip{display:grid;grid-template-columns:repeat(5,minmax(0,1fr));gap:8px;margin-top:12px}
.strip button{display:grid;gap:4px;padding:0;border:1px solid var(--line);border-radius:8px;background:#fff;color:var(--muted);font:inherit;font-size:11px;cursor:pointer;overflow:hidden;padding-bottom:4px}
.strip button.on{border-color:var(--accent);color:var(--accent);font-weight:600}
.strip button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
.thumb{aspect-ratio:3/4;display:block}.thumb img{width:100%;height:100%;object-fit:contain;display:block}
dl{display:grid;grid-template-columns:84px minmax(0,1fr);gap:7px 12px;margin:0;font-size:12.5px}
dt{color:var(--muted)}dd{margin:0;overflow-wrap:anywhere}.mono{font:11.5px/1.6 ui-monospace,monospace}
.pill{display:inline-block;padding:1px 9px;border-radius:99px;background:color-mix(in srgb,var(--accent) 12%,transparent);color:var(--accent);font-size:11.5px;font-weight:600}
.pill.warn{background:color-mix(in srgb,#b58032 15%,transparent);color:#b58032}
summary{cursor:pointer;font-size:14px;font-weight:600}
pre{margin:10px 0 0;padding:12px;border-radius:8px;background:var(--shell-button);font-size:11.5px;white-space:pre-wrap;overflow-wrap:anywhere;max-height:480px;overflow:auto}
.state{padding:26px;border:1px dashed var(--line);border-radius:10px;color:var(--muted)}.error{color:var(--danger)}
@keyframes shimmer{from{background-position:120% 0}to{background-position:-120% 0}}
@media (prefers-reduced-motion:reduce){.stage.loading,.thumb:empty{animation:none}}
@media (max-width:960px){.layout{grid-template-columns:1fr}}
</style>
