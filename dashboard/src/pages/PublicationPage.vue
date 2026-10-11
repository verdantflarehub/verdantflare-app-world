<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute } from 'vue-router'
import { useHost } from '../platform/host'
import { publicationError, publicationIdentity } from '../platform/publication-client'
import { createWorldPublication, publicationPointer, type PublicationPreview, type PublicationRecord } from '../platform/world-publication'
const route = useRoute(), { appSDK, dashboardHost } = useHost()
const query = (key: string) => typeof route.query[key] === 'string' ? route.query[key] as string : ''
const routeKey = computed(() => JSON.stringify([query('project'), query('prepared'), query('candidate'), query('commit')]))
const client = shallowRef<ReturnType<typeof createWorldPublication>>(), preview = shallowRef<PublicationPreview>(), pending = shallowRef<PublicationRecord>()
const busy = ref(false), hidden = ref(false), error = ref(''), acknowledged = ref(false)
let alive = true, generation = 0, scroll = 0, navigation: AbortController | undefined
const stateText = computed(() => ({ unknown: '入库结果待确认', preparing: '入库仍在准备', committed: '固定版本已入库', conflict: '原入库操作发生版本冲突', failed: '原入库操作未成功' }[pending.value?.state || 'unknown']))
async function task(work: (c: NonNullable<typeof client.value>) => Promise<void>) {
  if (!client.value || busy.value || hidden.value) return
  const c = client.value, current = generation; busy.value = true; error.value = ''
  try { await work(c) } catch (reason) { if (alive && current === generation && !hidden.value) error.value = publicationError(reason) }
  finally { if (alive && current === generation) { pending.value = c.pending(); busy.value = false } }
}
function load() { void task(async c => { const value = await c.load(); if (alive && client.value === c) preview.value = value }) }
function confirm() { if (acknowledged.value) void task(async c => { await c.submit() }) }
function status() { void task(async c => { await c.status() }) }
async function returnToProject() {
  if (!client.value || busy.value || hidden.value) return
  const c = client.value, current = generation
  navigation?.abort(); const controller = new AbortController(); navigation = controller; busy.value = true
  try {
    await c.status()
    if (!alive || hidden.value || generation !== current || client.value !== c || controller.signal.aborted) return
    await appSDK.capabilities.call('navigation.openApp', { appId: 'project', path: c.returnPath() }, { signal: controller.signal })
  }
  catch { if (!controller.signal.aborted) error.value = '无法返回项目，入库结果与恢复记录仍保留。请稍后重试。' }
  finally { if (alive && generation === current) { pending.value = c.pending(); busy.value = false } }
}
function mayLeave() { if (!busy.value) return true; error.value = '操作仍在进行，请等结果确认后再离开。'; return false }
onBeforeRouteLeave(mayLeave); onBeforeRouteUpdate(mayLeave)
watch(busy, value => dashboardHost.setDirty(value), { flush: 'sync' })
watch(routeKey, () => {
  generation++; navigation?.abort(); client.value?.dispose(); client.value = undefined; preview.value = undefined; pending.value = undefined; acknowledged.value = false; error.value = ''
  try {
    const pointer = publicationPointer({ project_id: query('project'), prepared_revision_id: query('prepared'), candidate_key: query('candidate'), commit_id: query('commit') })
    client.value = createWorldPublication(appSDK, localStorage, pointer); pending.value = client.value.pending()
    if (pending.value?.result || pending.value?.preview.request.asset_id) preview.value = pending.value.preview
    else load()
  } catch (reason) { error.value = publicationError(reason) }
}, { immediate: true })
const initial = publicationIdentity(appSDK.context.get())
const stopContext = appSDK.context.subscribe(value => {
  if (publicationIdentity(value) === initial) return
  generation++; navigation?.abort(); client.value?.dispose(); client.value = undefined; preview.value = undefined; pending.value = undefined; busy.value = false; error.value = '会话已变化，请重新打开应用。原操作恢复记录仍保留。'
})
const stopLifecycle = appSDK.lifecycle.subscribe(event => {
  if (event === 'hidden') { scroll = dashboardHost.getScroll(); hidden.value = true; navigation?.abort() }
  if (event === 'active') { hidden.value = false; dashboardHost.restoreScroll(scroll); if (!preview.value && !busy.value) load() }
  if (event === 'dispose') { alive = false; generation++; navigation?.abort() }
})
onBeforeUnmount(() => { alive = false; generation++; navigation?.abort(); client.value?.dispose(); stopContext(); stopLifecycle(); dashboardHost.setDirty(false) })
</script>
<template>
  <main class="main publication">
    <header class="heading"><div class="heading-left"><div class="eyebrow">数字世界</div><h1>确认固定版本入库</h1><p>核对项目已准备的版本与文件范围，再确认保存。</p></div><RouterLink class="btn" to="/">返回资产目录</RouterLink></header>
    <p v-if="busy" role="status" class="state">正在核对原操作…</p><p v-if="error" role="alert" class="state error">{{ error }}</p>
    <section v-if="preview" class="panel"><h2>{{ preview.request.name }}</h2><p class="hint">来源项目的固定修订：<code>{{ preview.request.source_revision_id }}</code></p><ul><li v-for="version in preview.versions" :key="version.version_key">{{ version.label }} · {{ version.file_ids.length }} 个成果文件</li></ul><p class="hint">共 {{ preview.request.file_ids.length }} 个文件，包含明确登记的随附资料。项目后续选用变化不会改变本次范围。</p>
      <template v-if="!pending || ['unknown','preparing'].includes(pending.state)"><label><input v-model="acknowledged" type="checkbox" :disabled="busy || hidden">确认按上述版本和范围入库</label><div class="actions"><button class="btn primary" :disabled="busy || hidden || !acknowledged" @click="confirm">{{ pending ? '恢复原入库操作' : '确认入库' }}</button><button v-if="pending?.result || pending?.preview.request.asset_id" class="btn" :disabled="busy || hidden" @click="status">查询原操作结果</button></div></template>
    </section>
    <section v-if="pending" class="panel"><h2>{{ stateText }}</h2><p v-if="pending.state === 'committed'" class="hint">入库已成功。返回项目后将核对真实回执，再保存项目关联。</p><p v-else class="hint">原操作记录已保留。恢复使用相同的操作编号、版本和文件范围。</p><button v-if="pending.state === 'committed'" class="btn primary" :disabled="busy || hidden" @click="returnToProject">返回项目并保存关联</button></section>
    <button v-if="!preview && client && !busy" class="btn" :disabled="hidden" @click="load">重新读取入库范围</button>
  </main>
</template>
<style scoped>
.eyebrow { color: var(--accent); font-size: 11px; letter-spacing: .12em; }.panel { padding: 24px; margin-bottom: 20px; min-width: 0; border: 1px solid var(--line); border-radius: 8px; background: var(--shell-panel); }h2 { font-size: 17px; margin-bottom: 16px; }.hint { color: var(--muted); margin: 12px 0; overflow-wrap: anywhere; }ul { padding-left: 22px; }label { display: flex; align-items: start; gap: 8px; margin-top: 18px; }.actions { display: flex; flex-wrap: wrap; gap: 10px; margin-top: 18px; }.state { padding: 16px; margin-bottom: 20px; border: 1px solid var(--line); border-radius: 8px; color: var(--muted); }.error { color: var(--danger); }
</style>
