<script setup lang="ts">
import WorldConcept from '../components/WorldConcept.vue'
import { worldDomains, catalogTypes, catalogDomain, catalogQuery, assetDomain, assetTypeLabel } from '../platform/world-catalog'
import { promptProjectTarget } from '../platform/prompt-project-target'
import type { LearningResult } from '../platform/learning-results'
import AssetResults from '../components/AssetResults.vue'
import { publicationIdentity, publicationPrincipal } from '../platform/publication-client'
import { rememberPosition, recalledPosition } from '../platform/route-memory'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { callTool } from '../platform/mcp'
import { useHost } from '../platform/host'
import { isID, readAsset, readAssetList, type AssetItem, type AssetOpen } from '../platform/world-model'
import { projectTarget, useAssetTarget } from '../platform/route-links'

const route = useRoute(), router = useRouter()
const { appSDK, dashboardHost } = useHost()
const identity = publicationIdentity(appSDK.context.get())
const detail = computed(() => route.query.asset !== undefined)
const listRoute = computed(() => ({ path: route.path, query: { ...route.query, asset: undefined, version: undefined } }))
const assets = ref<AssetItem[]>([]), selectedAsset = ref<AssetOpen | null>(null)
const loading = ref(false), detailLoading = ref(false), error = ref(''), cursor = ref('')
const assetType = computed({
  get: () => typeof route.query.asset_type === 'string' ? route.query.asset_type : '',
  set: value => { void router.replace({ path: route.path, query: { ...route.query, asset_type: value || undefined, asset: undefined, version: undefined } }) },
})
const domain=computed(()=>catalogDomain(route.path))
const assetTypes=computed(()=>[{value:'',label:'全部类型'},...catalogTypes.filter(t=>!domain.value||(domain.value.types as readonly string[]).includes(t.value))])
const query=ref(typeof route.query.q==='string'?route.query.q:'')
const relation=computed({get:()=>typeof route.query.relation==='string'?route.query.relation:'',set:value=>{void router.replace({path:route.path,query:{...route.query,relation:value||undefined,asset:undefined,version:undefined}})}})
const filters=computed(()=>catalogQuery(route.path,route.query))
const filtered=computed(()=>Object.keys(filters.value).length>0)
function search(){void router.replace({path:route.path,query:{...route.query,q:query.value.trim()||undefined,asset:undefined,version:undefined}})}
function categoryPath(key:string){return {path:key?'/'+key:'/',query:{...route.query,asset_type:undefined,asset:undefined,version:undefined}}}
const library=ref<HTMLElement|null>(null)
function browse(){library.value?.scrollIntoView({behavior:matchMedia('(prefers-reduced-motion: reduce)').matches?'auto':'smooth',block:'start'});library.value?.focus({preventScroll:true})}

