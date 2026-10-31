<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { callTool } from '../platform/mcp'
import { useHost } from '../platform/host'
import { readAssetList, type AssetItem } from '../platform/world-model'
import { assetFileURL } from '../platform/asset-media'
import { ANGLES, chipValues, facetRequests, listArgs, masonry, memoryKey, readFacets, readFilters, recall, remember, setLastList, subjectValue, type GalleryKind } from '../platform/reference-gallery'
// Pose / costume gallery (World / Pose §9, Costume §8): category chips with
// counts, image masonry with incremental loading, and a detail sub-page.
const props = defineProps<{ kind: GalleryKind }>()
const route = useRoute(), router = useRouter(), { appSDK, dashboardHost } = useHost()
const filters = computed(() => readFilters(props.kind, route.query))
const items = ref<AssetItem[]>([]), cursor = ref(''), total = ref(0), loading = ref(false), error = ref('')
const outer = ref<Record<string, Record<string, number>>>({}), inner = ref<Record<string, Record<string, number>>>({})
const urls = ref<Record<string, string>>({}), grid = ref<HTMLElement>(), sentinel = ref<HTMLElement>(), columns = ref(4)
const search = ref(filters.value.q)
let generation = 0, alive = true, observer: IntersectionObserver | undefined, resize: ResizeObserver | undefined

const title = computed(() => props.kind === 'pose' ? '姿势' : '服装')
const ratio = (a: AssetItem) => props.kind === 'pose' ? 4 / 3 : subjectValue(a, '范围') === '单品' ? 1.15 : 1.5
const cols = computed(() => masonry(items.value, columns.value, ratio))

function setQuery(patch: Record<string, string>) {
  const q: Record<string, string> = {}
  for (const [k, v] of Object.entries({ ...route.query, ...patch })) if (typeof v === 'string' && v) q[k] = v
  void router.replace({ path: route.path, query: q })
}
async function loadFacets() {
  const [a, b] = await Promise.all(facetRequests(props.kind, filters.value).map(args => callTool('world.list', args)))
  outer.value = readFacets(a).facets; inner.value = readFacets(b).facets
}
async function loadPage(reset: boolean) {
  const current = ++generation
  if (reset) { items.value = []; cursor.value = ''; total.value = 0 }
  loading.value = true; error.value = ''
  try {
    const raw = await callTool('world.list', { ...listArgs(props.kind, filters.value, reset ? '' : cursor.value), ...(reset ? { facets: [props.kind === 'pose' ? '人数' : '范围'] } : {}) })
    if (!alive || current !== generation) return
    const page = readAssetList(raw)
    if (reset) total.value = readFacets(raw).total
    items.value = reset ? page.items : [...items.value, ...page.items.filter(i => !items.value.some(x => x.asset_id === i.asset_id))]
    cursor.value = page.next_cursor
    for (const item of page.items) void preview(item)
  } catch (e) { if (alive && current === generation) error.value = e instanceof Error ? e.message : '读取失败，请重试。' }
  finally { if (alive && current === generation) loading.value = false }
}
async function preview(item: AssetItem) {
  if (!item.preview || urls.value[item.asset_id]) return
  try { const url = await assetFileURL((n, a) => appSDK.capabilities.call(n, a as never), { asset_id: item.asset_id, asset_version_id: item.head_asset_version_id }, item.preview); if (alive) urls.value = { ...urls.value, [item.asset_id]: url } } catch { /* tile keeps its neutral background */ }
}
async function reload() {
  const saved = recall(memoryKey(props.kind, filters.value))
  void loadFacets().catch(() => { outer.value = {}; inner.value = {} })
  if (saved) {
    items.value = saved.items; cursor.value = saved.cursor; total.value = saved.total; error.value = ''
    for (const item of saved.items) void preview(item)
    await nextTick(); dashboardHost.restoreScroll(saved.scroll); return
  }
  await loadPage(true)
}
function open(item: AssetItem) {
  const key = memoryKey(props.kind, filters.value)
  remember(key, { items: items.value, cursor: cursor.value, total: total.value, scroll: dashboardHost.getScroll() })
  setLastList(props.kind, key, Object.fromEntries(Object.entries(route.query).filter(([, v]) => typeof v === 'string')) as Record<string, string>)
  void router.push({ path: `/${props.kind}/asset/${item.asset_id}`, query: { version: item.head_asset_version_id, ...(props.kind === 'pose' && filters.value.view !== 'front' ? { v: filters.value.view } : {}) } })
}
const caption = (a: AssetItem) => props.kind === 'pose'
  ? [subjectValue(a, '动作'), subjectValue(a, '体系')].filter(Boolean).join(' · ')
  : subjectValue(a, '范围') === '单品' ? [subjectValue(a, '类型'), subjectValue(a, '画风')].filter(Boolean).join(' · ') : subjectValue(a, '分类')
