<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { callTool } from '../platform/mcp'
import { useHost } from '../platform/host'
import { isID, readAsset, readAssetList, type AssetItem, type AssetOpen } from '../platform/world-model'
import { projectTarget, useAssetTarget } from '../platform/route-links'

const route = useRoute(), router = useRouter()
const { appSDK, dashboardHost } = useHost()
const assets = ref<AssetItem[]>([]), selectedAsset = ref<AssetOpen | null>(null)
const loading = ref(false), detailLoading = ref(false), error = ref(''), cursor = ref('')
const assetType = computed({
  get: () => typeof route.query.asset_type === 'string' ? route.query.asset_type : '',
  set: value => { void router.replace({ path: '/', query: { ...route.query, asset_type: value || undefined, asset: undefined, version: undefined } }) },
})
const assetTypes = [{ value: '', label: '全部类型' }, { value: 'voice-model', label: '声音模型' }, { value: 'character-image', label: '人物形象' }, { value: 'music-work', label: '音乐作品' }, { value: 'video-project', label: '视频工程' }]
const positions = new Map<string, number>()
let listGeneration = 0, detailGeneration = 0, alive = true, hidden = false
let navigationRequest: AbortController | undefined
const shortID = (value: string) => value ? `${value.slice(0, 8)}…` : '—'
const message = (cause: unknown) => cause instanceof Error ? cause.message : '数字世界暂时不可用。'
onBeforeRouteUpdate((_to, from) => {
  positions.delete(from.fullPath); positions.set(from.fullPath, dashboardHost.getScroll())
  while (positions.size > 60) positions.delete(positions.keys().next().value!)
})
async function restore() { await nextTick(); if (alive && !hidden) dashboardHost.restoreScroll(positions.get(route.fullPath) || 0) }
async function loadWorld(more = false) {
  if (!alive || hidden) return
  const generation = ++listGeneration
  loading.value = true; error.value = ''
  if (!more) { assets.value = []; cursor.value = '' }
  try {
    const args: Record<string, unknown> = { limit: 50 }
    if (assetType.value) args.asset_type = assetType.value
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
function openAsset(item: AssetItem) { return router.push({ path: '/', query: { ...route.query, asset: item.asset_id, version: item.head_asset_version_id } }) }
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
  const source = selectedAsset.value.manifest.source, target = projectTarget(source.project_id, source.project_revision_id)
  try { await appSDK.capabilities.call('navigation.openApp', { appId: target.appId, path: target.path }, { signal: controller.signal }) }
  catch { if (!controller.signal.aborted) error.value = '无法打开来源项目，请确认应用已安装且当前身份有访问权限。' }
}
function refresh() { void loadWorld(); void loadAsset() }
watch(assetType, () => { void loadWorld() })
watch(() => [route.query.asset, route.query.version], () => void loadAsset(), { immediate: true })
const unsubscribe = appSDK.lifecycle.subscribe(event => {
  if (event === 'hidden' || event === 'dispose') { positions.set(route.fullPath, dashboardHost.getScroll()); navigationRequest?.abort() }
  hidden = event === 'hidden'
  if (event === 'hidden' || event === 'dispose') { listGeneration++; detailGeneration++; loading.value = false; detailLoading.value = false }
  if (event === 'active') refresh()
})
onBeforeUnmount(() => { alive = false; listGeneration++; detailGeneration++; navigationRequest?.abort(); unsubscribe() })
void loadWorld()
</script>

<template>
  <main class="main">
    <div class="workspace project-world-page">
      <header class="heading"><div class="heading-left"><div class="eyebrow">WORLD</div><h1>数字世界</h1><p>按资产类型找到可复用的固定版本，来源项目始终保留。</p></div><button class="btn" :disabled="loading || detailLoading" @click="refresh">刷新</button></header>
      <div v-if="error" class="state error-state" role="alert"><strong>暂时无法读取</strong><span>{{ error }}</span><button class="btn" @click="refresh">重试</button></div>
      <section class="toolbar"><label>资产类型<select v-model="assetType"><option v-for="item in assetTypes" :key="item.value" :value="item.value">{{ item.label }}</option></select></label><span class="toolbar-note">World 只登记明确选中的文件和固定版本</span></section>
      <div v-if="loading && !assets.length" class="state loading-state" role="status">正在读取服务端修订与固定引用…</div>
      <section v-else class="content-grid">
        <div class="list-panel"><div class="section-title"><span>可复用资产</span><small>{{ assets.length }} 个可见资产</small></div><div v-if="assets.length === 0 && !error" class="state empty-state">当前类型没有可见资产。</div><button v-for="asset in assets" :key="asset.asset_id" class="list-card" :class="{ selected: selectedAsset?.asset_id === asset.asset_id }" @click="openAsset(asset)"><span class="card-glyph">◉</span><span class="card-main"><strong>{{ asset.name }}</strong><small>{{ asset.asset_type }} · {{ asset.subjects.join('、') || '未标主体' }}</small></span><span class="card-id">{{ shortID(asset.head_asset_version_id) }}</span></button><button v-if="cursor" class="btn detail-action" :disabled="loading" @click="loadWorld(true)">加载更多</button></div>
        <aside class="detail-panel"><div v-if="detailLoading" class="state" role="status">正在读取固定版本…</div><div v-else-if="!selectedAsset" class="state empty-state"><strong>选择一个资产</strong><span>读取固定版本详情后才能进入项目复用。</span></div><template v-else><div class="section-title"><span>{{ selectedAsset.manifest.name }}</span><span class="version-pill">{{ shortID(selectedAsset.asset_version_id) }}</span></div><dl class="facts"><dt>资产类型</dt><dd>{{ selectedAsset.manifest.asset_type }}</dd><dt>主体</dt><dd>{{ selectedAsset.manifest.subjects.join('、') }}</dd><dt>来源项目修订</dt><dd>{{ shortID(selectedAsset.manifest.source.project_id) }} · {{ shortID(selectedAsset.manifest.source.project_revision_id) }}</dd><dt>关系</dt><dd>{{ selectedAsset.manifest.source.relation }}</dd></dl><div class="subheading">可用文件</div><ul class="plain-list"><li v-for="file in selectedAsset.manifest.files" :key="file.file_id"><span>{{ file.path }}</span><small>{{ file.role }} · {{ shortID(file.file_id) }}</small></li></ul><button class="btn detail-action" @click="openSource">查看来源项目的固定修订</button><button class="btn primary detail-action" @click="bringIntoProject">带入项目后固定引用</button></template></aside>
      </section>
    </div>
  </main>
</template>

<style scoped>
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