const scrollScope = JSON.stringify(publicationPrincipal(appSDK.context.get()))
let listGeneration = 0, detailGeneration = 0, alive = true, hidden = false
let navigationRequest: AbortController | undefined
const shortID = (value: string) => value ? `${value.slice(0, 8)}…` : '—'
const message = (cause: unknown) => cause instanceof Error ? cause.message : '数字世界暂时不可用。'
function rememberRoute(_to: unknown, from: {fullPath:string}) {
  rememberPosition(scrollScope, from.fullPath, dashboardHost.getScroll())
}
onBeforeRouteUpdate(rememberRoute);onBeforeRouteLeave(rememberRoute)
async function restore() { await nextTick(); if (alive && !hidden) dashboardHost.restoreScroll(recalledPosition(scrollScope, route.fullPath)) }
async function loadWorld(more = false) {
  if (!alive || hidden) return
  const generation = ++listGeneration
  loading.value = true; error.value = ''
  if (!more) { assets.value = []; cursor.value = '' }
  try {
    const args: Record<string, unknown> = { limit: 50, ...filters.value }
    if (more && cursor.value) args.cursor = cursor.value
    const data = readAssetList(await callTool('world.list', args))
    if (!alive || hidden || generation !== listGeneration) return
    if (more && data.next_cursor && data.next_cursor === cursor.value) throw new Error('分页没有继续，请刷新重试。')
    assets.value = [...new Map([...(more ? assets.value : []), ...data.items].map(item => [item.asset_id, item])).values()]
    cursor.value = data.next_cursor
  } catch (cause) { if (alive && generation === listGeneration) error.value = message(cause) }
  finally { if (alive && generation === listGeneration) { loading.value = false; void restore() } }
}
async function loadAsset() {
  const generation = ++detailGeneration
  selectedAsset.value = null; detailLoading.value = false
  if (!alive || hidden || route.query.asset == null) return
  const asset = route.query.asset, version = route.query.version
  if (!isID(asset) || !isID(version)) { error.value = '资产地址缺少有效的固定版本。'; return }
  detailLoading.value = true; error.value = ''
  try {
    const data = readAsset(await callTool('world.get', { asset_id: asset, asset_version_id: version }), asset, version)
    if (alive && !hidden && generation === detailGeneration) selectedAsset.value = data
  } catch (cause) { if (alive && generation === detailGeneration) error.value = message(cause) }
  finally { if (alive && generation === detailGeneration) { detailLoading.value = false; void restore() } }
}
function openAsset(item: AssetItem) { return router.push({ path: route.path, query: { ...route.query, asset: item.asset_id, version: item.head_asset_version_id } }) }
async function newProject(result:LearningResult,variant:string) {
  if (!alive || hidden || !selectedAsset.value) return
  navigationRequest?.abort(); const controller = new AbortController(); navigationRequest = controller
  try { const target=promptProjectTarget(selectedAsset.value,result,variant); await appSDK.capabilities.call('navigation.openApp', target, { signal: controller.signal }) }
  catch { if (!controller.signal.aborted) error.value = '无法打开提示词项目交接。请确认 Project 已安装且支持此入口；固定来源仍保留，可复制配置。' }
}
async function bringIntoProject() {
  if (!selectedAsset.value) return
  navigationRequest?.abort(); const controller = new AbortController(); navigationRequest = controller
  const target = useAssetTarget(selectedAsset.value.asset_id, selectedAsset.value.asset_version_id)
  try { await appSDK.capabilities.call('navigation.openApp', { appId: target.appId, path: target.path }, { signal: controller.signal }) }
  catch { if (!controller.signal.aborted) error.value = '无法打开项目选择，请确认 Project 已安装且当前身份有访问权限。所选资产版本已保留。' }
}
async function openSource() {
  if (!selectedAsset.value) return
  navigationRequest?.abort(); const controller = new AbortController(); navigationRequest = controller
  const source = selectedAsset.value.manifest.source
  if (source.kind === 'world_draft') return
  const target = projectTarget(source.project_id, source.project_revision_id)
  try { await appSDK.capabilities.call('navigation.openApp', { appId: target.appId, path: target.path }, { signal: controller.signal }) }
  catch { if (!controller.signal.aborted) error.value = '无法打开来源项目，请确认应用已安装且当前身份有访问权限。' }
}
function refresh() { void loadWorld(); void loadAsset() }
watch(filters, (value,old) => { query.value=typeof route.query.q==='string'?route.query.q:'';if(JSON.stringify(value)!==JSON.stringify(old))void loadWorld() })
watch(() => [route.query.asset, route.query.version], () => void loadAsset(), { immediate: true })
const unsubscribe = appSDK.lifecycle.subscribe(event => {
  if (event === 'hidden' || event === 'dispose') { rememberPosition(scrollScope, route.fullPath, dashboardHost.getScroll()); navigationRequest?.abort() }
  hidden = event === 'hidden'
  if (event === 'hidden' || event === 'dispose') { listGeneration++; detailGeneration++; loading.value = false; detailLoading.value = false }
  if (event === 'active') refresh()
})
const stopContext = appSDK.context.subscribe(c => { if(publicationIdentity(c)===identity)return;listGeneration++;detailGeneration++;assets.value=[];selectedAsset.value=null;navigationRequest?.abort();hidden=true;error.value='会话已变化，请重新打开数字世界。' })
onBeforeUnmount(() => { alive = false; listGeneration++; detailGeneration++; navigationRequest?.abort(); unsubscribe();stopContext() })
void loadWorld()
</script>