const label = (a: AssetItem) => a.subjects.find(s => !s.includes(':')) || a.name

watch(() => route.fullPath, (to, from) => { if (route.path === `/${props.kind}` && to !== from) { search.value = filters.value.q; void reload() } })
onMounted(() => {
  void reload()
  observer = new IntersectionObserver(e => { if (e.some(x => x.isIntersecting) && cursor.value && !loading.value) void loadPage(false) }, { rootMargin: '900px' })
  if (sentinel.value) observer.observe(sentinel.value)
  resize = new ResizeObserver(() => { const w = grid.value?.clientWidth || 1200; columns.value = Math.max(2, Math.min(6, Math.floor(w / 230))) })
  if (grid.value) resize.observe(grid.value)
})
onBeforeUnmount(() => { alive = false; generation++; observer?.disconnect(); resize?.disconnect() })
</script>
<template>
  <main class="gallery" :aria-label="title">
    <header class="head"><div><h1>{{ title }}</h1><p>{{ kind === 'pose' ? '线稿人偶姿势参考，每个姿势含多个机位视角；可用于生图、试装与动作设计' : '整套穿搭与单品参考，用于试装与生图迁移款式、材质与配色' }}</p></div><span class="pill">私有 · 内部组织</span></header>

    <nav v-if="kind === 'costume'" class="tabs" aria-label="服装范围">
      <button :aria-pressed="filters.tab === 'outfit'" @click="setQuery({ tab: '', cat: '', kind: '', style: '', q: '' })">穿搭<span>{{ outer['范围']?.['穿搭'] ?? '' }}</span></button>
      <button :aria-pressed="filters.tab === 'item'" @click="setQuery({ tab: 'item', cat: '', kind: '', style: '', q: '' })">单品<span>{{ outer['范围']?.['单品'] ?? '' }}</span></button>
    </nav>

    <section class="filters" aria-label="分类筛选">
      <template v-if="kind === 'pose'">
        <div class="row"><span class="label">体系</span><button class="chip" :aria-pressed="!filters.sys" @click="setQuery({ sys: '', cat: '' })">全部</button><button v-for="v in chipValues(outer['体系'])" :key="v" class="chip" :aria-pressed="filters.sys === v" @click="setQuery({ sys: v, cat: '' })">{{ v }}<span>{{ outer['体系'][v] }}</span></button></div>
        <div class="row sub"><span class="label">动作</span><button class="chip" :aria-pressed="!filters.cat" @click="setQuery({ cat: '' })">全部</button><button v-for="v in chipValues(inner['动作'])" :key="v" class="chip" :aria-pressed="filters.cat === v" @click="setQuery({ cat: v })">{{ v }}<span>{{ inner['动作'][v] }}</span></button></div>
      </template>
      <template v-else-if="filters.tab === 'outfit'">
        <div class="row"><span class="label">分类</span><button class="chip" :aria-pressed="!filters.cat" @click="setQuery({ cat: '' })">全部</button><button v-for="v in chipValues(inner['分类'])" :key="v" class="chip" :aria-pressed="filters.cat === v" @click="setQuery({ cat: v })">{{ v }}<span>{{ inner['分类'][v] }}</span></button></div>
      </template>
      <template v-else>
        <div class="row"><span class="label">类型</span><button class="chip" :aria-pressed="!filters.kind" @click="setQuery({ kind: '' })">全部</button><button v-for="v in chipValues(inner['类型'])" :key="v" class="chip" :aria-pressed="filters.kind === v" @click="setQuery({ kind: v })">{{ v }}<span>{{ inner['类型'][v] }}</span></button></div>
        <div class="row sub"><span class="label">画风</span><button class="chip" :aria-pressed="!filters.style" @click="setQuery({ style: '' })">全部</button><button v-for="v in chipValues(inner['画风'])" :key="v" class="chip" :aria-pressed="filters.style === v" @click="setQuery({ style: v })">{{ v }}<span>{{ inner['画风'][v] }}</span></button></div>
      </template>
      <div class="bar">
        <form class="search" @submit.prevent="setQuery({ q: search.trim() })"><input v-model="search" type="search" maxlength="128" :placeholder="kind === 'pose' ? '搜索姿势名称，如“站姿”“回眸”' : '搜索名称、单品、标签'" aria-label="搜索"><button class="btn">搜索</button></form>
        <template v-if="kind === 'pose'">
          <span class="seg" role="group" aria-label="视角"><button v-for="[k, l] in ANGLES" :key="k" :aria-pressed="filters.view === k" @click="setQuery({ v: k === 'front' ? '' : k })">{{ l }}</button></span>
          <span class="seg" role="group" aria-label="人数"><button :aria-pressed="!filters.people" @click="setQuery({ people: '' })">全部</button><button :aria-pressed="filters.people === '单人'" @click="setQuery({ people: '单人' })">单人</button><button :aria-pressed="filters.people === '双人'" @click="setQuery({ people: '双人' })">双人</button></span>
        </template>
        <span class="summary">{{ total ? total + ' 项' : '' }}</span>
      </div>
    </section>

    <div v-if="error && !items.length" class="state error" role="alert"><strong>读取{{ title }}目录失败</strong><span>{{ error }}</span><button class="btn" @click="loadPage(true)">重试</button></div>
    <div v-else-if="!loading && !items.length" class="state"><strong>没有符合条件的{{ title }}</strong><span>调整分类或清空搜索后再试。</span></div>
    <div ref="grid" class="masonry" :aria-busy="loading">
      <div v-for="(col, ci) in cols" :key="ci" class="col">
        <button v-for="a in col" :key="a.asset_id" class="tile" :style="{ aspectRatio: urls[a.asset_id] ? undefined : `1 / ${ratio(a)}` }" @click="open(a)">
          <img v-if="urls[a.asset_id]" :src="urls[a.asset_id]" alt="" loading="lazy">
          <span class="cap">{{ label(a) }}<small>{{ caption(a) }}</small></span>
        </button>
      </div>
    </div>
    <div v-if="loading && !items.length" class="masonry"><div v-for="i in columns" :key="i" class="col"><span v-for="j in 3" :key="j" class="tile skeleton" :style="{ aspectRatio: `1 / ${[1.33, 1.5, 1.2][(i + j) % 3]}` }"></span></div></div>
    <div ref="sentinel" class="sentinel"></div>
    <p class="end">{{ loading && items.length ? '正在加载…' : error && items.length ? '加载失败，继续滚动重试' : items.length && !cursor ? `已显示全部 ${items.length} 项` : '' }}</p>
  </main>
