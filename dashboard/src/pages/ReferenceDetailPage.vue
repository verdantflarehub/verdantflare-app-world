<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { callTool } from '../platform/mcp'
import { useHost } from '../platform/host'
import { isID, readAsset, readAssetList, type AssetFile, type AssetItem, type AssetOpen } from '../platform/world-model'
import { assetFileText, assetFileURL } from '../platform/asset-media'
import { readReferenceDomain, RIGHTS_LABELS, type ReferenceDomain } from '../platform/reference-assets'
import { getLastList, recall, type GalleryKind } from '../platform/reference-gallery'
import { useAssetTarget } from '../platform/route-links'
import WorldTrail from '../components/WorldTrail.vue'
// Pose / costume detail sub-page: breadcrumb back to the remembered list,
// previous/next within that list, view strip, overview, rights and related.
const props = defineProps<{ kind: GalleryKind }>()
const route = useRoute(), router = useRouter(), { appSDK, dashboardHost } = useHost()
const call = (n: string, a: Record<string, unknown>) => appSDK.capabilities.call(n, a as never)
const asset = ref<AssetOpen | null>(null), domain = ref<ReferenceDomain | null>(null), error = ref(''), loading = ref(true), notice = ref('')
const urls = ref<Record<string, string>>({}), related = ref<AssetItem[]>([]), relatedUrls = ref<Record<string, string>>({})
let generation = 0, alive = true
onBeforeUnmount(() => { alive = false; generation++; window.removeEventListener('keydown', keys) })

const assetID = computed(() => String(route.params.assetId || '')), versionID = computed(() => typeof route.query.version === 'string' ? route.query.version : '')
const title = computed(() => props.kind === 'pose' ? '姿势' : '服装')
const list = computed(() => getLastList(props.kind)), listItems = computed(() => list.value ? recall(list.value.key)?.items || [] : [])
const index = computed(() => listItems.value.findIndex(i => i.asset_id === assetID.value))
const total = computed(() => list.value ? recall(list.value.key)?.total || listItems.value.length : 0)
const views = computed(() => domain.value?.views || [])
const current = computed(() => { const v = typeof route.query.v === 'string' ? route.query.v : ''; return views.value.find(x => x.key === v) || views.value.find(x => x.key === domain.value?.default) || views.value[0] })
const files = computed(() => new Map((asset.value?.manifest.files || []).map(f => [f.path, f])))
const scope = computed(() => ({ asset_id: assetID.value, asset_version_id: versionID.value }))