<template>
  <main class="main"><div class="workspace project-world-page">

    <RouterLink v-if="detail" class="back-link" :to="listRoute">← 返回数字世界资产</RouterLink>
    <header class="heading"><div class="heading-left"><div class="eyebrow">WORLD</div><h1>{{detail ? selectedAsset?.manifest.name || '资产详情' : domain?domain.label+'资产':'数字世界'}}</h1><p>{{detail?'查看实际成果、适用条件和固定版本，可直接复制提示词配置。':'从灵感到资产，从资产到下一部作品。'}}</p></div><div class="heading-actions"><template v-if="!detail"><RouterLink class="btn primary" to="/collections">＋ 收录内容</RouterLink><RouterLink class="btn" to="/learn">拆解作品</RouterLink></template><button class="btn" :disabled="loading || detailLoading" @click="refresh">刷新</button></div></header>
    <div v-if="error" class="state error-state" role="alert"><strong>暂时无法读取</strong><span>{{error}}</span><button class="btn" @click="refresh">重试</button></div>
    <template v-if="!detail">
      <WorldConcept v-if="!filtered" @browse="browse"/>
      <div class="asset-section-title"><div><span>YOUR CREATIVE ASSETS</span><h2 ref="library" tabindex="-1">{{domain?domain.label+'资产':'资产目录'}}</h2></div><RouterLink class="btn" to="/knowledge?asset_type=prompt">提示词资产 ↗</RouterLink></div>
      <form class="searchbar" @submit.prevent="search"><input v-model="query" type="search" maxlength="256" aria-label="搜索资产" placeholder="搜索资产名称、标签…"><button class="btn" :disabled="loading">搜索</button></form>
      <nav class="domain-filters" aria-label="资产领域"><RouterLink :to="categoryPath('')" :aria-current="!domain?'page':undefined">全部</RouterLink><RouterLink v-for="d in worldDomains" :key="d.key" :to="categoryPath(d.key)" :aria-current="domain?.key===d.key?'page':undefined">{{d.label}}</RouterLink></nav>
      <section class="toolbar"><label>具体类型<select v-model="assetType" aria-label="具体类型"><option v-for="item in assetTypes" :key="item.value" :value="item.value">{{item.label}}</option><option v-if="assetType&&!assetTypes.some(t=>t.value===assetType)" :value="assetType">{{assetTypeLabel(assetType)}}</option></select></label><label>来源<select v-model="relation" aria-label="来源"><option value="">全部来源</option><option value="curated_in">参考收录</option><option value="produced_in">原创与制作</option></select></label><RouterLink v-if="filtered" class="btn" to="/">清除筛选</RouterLink><span class="toolbar-note">已加载 {{assets.length}} 项{{cursor?' · 可继续加载':''}}</span></section>
      <div v-if="loading && !assets.length" class="state loading-state" role="status">正在读取资产…</div>
      <section v-else aria-label="可复用资产"><div v-if="assets.length === 0 && !error" class="state empty-state"><strong>没有匹配的资产</strong><span>可以调整分类或搜索，或先收录一份值得学习的内容。</span><RouterLink class="btn" to="/collections">收录内容</RouterLink></div><div class="asset-grid"><button v-for="asset in assets" :key="asset.asset_id" class="asset-card" @click="openAsset(asset)"><span class="card-art" aria-hidden="true">{{asset.asset_type==='prompt'?'提 · 示 · 词':assetDomain(asset.asset_type)?.glyph||'创 · 作'}}</span><span class="card-body"><small>{{assetDomain(asset.asset_type)?.label||'其他'}} · {{assetTypeLabel(asset.asset_type)}}</small><strong>{{asset.name}}</strong><span>{{asset.subjects.join('、')||'暂无标签'}}</span></span><span class="card-foot">查看成果 <span>↗</span></span></button></div><button v-if="cursor" class="btn detail-action" :disabled="loading" @click="loadWorld(true)">加载更多资产</button></section>
    </template>
    <template v-else>
      <div v-if="detailLoading" class="state" role="status">正在读取固定版本…</div>
      <template v-else-if="selectedAsset">
        <div class="asset-actions"><span class="version-pill">固定版本 {{shortID(selectedAsset.asset_version_id)}}</span><button class="btn" @click="bringIntoProject">选择项目并引用此版本</button></div>
        <AssetResults :key="selectedAsset.asset_id+':'+selectedAsset.asset_version_id" :asset="selectedAsset" @new-project="newProject"/>
        <details class="detail-panel"><summary>资产信息与已保存文件</summary>
          <dl class="facts"><dt>资产类型</dt><dd>{{selectedAsset.manifest.asset_type}}</dd><dt>主体</dt><dd>{{selectedAsset.manifest.subjects.join('、') || '未标主体'}}</dd><template v-if="selectedAsset.manifest.source.kind === 'world_draft'"><dt>来源工作区修订</dt><dd>{{shortID(selectedAsset.manifest.source.draft_id)}} · {{shortID(selectedAsset.manifest.source.draft_revision_id)}}</dd></template><template v-else><dt>来源项目修订</dt><dd>{{shortID(selectedAsset.manifest.source.project_id)}} · {{shortID(selectedAsset.manifest.source.project_revision_id)}}</dd></template></dl>
          <ul class="plain-list"><li v-for="file in selectedAsset.manifest.files" :key="file.file_id"><span>{{file.path}}</span><small>{{file.role}} · {{shortID(file.file_id)}}</small></li></ul><button v-if="selectedAsset.manifest.source.kind !== 'world_draft'" class="btn detail-action" @click="openSource">查看来源项目的固定修订</button>
        </details>
      </template>
    </template>
  </div></main>