</template>
<style scoped>
.gallery{color:var(--ink);min-width:0}
.head{display:flex;justify-content:space-between;align-items:flex-end;gap:16px;margin-bottom:14px}.head h1{font-size:24px;margin:0}.head p{margin:4px 0 0;color:var(--muted);font-size:13px}
.pill{padding:2px 10px;border-radius:99px;background:var(--shell-button);color:var(--muted);font-size:11.5px}
.tabs{display:flex;gap:4px;border-bottom:1px solid var(--line);margin-bottom:14px}.tabs button{padding:8px 14px;border:0;border-bottom:2px solid transparent;background:none;color:var(--muted);font:inherit;cursor:pointer;margin-bottom:-1px}.tabs button span{margin-left:6px;font-size:11.5px}.tabs button[aria-pressed=true]{color:var(--accent);border-color:var(--accent);font-weight:600}
.filters{display:grid;gap:10px;margin-bottom:16px}.row{display:flex;gap:8px;flex-wrap:wrap;align-items:center}.label{width:40px;color:var(--muted);font-size:12px;flex:none}
.chip{padding:4px 12px;border:1px solid var(--line);border-radius:99px;background:var(--shell-panel);color:var(--ink);font:inherit;font-size:12.5px;cursor:pointer}.chip span{margin-left:4px;color:var(--muted);font-size:11px}.chip[aria-pressed=true]{background:color-mix(in srgb,var(--accent) 12%,transparent);color:var(--accent);border-color:color-mix(in srgb,var(--accent) 40%,transparent);font-weight:600}.sub .chip{background:transparent;font-size:12px}
.bar{display:flex;gap:10px;align-items:center;flex-wrap:wrap}.search{display:flex;gap:8px}.search input{width:280px;padding:8px 12px;border:1px solid var(--line);border-radius:8px;background:var(--shell-panel);color:var(--ink)}
.seg{display:inline-flex;border:1px solid var(--line);border-radius:8px;overflow:hidden}.seg button{border:0;background:var(--shell-panel);color:var(--ink);padding:6px 12px;font:inherit;font-size:12.5px;cursor:pointer}.seg button+button{border-left:1px solid var(--line)}.seg button[aria-pressed=true]{background:var(--paper);color:var(--button-ink)}
.summary{margin-left:auto;color:var(--muted);font-size:12px}
.chip:focus-visible,.seg button:focus-visible,.tabs button:focus-visible,.tile:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
.masonry{display:flex;gap:12px;align-items:flex-start}.col{flex:1;min-width:0;display:grid;gap:12px}
.tile{position:relative;display:block;width:100%;padding:0;border:0;border-radius:10px;overflow:hidden;background:#fff;box-shadow:0 0 0 1px var(--line);cursor:pointer;transition:transform .18s,box-shadow .18s}.tile:hover{transform:translateY(-2px);box-shadow:0 0 0 1px var(--accent),0 10px 24px -10px #0003}
.tile img{display:block;width:100%;height:auto}
.cap{position:absolute;left:0;right:0;bottom:0;padding:22px 10px 8px;background:linear-gradient(transparent,#0a120fb8);color:#fff;font-size:12px;line-height:1.4;text-align:left;opacity:0;transition:opacity .16s}.cap small{display:block;opacity:.8;font-size:11px}.tile:hover .cap,.tile:focus-visible .cap{opacity:1}
.skeleton{background:linear-gradient(105deg,var(--shell-button) 20%,color-mix(in srgb,var(--accent) 10%,var(--shell-button)) 48%,var(--shell-button) 76%);background-size:220% 100%;animation:shimmer 1.4s linear infinite}
@keyframes shimmer{from{background-position:120% 0}to{background-position:-120% 0}}
@media (prefers-reduced-motion:reduce){.skeleton{animation:none}.tile{transition:none}}
.sentinel{height:1px}.end{text-align:center;color:var(--muted);font-size:12px;padding:16px 0}
.state{display:grid;gap:6px;justify-items:center;padding:40px;border:1px dashed var(--line);border-radius:12px;color:var(--muted);margin-bottom:16px}.state strong{color:var(--ink)}.state.error strong{color:var(--danger)}
</style>