async function url(file: AssetFile | undefined) {
  if (!file || urls.value[file.path]) return
  try { const path = await assetFileURL(call, scope.value, file); if (alive) urls.value = { ...urls.value, [file.path]: path } } catch { /* slot keeps its placeholder */ }
}
async function load() {
  const current = ++generation
  loading.value = true; error.value = ''; asset.value = null; domain.value = null; urls.value = {}; related.value = []
  try {
    if (!isID(assetID.value) || !isID(versionID.value)) throw new Error('资产地址无效。')
    const opened = readAsset(await callTool('world.get', { asset_id: assetID.value, asset_version_id: versionID.value }), assetID.value, versionID.value)
    const file = opened.manifest.files.find(f => f.role === 'domain-json')
    if (!file) throw new Error('这份资产缺少领域文件。')
    const parsed = readReferenceDomain(JSON.parse(await assetFileText(call, scope.value, file)))
    if (!alive || current !== generation) return
    asset.value = opened; domain.value = parsed
    for (const v of parsed.views) void url(files.value.get(v.thumb || v.image))
    void loadRelated(parsed)
  } catch (e) { if (alive && current === generation) error.value = e instanceof Error ? e.message : '无法读取资产。' }
  finally { if (alive && current === generation) loading.value = false; dashboardHost.restoreScroll(0) }
}
async function loadRelated(d: ReferenceDomain) {
  const tag = (label: string) => d.overview.find(r => r[0] === label)?.[1] || ''
  const subjects = d.kind === 'pose-reference' ? [`体系:${tag('体系')}`, `动作:${tag('动作分类')}`]
    : tag('资产类型').endsWith('单品') ? ['范围:单品', `类型:${tag('分类')}`] : ['范围:穿搭', `分类:${tag('分类')}`]
  try {
    const page = readAssetList(await callTool('world.list', { domain: props.kind, subjects: subjects.filter(s => !s.endsWith(':')), limit: 7 }))
    if (!alive) return
    related.value = page.items.filter(i => i.asset_id !== assetID.value).slice(0, 6)
    for (const item of related.value) if (item.preview) assetFileURL(call, { asset_id: item.asset_id, asset_version_id: item.head_asset_version_id }, item.preview).then(u => { if (alive) relatedUrls.value = { ...relatedUrls.value, [item.asset_id]: u } }, () => {})
  } catch { related.value = [] }
}
function go(item: AssetItem | undefined) {
  if (!item) return
  void router.replace({ path: `/${props.kind}/asset/${item.asset_id}`, query: { version: item.head_asset_version_id, ...(typeof route.query.v === 'string' ? { v: route.query.v } : {}) } })
}
function setView(key: string) { void router.replace({ path: route.path, query: { ...route.query, v: key } }) }
function back() { void router.push({ path: `/${props.kind}`, query: list.value?.query || {} }) }
async function bringIntoProject() {
  const target = useAssetTarget(assetID.value, versionID.value)
  try { await appSDK.capabilities.call('navigation.openApp', { appId: target.appId, path: target.path }) }
  catch { notice.value = '无法打开项目选择，请确认 Project 已安装且当前身份有访问权限。' }
}
function keys(e: KeyboardEvent) {
  if ((e.target as HTMLElement)?.matches?.('input,textarea,select')) return
  if (e.key === 'Escape') back()
  if (e.key === 'ArrowLeft' && index.value > 0) go(listItems.value[index.value - 1])
  if (e.key === 'ArrowRight' && index.value >= 0) go(listItems.value[index.value + 1])
}
window.addEventListener('keydown', keys)
watch(current, v => { if (v) void url(files.value.get(v.image)) })
watch(() => [assetID.value, versionID.value], () => void load(), { immediate: true })
</script>
<template>
  <main class="detail-page" aria-label="资产详情">
    <WorldTrail :items="[{ label: '数字世界', to: '/' }, { label: title, to: { path: `/${kind}`, query: list?.query || {} } }, { label: domain?.title || '资产详情' }]" />
    <div v-if="loading" class="state" role="status">正在读取固定版本…</div>
    <div v-else-if="error" class="state error" role="alert"><strong>{{ error }}</strong><span><button class="btn" @click="load">重新读取</button> <button class="btn" @click="back">返回{{ title }}</button></span></div>
    <template v-else-if="domain && asset">
      <header class="head">
        <div><h1>{{ domain.title }}</h1><p>{{ domain.summary }}</p></div>
        <div class="actions">
          <span v-if="index >= 0" class="pager"><button class="btn" :disabled="index <= 0" @click="go(listItems[index - 1])">‹ 上一个</button><span>{{ index + 1 }} / {{ total }}</span><button class="btn" :disabled="index >= listItems.length - 1" @click="go(listItems[index + 1])">下一个 ›</button></span>
          <button class="btn primary" @click="bringIntoProject">选择项目并引用此版本</button>
        </div>
      </header>
      <p v-if="notice" class="notice" role="status">{{ notice }}</p>
      <div class="layout">
        <section class="stage" aria-label="大图">
          <div class="big" :class="{ loading: !current || !urls[current.image] }"><img v-if="current && urls[current.image]" :src="urls[current.image]" :alt="`${domain.title} ${current.label}`"></div>
          <div v-if="views.length > 1" class="views" role="tablist" aria-label="视角">
            <button v-for="v in views" :key="v.key" role="tab" :aria-selected="v.key === current?.key" @click="setView(v.key)"><span class="thumb"><img v-if="urls[v.thumb || v.image]" :src="urls[v.thumb || v.image]" alt=""></span>{{ v.label }}</button>
          </div>
        </section>
        <aside class="side">
          <section class="panel"><h3>概览</h3><dl><template v-for="row in domain.overview" :key="row[0]"><dt>{{ row[0] }}</dt><dd>{{ row[1] }}</dd></template><dt>固定版本</dt><dd class="mono">{{ asset.asset_version_id }}</dd></dl></section>
          <section class="panel"><h3>来源与权利</h3><dl><dt>来源</dt><dd>{{ domain.source.collection }}</dd><template v-if="domain.source.author"><dt>作者</dt><dd>{{ domain.source.author }}</dd></template><dt>权利</dt><dd><span class="pill" :class="{ warn: domain.source.rights === 'third-party-internal-only' }">{{ RIGHTS_LABELS[domain.source.rights] || domain.source.rights }}</span></dd></dl></section>
          <section v-if="related.length" class="panel"><h3>{{ kind === 'pose' ? '同类动作' : '同分类推荐' }}</h3><div class="related"><button v-for="r in related" :key="r.asset_id" :title="r.subjects[0] || r.name" @click="go(r)"><img v-if="relatedUrls[r.asset_id]" :src="relatedUrls[r.asset_id]" alt=""></button></div></section>
          <p class="hint">← → 切换上一个／下一个，Esc 返回列表</p>
        </aside>
      </div>
    </template>
  </main>