</template>

<style scoped>
.heading-actions{display:flex;gap:10px;flex-wrap:wrap}.asset-section-title{display:flex;align-items:end;justify-content:space-between;gap:12px;margin:28px 0 20px}.asset-section-title>div>span{font:10px/1.5 ui-monospace,monospace;letter-spacing:1.5px;color:var(--muted)}.asset-section-title h2{font-size:22px;margin-top:5px;color:var(--ink);scroll-margin-top:24px}.searchbar{display:flex;gap:12px;margin:18px 0}.searchbar input{flex:1;min-width:0;padding:12px 16px;border:1px solid var(--line);background:var(--shell-panel);color:var(--ink);border-radius:8px}.domain-filters{display:flex;gap:8px;flex-wrap:wrap;margin:20px 0}.domain-filters a{padding:8px 18px;border:1px solid var(--line);border-radius:30px;color:var(--muted);font-size:13px}.domain-filters a[aria-current=page]{background:var(--paper);color:var(--button-ink);border-color:var(--paper)}.asset-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:20px}.asset-card{display:flex;flex-direction:column;min-width:0;text-align:left;border:1px solid var(--line);border-radius:12px;overflow:hidden;background:var(--shell-panel);color:var(--ink)}.asset-card:hover{border-color:var(--accent)}.card-art{height:160px;display:grid;place-items:center;background:var(--shell-button);color:var(--muted);font-size:23px;letter-spacing:8px;border-bottom:1px solid var(--line)}.card-body{display:grid;align-content:start;gap:14px;padding:22px;flex:1}.card-body small{font-size:11px;color:var(--accent)}.card-body strong{font-size:17px;line-height:1.6;overflow-wrap:anywhere}.card-body>span{font-size:12px;color:var(--muted);overflow-wrap:anywhere}.card-foot{display:flex;justify-content:space-between;padding:16px 22px;border-top:1px solid var(--line);font-size:12px}.card-foot>span{color:var(--accent)}@media(max-width:1100px){.asset-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}@media(max-width:700px){.asset-grid{grid-template-columns:1fr}.domain-filters a{padding:7px 12px}.heading-actions{margin-top:12px}}