</template>
<style scoped>
.detail-page{color:var(--ink);min-width:0}
.head{display:flex;justify-content:space-between;align-items:flex-end;gap:16px;flex-wrap:wrap;margin-bottom:16px}.head h1{margin:0;font-size:21px;line-height:1.4}.head p{margin:2px 0 0;color:var(--muted);font-size:13px}
.actions{display:flex;gap:8px;align-items:center;flex-wrap:wrap}.pager{display:inline-flex;gap:6px;align-items:center;color:var(--muted);font-size:12px}
.layout{display:grid;grid-template-columns:minmax(0,1.35fr) minmax(300px,1fr);gap:20px;align-items:start}
.stage,.panel{border:1px solid var(--line);border-radius:12px;background:var(--shell-panel)}.stage{padding:14px;background:#fff}
.big{display:grid;place-items:center;min-height:360px}.big img{display:block;width:100%;max-height:max(360px,calc(100vh - 340px));object-fit:contain}
.big.loading,.thumb:empty,.related button:empty{background:linear-gradient(105deg,var(--shell-button) 20%,color-mix(in srgb,var(--accent) 10%,var(--shell-button)) 48%,var(--shell-button) 76%);background-size:220% 100%;animation:shimmer 1.4s linear infinite}
.views{display:grid;grid-template-columns:repeat(5,minmax(0,1fr));gap:8px;margin-top:12px}.views button{display:grid;gap:2px;padding:0 0 4px;border:1.5px solid var(--line);border-radius:8px;background:#fff;color:var(--muted);font:inherit;font-size:11.5px;cursor:pointer;overflow:hidden}.views button[aria-selected=true]{border-color:var(--accent);color:var(--accent);font-weight:600}.thumb{display:block;height:96px}.thumb img{width:100%;height:100%;object-fit:contain}
.side{display:grid;gap:14px;min-width:0}.panel{padding:14px 16px}.panel h3{margin:0 0 10px;font-size:14px}
dl{display:grid;grid-template-columns:76px minmax(0,1fr);gap:7px 12px;margin:0;font-size:12.5px}dt{color:var(--muted)}dd{margin:0;overflow-wrap:anywhere}.mono{font:11.5px/1.6 ui-monospace,monospace}
.pill{display:inline-block;padding:1px 9px;border-radius:99px;background:color-mix(in srgb,var(--accent) 12%,transparent);color:var(--accent);font-size:11.5px;font-weight:600}.pill.warn{background:color-mix(in srgb,#b58032 15%,transparent);color:#b58032}
.related{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:8px}.related button{padding:0;border:1px solid var(--line);border-radius:8px;overflow:hidden;background:#fff;aspect-ratio:3/4;cursor:pointer}.related img{width:100%;height:100%;object-fit:cover;object-position:top;display:block}
.views button:focus-visible,.related button:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
.hint{color:var(--muted);font-size:12px;margin:0}.notice{color:var(--danger);font-size:12.5px}
.state{display:grid;gap:8px;justify-items:center;padding:40px;border:1px dashed var(--line);border-radius:12px;color:var(--muted)}.state.error strong{color:var(--danger)}
@keyframes shimmer{from{background-position:120% 0}to{background-position:-120% 0}}
@media (prefers-reduced-motion:reduce){.big.loading,.thumb:empty,.related button:empty{animation:none}}
@media (max-width:1100px){.layout{grid-template-columns:1fr}}
</style>