.back-link{display:inline-block;margin-bottom:18px;color:var(--muted);font-size:12px}.asset-actions{display:flex;gap:16px;align-items:center;flex-wrap:wrap}.detail-panel summary{cursor:pointer;margin-bottom:18px;color:var(--muted);font-size:13px}
.project-world-page { max-width: 1380px; }
.eyebrow { color: var(--accent); font-size: 11px; letter-spacing: .14em; font-weight: 700; }
.state { display: flex; align-items: center; gap: 10px; padding: 22px; border: 1px dashed var(--line); border-radius: 12px; color: var(--muted); background: var(--shell-panel); }
.state strong { color: var(--ink); }
.error-state { border-color: color-mix(in srgb,var(--danger) 45%,var(--line)); color: var(--danger); margin-bottom: 16px; }
.loading-state { justify-content: center; margin-bottom: 16px; }
.empty-state { min-height: 120px; justify-content: center; flex-direction: column; text-align: center; }
.list-panel,.detail-panel { padding: 20px; border: 1px solid var(--line); border-radius: 14px; background: var(--shell-panel); }
.section-title { display: flex; align-items: center; justify-content: space-between; gap: 12px; margin-bottom: 14px; color: var(--ink); font-weight: 700; }
.section-title small,.toolbar-note { color: var(--muted); font-size: 12px; font-weight: 400; }
label { display: grid; gap: 6px; color: var(--muted); font-size: 12px; }
select { width: 100%; border: 1px solid var(--line); border-radius: 8px; background: var(--shell-button); color: var(--ink); padding: 9px 10px; font: inherit; font-size: 13px; }
.content-grid { display: grid; grid-template-columns: minmax(0,1.1fr) minmax(320px,.9fr); gap: 16px; }
.list-card { width: 100%; display: flex; align-items: center; gap: 12px; padding: 13px 12px; margin-top: 8px; border: 1px solid var(--line); border-radius: 10px; background: var(--shell-button); color: var(--ink); text-align: left; cursor: pointer; }
.list-card:hover,.list-card.selected { border-color: var(--accent); background: color-mix(in srgb,var(--accent) 8%,var(--shell-button)); }
.card-glyph { width: 32px; height: 32px; display: grid; place-items: center; border-radius: 8px; background: color-mix(in srgb,var(--accent) 12%,transparent); color: var(--accent); font-size: 17px; }
.card-main { min-width: 0; flex: 1; display: grid; gap: 4px; }
.card-main strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.card-main small,.card-id,.plain-list small { color: var(--muted); font-size: 11px; }
.card-id,.version-pill { font-family: ui-monospace,SFMono-Regular,Consolas,monospace; font-size: 11px; color: var(--muted); }
.toolbar { display: flex; align-items: end; gap: 18px; margin-bottom: 16px; }
.toolbar label { width: 220px; }
.facts { display: grid; grid-template-columns: 120px minmax(0,1fr); gap: 9px 12px; margin: 0 0 20px; font-size: 12px; }
.facts dt { color: var(--muted); }
.facts dd { color: var(--ink); margin: 0; overflow-wrap: anywhere; }
.subheading { margin: 16px 0 8px; color: var(--muted); font-size: 12px; font-weight: 700; }
.plain-list { display: grid; gap: 7px; margin: 0; padding: 0; list-style: none; }
.plain-list li { display: flex; justify-content: space-between; gap: 10px; padding: 9px 10px; border: 1px solid var(--line); border-radius: 8px; font-size: 12px; }
.detail-action { margin-top: 18px; width: 100%; }
@media(max-width:800px) { .content-grid { grid-template-columns:1fr; } }
</style>
