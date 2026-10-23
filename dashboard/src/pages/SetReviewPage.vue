<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { onBeforeRouteLeave, useRoute, useRouter } from 'vue-router'
import { useHost } from '../platform/host'
import { canonicalJSON, compareEvidence, createReviewClient, fullReviewEligible, mappedReviewOptions, parseReviewPath, readEvidence, resolveReviewScope, ReviewError, reviewGapMessage as labelGap, reviewMessage, reviewPath, reviewViewerTarget, type Evidence, type ReviewAppend, type ReviewOpen, type ReviewResult, type ReviewScope, type ReviewScopeChoice } from '../platform/set-review'
import { reviewContext, reviewContextKey, rememberReviewContext } from '../platform/set-review-memory'
import { loadReviewCandidates, type ReviewCandidate } from '../platform/set-review-candidates'

const route = useRoute(), router = useRouter(), { appSDK: sdk, dashboardHost: host } = useHost()
const domain = sdk.context.get().appId === 'world' ? 'world' : 'project'
const parsed = computed(() => parseReviewPath(route.fullPath))
const page = shallowRef<ReviewOpen>(), other = shallowRef<ReviewOpen>()
const loading = ref(false), error = ref(''), message = ref(''), forbidden = ref(false), saving = ref(false), hidden = ref(false)
const text = ref(''), scopeChoice = ref<ReviewScopeChoice>('current'), selected = ref(''), camera = ref(''), selectedOther = ref(''), compareChoice = ref(''), otherLabel = ref(''), compareError = ref(''), compareBusy = ref(false), narrowCandidate = ref('current')
const candidates = shallowRef<ReviewCandidate[]>([]), candidateBusy = ref(false), candidateError = ref(''), candidateCursor = ref(''), browseChoice = ref(''), switching = ref(false)
let candidateCursors = new Set<string>()
const scopeCameras = ref<string[]>([]), scopeRegions = ref<string[]>([]), confirmedScope = ref<ReviewScope>(), confirmedTarget = ref('')
const viewerBusy = ref(false), viewerUnavailable = ref(false)
const pending = ref(false), pendingPath = ref(''), resolvedRejected = ref(false), dialog = ref<HTMLDialogElement>(), confirmAction = ref<ReviewAppend['action']>('comment'), supersede = ref(false)
const media = ref<Record<string, { url: string; blob: Blob; width: number; height: number }>>({}), mediaErrors = ref<Record<string, string>>({}), mediaBusy = ref<Record<string, boolean>>({})
let client: ReturnType<typeof createReviewClient> | undefined, controller = new AbortController(), epoch = 0, alive = true, invalidated = false, pendingResult: ReviewResult | undefined, trigger: HTMLElement | null = null
const initialContext = canonicalJSON(sdk.context.get())
let editingTarget = ''
const target = computed(() => page.value?.target_view)
const images = computed(() => target.value?.evidence.filter(e => e.mime.startsWith('image/')) || [])
const selectedEvidence = computed(() => images.value.find(e => e.evidence_id === selected.value) || images.value[0])
const otherImages = computed(() => other.value?.target_view?.evidence.filter(e => e.mime.startsWith('image/')) || [])
const otherEvidence = computed(() => otherImages.value.find(e => e.evidence_id === selectedOther.value) || otherImages.value[0])
const stateLabels: Record<string, string> = { unreviewed: '未审阅', in_review: '审阅中', approved: '完整范围通过', rejected: '需修改' }
const roleLabels: Record<string, string> = { source_engineering: '源工程', space_record: '空间记录', lighting_record: '灯光记录', camera_reference: '机位参考', render: '工程渲染', photograph: '实拍图片', generated: '生成原片', composite: '底板合成', set_package: '片场包', supporting: '辅助证据' }
const claimLabels: Record<string, string> = { image_reading: '图像阅读', space_layout: '空间布局', source_recovery: '源工程恢复', new_camera: '新机位复用', full_set: '完整片场' }
const scopeLabels: Record<string, string> = { evidence: '所选固定证据', camera: '指定机位', region: '指定区域', full_target: '完整固定版本' }
function recordEvidence(scope?: ReviewScope) { return target.value?.evidence.filter(item => scope?.evidence_ids.includes(item.evidence_id)) || [] }
function evidenceLabel(item: Evidence) { return `${roleLabels[item.role] || '固定证据'} · ${item.mime.startsWith('image/') ? '图像 ' + (images.value.findIndex(image => image.evidence_id === item.evidence_id) + 1) : '附件'}` }
function recordMappings(scope: ReviewScope | undefined, kind: 'camera' | 'region') {
  const allowed = kind === 'camera' ? target.value?.coverage.camera_ids : target.value?.coverage.region_ids
  const recorded = kind === 'camera' ? scope?.camera_ids : scope?.region_ids
  return recorded?.filter(id => allowed?.includes(id)) || []
}
const definition = computed(() => target.value?.definition || {})
const title = computed(() => {
  for (const value of [definition.value.name, definition.value.title, (definition.value.location as Record<string, unknown>)?.name, (definition.value.material as Record<string, unknown>)?.name]) if (typeof value === 'string' && value) return value
  const candidate = candidates.value.find(item => item.key === canonicalJSON(page.value?.target_locator || parsed.value?.locator))
  return candidate?.title || candidate?.label || original.value?.label || (domain === 'project' ? '地点片场' : '场景资产')
})
const ownerID = computed(() => parsed.value?.locator.kind === 'project' ? parsed.value.locator.project_id : parsed.value?.locator.asset_id || '')
const memoryKey = computed(() => reviewContextKey(sdk.context.get(), ownerID.value))
const original = computed(() => parsed.value ? reviewContext(memoryKey.value) : undefined)
const parentPath = computed(() => {
  const l = parsed.value?.locator
  if (original.value?.path) return original.value.path
  if (!l) return domain === 'project' ? '/ai_short_drama' : '/scene'
  return l.kind === 'project' ? `/ai_short_drama/${l.project_id}/materials/location/${l.material_key}${l.state_key ? '/' + l.state_key : ''}?` + new URLSearchParams({ revision: l.content_revision_id, ...(l.version_key ? { version: l.version_key } : {}) }) : '/scene?' + new URLSearchParams({ asset: l.asset_id, version: l.asset_version_id })
})
const currentCandidateKey = computed(() => page.value ? canonicalJSON(page.value.target_locator) : '')
const candidateOptions = computed(() => {
  const current = page.value
  if (!current || candidates.value.some(item => item.key === currentCandidateKey.value)) return candidates.value
  return [{ key: currentCandidateKey.value, label: `${title.value} · 当前固定版本`, title: title.value, locator: current.target_locator }, ...candidates.value]
})
const comparisonOptions = computed(() => candidateOptions.value.filter(item => item.key !== currentCandidateKey.value))
const targetChangeBlocked = computed(() => !!text.value.trim() || pending.value || saving.value)
const fullEligible = computed(() => fullReviewEligible(page.value))
const readableImages = computed(() => page.value ? images.value.filter(e => !!media.value[mediaKey(page.value!, e)]).map(e => e.evidence_id) : [])
const cameraOptions = computed(() => mappedReviewOptions(page.value, 'camera', readableImages.value))
const regionOptions = computed(() => mappedReviewOptions(page.value, 'region', readableImages.value))
const scopeResult = computed(() => resolveReviewScope(page.value, { kind: scopeChoice.value, evidenceID: selectedEvidence.value?.evidence_id, cameraIDs: scopeCameras.value, regionIDs: scopeRegions.value }, readableImages.value))
const scope = computed(() => scopeResult.value.scope)
const previousDecision = computed(() => scope.value ? page.value?.scope_decisions.find(d => canonicalJSON(d.scope) === canonicalJSON(scope.value)) : undefined)
const confirmationMatches = computed(() => !!scope.value && !!confirmedScope.value && page.value?.target_hash === confirmedTarget.value && canonicalJSON(scope.value) === canonicalJSON(confirmedScope.value))
const canComment = computed(() => !!page.value?.target_hash && !!page.value.capabilities.can_comment && !!scope.value?.evidence_ids.length && !pending.value && !saving.value)
const canDecide = computed(() => canComment.value && page.value?.capabilities.can_decide)
const mediaKey = (p: ReviewOpen, e: Evidence) => canonicalJSON([p.target_locator, e.evidence_id, e.sha256])
const image = computed(() => page.value && selectedEvidence.value ? media.value[mediaKey(page.value, selectedEvidence.value)] : undefined)
const imageOther = computed(() => other.value && otherEvidence.value ? media.value[mediaKey(other.value, otherEvidence.value)] : undefined)
const commonCameras = computed(() => selectedEvidence.value?.camera_ids.filter(c => otherEvidence.value?.camera_ids.includes(c)) || [])
const comparisonIssues = computed(() => target.value && other.value?.target_view && selectedEvidence.value && otherEvidence.value ? compareEvidence(target.value, selectedEvidence.value, other.value.target_view, otherEvidence.value, [image.value || { width: 0, height: 0 }, imageOther.value || { width: 0, height: 0 }], camera.value) : ['尚未提供第二份固定候选'])
function live(e: number) { return alive && !hidden.value && !invalidated && e === epoch }
function clearMedia() { controller.abort(); controller = new AbortController(); sdk.media?.close(); for (const m of Object.values(media.value)) URL.revokeObjectURL(m.url); media.value = {}; mediaErrors.value = {}; mediaBusy.value = {} }
function refreshPending() {
  try { const r = client?.pending(); pending.value = !!r; pendingPath.value = r ? reviewPath(r.request.target_locator, r.request.expected_target_hash) : ''; if (r && !text.value) text.value = r.request.body } catch (e) { pending.value = true; message.value = reviewMessage(e) }
}
async function load() {
  const nextTarget = parsed.value ? canonicalJSON(parsed.value.locator) : ''; if (editingTarget && editingTarget !== nextTarget) { text.value = ''; scopeChoice.value = 'current'; scopeCameras.value = []; scopeRegions.value = []; compareChoice.value = ''; message.value = ''; pendingResult = undefined; resolvedRejected.value = false } editingTarget = nextTarget
  const e = ++epoch; clearMedia(); page.value = undefined; other.value = undefined; forbidden.value = false; error.value = ''; loading.value = true
  client?.dispose(); client = undefined; saving.value = false; viewerBusy.value = false; viewerUnavailable.value = false
  candidates.value = []; candidateCursor.value = ''; candidateCursors = new Set(); candidateError.value = ''; candidateBusy.value = false; switching.value = false; compareBusy.value = false; compareError.value = ''
  try {
    if (!parsed.value || parsed.value.locator.kind !== domain) throw new ReviewError('INVALID_RESPONSE')
    client = createReviewClient(sdk, domain, parsed.value.locator, localStorage); refreshPending()
    const result = await client.open(parsed.value.locator, parsed.value.hash)
    if (!live(e)) return
    page.value = result; editingTarget = canonicalJSON(result.target_locator)
    const fixed = reviewPath(result.target_locator, result.target_hash)
    if (fixed !== route.fullPath) { await router.replace(fixed); return }
    const saved = reviewContext(memoryKey.value + ':' + fixed)
    selected.value = saved?.evidence || ''; camera.value = saved?.camera || ''; browseChoice.value = canonicalJSON(result.target_locator)
    await loadCandidates()
    await nextTick(); if (live(e)) host.restoreScroll(saved?.top || 0)
  } catch (cause) { if (live(e)) { error.value = reviewMessage(cause); forbidden.value = cause instanceof ReviewError && [401,403].includes(cause.status) } }
  finally { if (live(e)) loading.value = false }
}
async function loadMedia(p: ReviewOpen, e: Evidence) {
  const key = mediaKey(p, e), generation = epoch
  if (media.value[key] || mediaBusy.value[key]) return
  mediaBusy.value = { ...mediaBusy.value, [key]: true }; mediaErrors.value = { ...mediaErrors.value, [key]: '' }
  try {
    const blob = await readEvidence(sdk, p.target_locator, e, controller.signal)
    if (!live(generation)) return
    const bitmap = await createImageBitmap(blob), dimensions = { width: bitmap.width, height: bitmap.height }; bitmap.close()
    if (!live(generation)) return
    media.value = { ...media.value, [key]: { blob, url: URL.createObjectURL(blob), ...dimensions } }
  } catch (cause) { if (live(generation)) mediaErrors.value = { ...mediaErrors.value, [key]: reviewMessage(cause) } }
  finally { if (live(generation)) mediaBusy.value = { ...mediaBusy.value, [key]: false } }
}
async function loadThumbnails(p: ReviewOpen) {
  const generation = epoch; let remaining = 24 * 1024 * 1024
  for (const e of p.target_view?.evidence.filter(e => e.mime.startsWith('image/')).slice(0,12) || []) {
    if (!live(generation)) return
    if (e.size_bytes <= 0 || e.size_bytes > remaining) continue
    remaining -= e.size_bytes; await loadMedia(p, e)
  }
}
async function preview(p: ReviewOpen, e: Evidence) {
  const generation = epoch
  try {
    if (!sdk.media) { message.value = '当前宿主缺少图片预览能力，请更新 Studio 后查看大图。'; return }
    await loadMedia(p, e)
    const content = media.value[mediaKey(p, e)]
    if (!content || !live(generation)) return
    const bytes = await content.blob.arrayBuffer()
    if (live(generation)) await sdk.media.preview({ bytes, mime: content.blob.type, title: `${title.value} · ${roleLabels[e.role] || e.role}` })
  } catch (cause) { if (live(generation)) message.value = reviewMessage(cause) }
}
async function openAttachment(e: Evidence) {
  if (!page.value) return
  const generation = epoch
  try {
    const blob = await readEvidence(sdk, page.value.target_locator, e, controller.signal)
    if (!live(generation)) return
    if (blob.size <= 4 * 1024 * 1024 && /^(text\/|application\/json)/.test(blob.type)) {
      const content = await blob.text(); if (!live(generation)) return
      attachment.value = content; attachmentTitle.value = roleLabels[e.role] || '固定附件'; attachmentDialog.value?.showModal()
    } else { message.value = '此文件不是可直接阅读的图片或文本；请从固定版本文件清单下载检查。' }
  } catch (cause) { if (live(generation)) message.value = reviewMessage(cause) }
}
const attachment = ref(''), attachmentTitle = ref(''), attachmentDialog = ref<HTMLDialogElement>()
function restoreTrigger() { trigger?.focus() }
function prepare(action: ReviewAppend['action'], event: Event) {
  if (!(action === 'comment' ? canComment.value : canDecide.value) || !text.value.trim()) { message.value = '请填写意见并选择已授权的检查范围。'; return }
  confirmedScope.value = structuredClone(scope.value!); confirmedTarget.value = page.value!.target_hash!
  confirmAction.value = action; supersede.value = false; trigger = event.currentTarget as HTMLElement; dialog.value?.showModal()
}
async function accepted(result: ReviewResult, activeClient = client!, generation = epoch) {
  if (!live(generation)) return
  pendingResult = result
  message.value = '已提交，正在回读固定目标上的记录…'
  const reread = await activeClient.readback(result)
  if (!live(generation)) return
  page.value = reread; pendingResult = undefined; text.value = ''; host.setDirty(false); refreshPending()
  message.value = result.projection_applied === false ? '记录已保存并回读。它属于历史目标，未改变当前制作版本状态。' : '记录已保存并回读。选用与资产发布保持独立。'
}
async function submit() {
  if (!page.value || !client || !(confirmAction.value === 'comment' ? canComment.value : canDecide.value) || !confirmationMatches.value || previousDecision.value && confirmAction.value !== 'comment' && !supersede.value) return
  dialog.value?.close(); saving.value = true; message.value = ''; resolvedRejected.value = false
  const activeClient = client, generation = epoch
  try { await activeClient.begin(page.value, confirmAction.value, confirmedScope.value!, text.value, supersede.value ? previousDecision.value?.record_id : undefined); if (!live(generation)) return; refreshPending(); await accepted(await activeClient.submit(), activeClient, generation) }
  catch (cause) { if (live(generation)) { refreshPending(); resolvedRejected.value = activeClient.isTerminal(); message.value = pendingResult ? reviewMessage(new ReviewError('READBACK_FAILED')) : reviewMessage(cause) } }
  finally { if (live(generation)) saving.value = false }
}
async function recover(retry = false) {
  if (!client || saving.value) return
  saving.value = true; message.value = ''
  const activeClient = client, generation = epoch
  try {
    const r = activeClient.pending(); if (!r) return
    if (pendingPath.value !== route.fullPath) { await router.push(pendingPath.value); return }
    let status: { state: string; result?: ReviewResult }
    try { status = await activeClient.status() } catch (cause) { if (!(cause instanceof ReviewError && cause.code === 'NOT_FOUND' && retry)) throw cause; status = { state: 'not_found' } }
    if (!live(generation)) return
    if (status.result) await accepted(status.result, activeClient, generation)
    else if (status.state === 'rejected') { resolvedRejected.value = true; message.value = '原请求已明确拒绝。保留输入，重读后再决定。' }
    else if (retry) await accepted(await activeClient.submit(), activeClient, generation)
    else message.value = '原请求尚未完成，请稍后查询；重试只发送同一原请求。'
  } catch (cause) { if (live(generation)) { message.value = reviewMessage(cause); resolvedRejected.value = activeClient.isTerminal() } }
  finally { if (live(generation)) { saving.value = false; refreshPending() } }
}
async function rereadAfterRejection() {
  try { if (!client?.isTerminal()) return; client.clearRejected(); refreshPending(); resolvedRejected.value = false; const p = page.value; if (p) page.value = await client.open(p.target_locator, p.target_hash || undefined); message.value = '已重新读取，请重新核对范围与意见后提交。' } catch (cause) { message.value = reviewMessage(cause) }
}
async function moreRecords() {
  const p = page.value, e = epoch
  if (!p?.next_cursor || !client) return
  try { const next = await client.open(p.target_locator, p.target_hash || undefined, p.next_cursor, p.records_revision_id); if (!live(e)) return; if (next.records_head_record_id !== p.records_head_record_id || next.next_cursor === p.next_cursor) throw new ReviewError('REVIEW_CONFLICT'); page.value = { ...p, records: [...p.records, ...next.records], next_cursor: next.next_cursor } } catch (cause) { message.value = reviewMessage(cause) }
}
async function loadCandidates(more = false) {
  const current = page.value, generation = epoch, cursor = more ? candidateCursor.value : ''
  if (!current || candidateBusy.value || more && !cursor) return
  candidateBusy.value = true; candidateError.value = ''
  const seenCursors = more ? new Set(candidateCursors) : new Set<string>()
  try {
    if (cursor && seenCursors.has(cursor)) throw new ReviewError('INVALID_RESPONSE')
    const result = await loadReviewCandidates(sdk, current.target_locator, { signal: controller.signal, cursor })
    if (!live(generation)) return
    if (result.nextCursor && (result.nextCursor === cursor || seenCursors.has(result.nextCursor))) throw new ReviewError('INVALID_RESPONSE')
    if (cursor) seenCursors.add(cursor)
    candidateCursors = seenCursors
    candidates.value = [...new Map([...(more ? candidates.value : []), ...result.items].map(item => [item.key, item])).values()]
    candidateCursor.value = result.nextCursor
    if (!comparisonOptions.value.some(item => item.key === compareChoice.value)) compareChoice.value = ''
  } catch (cause) { if (live(generation)) candidateError.value = reviewMessage(cause) }
  finally { if (live(generation)) candidateBusy.value = false }
}
async function changeTarget(candidate?: ReviewCandidate) {
  if (!candidate || switching.value || candidate.key === currentCandidateKey.value) return
  if (targetChangeBlocked.value) { message.value = '请先保存或清空当前意见，并确认原提交结果后再切换审阅目标。'; return }
  const generation = epoch
  switching.value = true; message.value = ''
  const reader = createReviewClient(sdk, domain, candidate.locator, localStorage)
  try {
    const value = await reader.open(candidate.locator)
    if (!live(generation)) return
    if (!value.target_hash) { message.value = '此版本尚未建立可审阅的固定目标，请先补齐版本关联。'; return }
    if (targetChangeBlocked.value) { message.value = '当前意见尚未处理，已保留输入并停止切换。'; return }
    remember()
    const locator = value.target_locator
    const parent = locator.kind === 'project'
      ? `/ai_short_drama/${locator.project_id}/materials/location/${locator.material_key}${locator.state_key ? '/' + locator.state_key : ''}?` + new URLSearchParams({ revision: locator.content_revision_id, ...(locator.version_key ? { version: locator.version_key } : {}) })
      : '/scene?' + new URLSearchParams({ asset: locator.asset_id, version: locator.asset_version_id })
    rememberReviewContext(reviewContextKey(sdk.context.get(), locator.kind === 'project' ? locator.project_id : locator.asset_id), parent, 0, undefined, undefined, original.value?.work, candidate.title)
    await router.push(reviewPath(locator, value.target_hash))
  } catch (cause) { if (live(generation)) message.value = reviewMessage(cause) }
  finally { reader.dispose(); if (live(generation)) switching.value = false }
}
async function loadComparison() {
  const candidate = comparisonOptions.value.find(item => item.key === compareChoice.value), generation = epoch
  if (!candidate || compareBusy.value) return
  compareError.value = ''; compareBusy.value = true; other.value = undefined; otherLabel.value = ''
  const reader = createReviewClient(sdk, domain, candidate.locator, localStorage)
  try {
    const value = await reader.open(candidate.locator)
    if (!live(generation)) return
    if (!value.target_hash) { compareError.value = '此候选尚未建立固定审阅目标，不能比较。'; return }
    if (value.target_hash === page.value?.target_hash && canonicalJSON(value.target_locator) === currentCandidateKey.value) { compareError.value = '所选候选与当前固定目标相同，请选择其他版本。'; return }
    other.value = value; otherLabel.value = candidate.label; selectedOther.value = ''; camera.value = ''
  } catch (cause) { if (live(generation)) compareError.value = reviewMessage(cause) }
  finally { reader.dispose(); if (live(generation)) compareBusy.value = false }
}
async function viewer(chosen?: Evidence) {
  const p = page.value, generation = epoch
  if (!p || viewerBusy.value) return
  const packages = p.target_view?.evidence.filter(e => e.role === 'set_package') || []
  if (!packages.length) { message.value = '此固定目标尚未提供片场包，不能打开三维空间。固定图像与审阅可继续。'; return }
  if (!chosen && packages.length > 1) { message.value = '此目标有多个固定片场包，请在“固定文件与摘要”中选择要打开的包。'; return }
  const selectedPackage = chosen || packages[0]
  if (!packages.some(e => e.evidence_id === selectedPackage.evidence_id)) return
  viewerBusy.value = true; viewerUnavailable.value = false
  try {
    if (selectedPackage.size_bytes > 4 * 1024 * 1024) throw new ReviewError('INVALID_RESPONSE')
    const blob = await readEvidence(sdk, p.target_locator, selectedPackage, controller.signal)
    if (!live(generation)) return
    if (blob.size > 4 * 1024 * 1024 || !/^(application\/json|text\/)/.test(blob.type)) throw new ReviewError('INVALID_RESPONSE')
    const manifest = JSON.parse(await blob.text()), destination = reviewViewerTarget(p.target_locator, manifest)
    if (!live(generation)) return
    remember()
    await sdk.capabilities.call('navigation.openApp', destination, { signal: controller.signal })
  } catch (cause) {
    if (live(generation)) {
      const code = cause && typeof cause === 'object' && 'code' in cause ? String(cause.code) : cause instanceof Error ? cause.message : ''
      viewerUnavailable.value = ['APP_NOT_INSTALLED','APP_NOT_FOUND','NOT_FOUND','CAPABILITY_UNAVAILABLE','TARGET_NOT_FOUND','APP_NAVIGATION_UNAVAILABLE'].includes(code)
      message.value = viewerUnavailable.value ? '片场查看器暂不可用，请在应用中心核对安装状态。固定图像与审阅可继续。' : code === 'PERMISSION_DENIED' || code === 'FORBIDDEN' || code === 'CAPABILITY_DENIED' ? '当前会话无权打开片场查看器或此固定包。' : reviewMessage(cause)
    }
  } finally { if (live(generation)) viewerBusy.value = false }
}
async function openViewerCenter() { try { await sdk.navigation.request({ target:'appcenter', path:'/' }) } catch (cause) { message.value = reviewMessage(cause) } }

function remember() { if (page.value) rememberReviewContext(memoryKey.value + ':' + route.fullPath, '', host.getScroll(), selectedEvidence.value?.evidence_id, camera.value) }
watch(text, value => host.setDirty(!!value.trim()))
watch(() => route.fullPath, () => { if (!invalidated && !hidden.value) void load() }, { immediate: true })
watch(page, p => { if (p) void loadThumbnails(p) })
watch([page, selectedEvidence], ([p, e]) => { if (p && e) void loadMedia(p, e) })
watch([other, otherEvidence], ([p, e]) => { if (p && e) void loadMedia(p, e) })
watch(commonCameras, values => { if (!values.includes(camera.value)) camera.value = values[0] || '' })
onBeforeRouteLeave(() => { remember(); dialog.value?.close(); attachmentDialog.value?.close(); return true })
const stopLife = sdk.lifecycle.subscribe(event => {
  if (event === 'hidden' || event === 'dispose') { remember(); hidden.value = true; epoch++; clearMedia(); page.value = undefined; other.value = undefined; candidates.value = []; candidateCursor.value = ''; dialog.value?.close(); attachmentDialog.value?.close() }
  else if (!invalidated) { hidden.value = false; void load() }
})
const stopContext = sdk.context.subscribe(c => { if (canonicalJSON(c) !== initialContext) { invalidated = true; epoch++; clearMedia(); page.value = undefined; other.value = undefined; candidates.value = []; candidateCursor.value = ''; text.value = ''; error.value = '会话已变化，请重新打开审阅。'; client?.dispose() } })
onBeforeUnmount(() => { alive = false; epoch++; clearMedia(); client?.dispose(); stopLife(); stopContext(); host.setDirty(false) })
</script>

<template>
  <main class="main set-review">
    <nav class="review-crumb" aria-label="面包屑">
      <RouterLink :to="domain === 'project' ? '/ai_short_drama' : '/'">{{ domain === 'project' ? '项目管理' : '数字世界' }}</RouterLink><span>/</span>
      <template v-if="!forbidden && page"><template v-if="domain === 'project'"><RouterLink to="/ai_short_drama">短剧制作</RouterLink><span>/</span><RouterLink v-if="parsed?.locator.kind === 'project'" :to="'/ai_short_drama/' + parsed.locator.project_id + '?revision=' + parsed.locator.content_revision_id">{{ original?.work || '短剧作品' }}</RouterLink><span>/</span></template><RouterLink :to="domain === 'project' && parsed?.locator.kind === 'project' ? '/ai_short_drama/' + parsed.locator.project_id + '/materials/location?revision=' + parsed.locator.content_revision_id : '/scene'">场景</RouterLink><span>/</span><RouterLink :to="parentPath">{{ title }}</RouterLink><span>/</span><template v-if="domain === 'world'"><RouterLink :to="parentPath">固定版本</RouterLink><span>/</span></template></template>
      <span aria-current="page">片场审阅</span>
    </nav>
    <header class="heading"><div class="heading-left"><span class="review-eyebrow">{{ domain === 'project' ? 'PROJECT · LOCATION' : 'WORLD · FIXED ASSET' }}</span><h1>{{ page ? title + ' · ' : '' }}片场审阅</h1><p>阅读空间、机位与缺项，在固定版本上记录检查范围。</p></div><div v-if="page" class="review-actions"><span class="review-tag">{{ stateLabels[page.review_state] }}</span><button class="btn" :disabled="viewerBusy" @click="viewer()">打开片场查看器</button></div></header>
    <p v-if="loading" class="review-state" role="status">正在读取固定目标与审阅记录…</p>
    <section v-else-if="error" class="review-state" role="alert"><h2>{{ forbidden ? '没有读取权限' : '固定目标暂不可读' }}</h2><p>{{ error }}</p><button class="btn" @click="load">重读固定目标</button></section>
    <template v-else-if="page">
      <div v-if="!page.target_hash || page.mapping_state === 'legacy_unmapped'" class="review-notice"><strong>历史附件 · 版本关联待补齐</strong><p>现有原件可阅读；尚未建立可正式写入的固定目标，不补造版本或批准。</p></div>
      <div v-else class="review-fixed"><span>已固定送审内容</span><code>{{ page.target_hash.slice(0, 16) }}…</code><span>新增意见不改变送审内容；新目标不继承旧结论。</span></div>
      <section class="review-version-picker" aria-label="固定版本选择">
        <form class="review-compare-form" @submit.prevent="changeTarget(candidateOptions.find(item => item.key === browseChoice))">
          <label>浏览固定版本<select v-model="browseChoice" :disabled="candidateBusy || switching || targetChangeBlocked"><option v-for="item in candidateOptions" :key="item.key" :value="item.key">{{ item.label }}</option></select></label>
          <button class="btn" :disabled="candidateBusy || switching || targetChangeBlocked || !browseChoice || browseChoice === currentCandidateKey">{{ switching ? '读取固定版本…' : '切换审阅目标' }}</button>
        </form>
        <p class="review-muted">{{ domain === 'project' ? '候选来自当前送审修订中的地点与版本，不读取后续制作内容。' : '列出当前有权访问场景的固定版本快照；当前历史版本会继续保留。' }}</p>
        <p v-if="targetChangeBlocked" class="review-muted" role="status">当前有未提交意见或待确认的原请求。请先保存或清空意见，并确认提交结果后再切换；另一候选仍可只读比较。</p>
        <p v-if="candidateBusy" class="review-muted" role="status">正在读取可用固定版本…</p>
        <p v-else-if="candidateError" class="review-muted" role="alert">{{ candidateError }} <button class="btn" @click="loadCandidates()">重读候选</button></p>
        <p v-else-if="!comparisonOptions.length" class="review-muted">当前没有其他可用固定候选。</p>
        <button v-if="candidateCursor" class="btn" :disabled="candidateBusy" @click="loadCandidates(true)">读取更多场景</button>
      </section>
      <div v-if="message" class="review-notice" role="status">{{ message }}<button v-if="viewerUnavailable" class="btn" @click="openViewerCenter">前往应用中心</button></div>
      <div v-if="pending" class="review-notice"><strong>有待确认的原提交</strong><p>刷新、中断或重试均恢复相同请求，不另建提交编号。</p><div class="review-actions"><button class="btn" :disabled="saving" @click="recover()">查询原提交</button><button class="btn" :disabled="saving" @click="recover(true)">恢复原请求</button><button v-if="resolvedRejected" class="btn" @click="rereadAfterRejection">保留输入并重读</button></div></div>
      <div class="review-hero-grid">
        <article class="review-panel">
          <button v-if="selectedEvidence && image" class="review-image" :aria-label="'查看大图：' + title" @click="preview(page, selectedEvidence)"><img :src="image.url" :alt="title + ' · ' + (roleLabels[selectedEvidence.role] || selectedEvidence.role)"><span>⤢ 查看大图</span></button>
          <div v-else class="review-image-empty"><p>{{ selectedEvidence ? mediaErrors[mediaKey(page, selectedEvidence)] || '正在读取原始图像…' : '这个固定目标尚无可读图像。' }}</p><button v-if="selectedEvidence" class="btn" @click="loadMedia(page, selectedEvidence)">重试原图</button></div>
          <div class="review-caption"><strong>{{ selectedEvidence ? roleLabels[selectedEvidence.role] || selectedEvidence.role : '证据待提供' }}</strong><span>{{ selectedEvidence?.camera_ids.join(' / ') || '机位映射未登记' }}</span><small v-if="image">{{ image.width }} × {{ image.height }} · 原始画幅，无裁切</small></div>
        </article>
        <aside class="review-panel review-padded"><h2>本次范围</h2><dl><dt>当前可读证据</dt><dd>{{ target?.evidence.length || 0 }} 项 · {{ images.length }} 张图片</dd><dt>工程与空间</dt><dd>{{ fullEligible ? '已具备登记的必需证据，仍需人工结论' : '完整范围证据尚不齐备' }}</dd><dt>当前阅读</dt><dd>只对所选固定证据作判断</dd></dl><p class="review-muted">图像通过不等于工程恢复、任意角度可拍或 World 发布。</p></aside>
      </div>
      <section class="review-section"><h2>视角与机位</h2><p class="review-muted">来自当前目标登记的固定证据；未提供机位对应时明确留空。</p><div class="review-evidence-grid"><button v-for="(item, index) in images" :key="item.evidence_id" class="review-evidence" :class="{ active: selectedEvidence?.evidence_id === item.evidence_id }" @click="selected = item.evidence_id"><img v-if="media[mediaKey(page,item)]" :src="media[mediaKey(page,item)].url" :alt="'固定图像 ' + (index + 1)"><span v-else class="review-evidence-placeholder">{{ index + 1 }}</span><strong>{{ roleLabels[item.role] || item.role }} {{ index + 1 }}</strong><small>{{ item.camera_ids.join(' / ') || '机位映射未登记' }}</small></button></div><p v-if="!images.length" class="review-muted">无图像不代表无工程；请继续检查下方实际文件与缺项。</p></section>
      <section class="review-section"><h2>空间、资产与缺项</h2><div class="review-two"><article class="review-panel review-padded"><h3>固定要求</h3><ul class="review-list"><li v-for="r in target?.requirements" :key="r.requirement_id"><strong>{{ claimLabels[r.claim] || '固定证据检查' }}</strong><span>{{ ({satisfied:'证据齐备',missing:'缺项',unverified:'未核实'} as Record<string,string>)[r.state] || r.state }}</span><small>{{ labelGap(r.reason_code) }}</small></li></ul><p v-if="!target?.requirements.length" class="review-muted">未登记完整空间与恢复要求，不能默认通过。</p></article><article class="review-panel review-padded"><h3>可用性与依赖</h3><p v-for="(gap,i) in [...page.missing,...page.availability]" :key="i" class="review-muted">○ {{ labelGap(gap.code) }}</p><p v-if="!page.missing.length && !page.availability.length" class="review-muted">当前已授权内容可读；技术可读不等于人工通过。</p><p class="review-muted">{{ target?.dependencies.length || 0 }} 项固定资产依赖。项目配置与 World 资产分别审阅。</p><details><summary>固定文件与摘要</summary><ul class="review-list"><li v-for="e in target?.evidence" :key="e.evidence_id"><strong>{{ roleLabels[e.role] || e.role }}</strong><small>{{ e.mime }} · {{ e.size_bytes }} 字节</small><code>{{ e.sha256 || '摘要暂未取得' }}</code><button v-if="e.role === 'set_package'" class="btn" :disabled="viewerBusy" @click="viewer(e)">在片场查看器中打开</button><button v-if="!e.mime.startsWith('image/')" class="btn" @click="openAttachment(e)">阅读固定附件</button></li></ul></details></article></div></section>
      <section class="review-section"><h2>同机位路线比较</h2><p class="review-muted">加载另一份已固定、已授权的审阅目标。比较不改变意见归属，一次只对一个目标提交。</p><form class="review-compare-form" @submit.prevent="loadComparison"><label>选择另一固定候选<select v-model="compareChoice" :disabled="candidateBusy || compareBusy"><option value="">{{ comparisonOptions.length ? '请选择固定候选' : '暂无其他固定候选' }}</option><option v-for="item in comparisonOptions" :key="item.key" :value="item.key">{{ item.label }}</option></select></label><button class="btn" :disabled="compareBusy || !compareChoice">{{ compareBusy ? '读取中…' : '读取固定候选' }}</button></form><p v-if="compareError" role="alert">{{ compareError }}</p><div class="review-notice"><strong>{{ other && !comparisonIssues.length ? '固定映射一致，可直接对照' : '尚不可直接比较' }}</strong><p>{{ comparisonIssues.join('；') || '空间、机位参数、证据类型、帧段与画幅均已核对。' }}</p><p v-if="!other">A/B/C 真实候选未登记时不生成替代图，不据缺项判断路线优胜。</p></div><template v-if="other"><div class="review-actions"><label>共同机位 <select v-model="camera"><option value="">未匹配</option><option v-for="c in commonCameras" :key="c" :value="c">{{ c }}</option></select></label><label>候选图像 <select v-model="selectedOther"><option v-for="(e,i) in otherImages" :key="e.evidence_id" :value="e.evidence_id">{{ roleLabels[e.role] || e.role }} {{ i+1 }}</option></select></label><button class="btn" :disabled="targetChangeBlocked || switching" @click="changeTarget({ key: canonicalJSON(other.target_locator), label: '比较候选', locator: other.target_locator })">转到已加载候选写意见</button></div><div class="review-candidate-switch"><button class="btn" @click="narrowCandidate = 'current'">当前目标</button><button class="btn" @click="narrowCandidate = 'other'">另一候选</button></div><div class="review-comparison" :data-candidate="narrowCandidate"><article data-candidate="current"><h3>当前目标 · 意见归属</h3><p class="review-muted">{{ candidateOptions.find(item => item.key === currentCandidateKey)?.label || title }}</p><button v-if="image && selectedEvidence" class="review-image" @click="preview(page,selectedEvidence)"><img :src="image.url" alt="当前固定候选"></button></article><article data-candidate="other"><h3>已加载候选 · 仅比较</h3><p class="review-muted">{{ otherLabel }}</p><button v-if="imageOther && otherEvidence" class="review-image" @click="preview(other,otherEvidence)"><img :src="imageOther.url" alt="另一固定候选"></button><p v-else class="review-muted">图像尚未读取或不可用。</p></article></div></template></section>
      <section class="review-section"><h2>审阅范围与意见</h2><p class="review-muted">当前浏览版本、人工结论、项目选用与资产发布相互独立。</p><div class="review-two"><form class="review-panel review-padded" @submit.prevent><h3>本次检查范围</h3><label class="review-radio"><input v-model="scopeChoice" type="radio" value="current">当前图像 · 仅图像阅读</label><label class="review-radio"><input v-model="scopeChoice" type="radio" value="images">本目标全部图片 · 仅图像阅读</label><label class="review-radio"><input v-model="scopeChoice" type="radio" value="camera" :disabled="!cameraOptions.some(option => option.evidenceIDs.length)">指定机位 · 仅已读图片范围</label>
              <fieldset v-if="scopeChoice === 'camera'" class="review-scope-options"><legend>选择已登记机位</legend><label v-for="option in cameraOptions" :key="option.id" class="review-radio"><input v-model="scopeCameras" type="checkbox" :value="option.id" :disabled="!option.evidenceIDs.length"><span>{{ option.id }} · {{ option.evidenceIDs.length ? option.evidenceIDs.length + ' 张已读图片' : '无已读图片映射，不可选' }}</span></label></fieldset>
              <p v-if="!cameraOptions.some(option => option.evidenceIDs.length)" class="review-muted">当前没有已登记且具备已读图片映射的机位。</p>
              <label class="review-radio"><input v-model="scopeChoice" type="radio" value="region" :disabled="!regionOptions.some(option => option.evidenceIDs.length)">指定区域 · 仅已读图片范围</label>
              <fieldset v-if="scopeChoice === 'region'" class="review-scope-options"><legend>选择已登记区域</legend><label v-for="option in regionOptions" :key="option.id" class="review-radio"><input v-model="scopeRegions" type="checkbox" :value="option.id" :disabled="!option.evidenceIDs.length"><span>{{ option.id }} · {{ option.evidenceIDs.length ? option.evidenceIDs.length + ' 张已读图片' : '无已读图片映射，不可选' }}</span></label></fieldset>
              <p v-if="!regionOptions.some(option => option.evidenceIDs.length)" class="review-muted">当前没有已登记且具备已读图片映射的区域。</p>
              <label class="review-radio"><input v-model="scopeChoice" type="radio" value="full" :disabled="!fullEligible">完整版本 · {{ fullEligible ? '必需证据已齐备' : '证据缺项，不可批准' }}</label>
              <p v-if="scopeResult.reason" class="review-muted" role="status">{{ scopeResult.reason }}</p>
              <p v-else-if="scope && (scope.kind === 'camera' || scope.kind === 'region')" class="review-muted">本次只包含所选{{ scope.kind === 'camera' ? '机位' : '区域' }}映射的 {{ scope.evidence_ids.length }} 张已读图片；未读图片与三维空间质量不在本次范围内。</p><label class="review-field">意见与定位<textarea v-model="text" maxlength="20000" :disabled="!page.capabilities.can_comment || saving || pending" placeholder="说明检查位置、机位、问题与适用范围。" /></label><p v-if="!page.capabilities.can_comment" class="review-muted">当前只读：{{ page.capabilities.reason_codes.map(labelGap).join('；') || '未取得审阅写权限。' }}</p><p v-else-if="!page.capabilities.can_decide" class="review-muted">可保存建议；当前会话不能记录人工结论。</p><div class="review-actions"><button class="btn" :disabled="!canComment" @click="prepare('comment',$event)">保存意见</button><button class="btn" :disabled="!canDecide" @click="prepare('request_changes',$event)">需修改</button><button class="btn primary" :disabled="!canDecide" @click="prepare('approve',$event)">通过本次范围</button></div></form><article class="review-panel review-padded"><h3>固定目标的记录 <span class="review-tag">{{ stateLabels[page.review_state] }}</span></h3><p v-if="!page.records.length" class="review-muted">此目标尚无当前有权读取的记录。</p><article v-for="r in page.records" :key="r.record_id" class="review-record"><div class="review-actions"><strong>{{ ({comment:'意见',request_changes:'需修改',approve:r.scope?.kind === 'full_target' ? '完整范围通过':'局部范围通过'} as Record<string,string>)[r.action || ''] || '受限记录' }}</strong><span class="review-tag">{{ r.actor?.origin === 'agent' ? 'AI 建议' : r.visibility === 'restricted' ? '受限记录' : '已认证用户' }}</span></div><p>{{ r.visibility === 'restricted' ? '当前权限不能读取此记录正文。' : r.body || r.record_view?.body || '正文未授权显示。' }}</p><small v-if="r.visibility !== 'restricted'">{{ r.scope?.kind === 'full_target' ? '完整版本范围' : '局部范围 · ' + (r.scope?.evidence_ids?.length || 0) + ' 项证据' }} · {{ r.created_at ? new Date(r.created_at).toLocaleString('zh-CN') : '' }}</small>
                <details v-if="r.visibility !== 'restricted' && r.scope" class="review-scope-evidence">
                  <summary>本条检查范围 · {{ scopeLabels[r.scope.kind] || '固定证据' }}</summary>
                  <p class="review-muted">{{ r.scope.claims.map(claim => claimLabels[claim] || '固定证据检查').join('、') }}</p>
                  <p v-if="recordMappings(r.scope, 'camera').length" class="review-muted">机位：{{ recordMappings(r.scope, 'camera').join('、') }}</p>
                  <p v-if="recordMappings(r.scope, 'region').length" class="review-muted">区域：{{ recordMappings(r.scope, 'region').join('、') }}</p>
                  <ul><li v-for="item in recordEvidence(r.scope)" :key="item.evidence_id">
                    <span>{{ evidenceLabel(item) }}</span>
                    <button class="btn" @click="item.mime.startsWith('image/') ? preview(page, item) : openAttachment(item)">{{ item.mime.startsWith('image/') ? '查看图像' : '阅读附件' }}</button>
                  </li></ul>
                  <p v-if="recordEvidence(r.scope).length < r.scope.evidence_ids.length || recordMappings(r.scope, 'camera').length < r.scope.camera_ids.length || recordMappings(r.scope, 'region').length < r.scope.region_ids.length" class="review-muted">部分范围当前不可读取，仅展示已授权的证据与映射。</p>
                </details>
              </article><button v-if="page.next_cursor" class="btn" @click="moreRecords">读取更多记录</button></article></div></section>
    </template>
    <dialog ref="dialog" class="review-dialog" @close="restoreTrigger">
      <h2>确认{{ ({comment:'保存意见',request_changes:'需修改',approve:'通过本次范围'} as Record<string,string>)[confirmAction] }}</h2>
      <template v-if="confirmedScope">
        <p>{{ title }} · {{ ({ full_target:'完整固定版本', camera:'指定机位的已读图片', region:'指定区域的已读图片', evidence:'所选固定图像' } as Record<string,string>)[confirmedScope.kind] }}</p>
        <p v-if="confirmedScope.camera_ids.length" class="review-body">机位：{{ confirmedScope.camera_ids.join('、') }}</p>
        <p v-if="confirmedScope.region_ids.length" class="review-body">区域：{{ confirmedScope.region_ids.join('、') }}</p>
        <details class="review-scope-evidence"><summary>本次 {{ confirmedScope.evidence_ids.length }} 项固定证据</summary><ul><li v-for="id in confirmedScope.evidence_ids" :key="id"><code>{{ id }}</code></li></ul></details>
        <p class="review-muted">{{ confirmedScope.kind === 'full_target' ? '结论适用于完整固定版本；' : '仅评价这些固定图像，不代表空间或工程质量；' }}不自动选用或发布。</p>
      </template>
      <p v-if="!confirmationMatches" class="review-notice" role="alert">{{ scopeResult.reason || '检查范围已变化，请返回编辑并重新确认。' }}</p>
      <p class="review-body">{{ text }}</p>
      <label v-if="previousDecision && confirmAction !== 'comment'" class="review-radio"><input v-model="supersede" type="checkbox">显式修正同范围的现有结论；原记录继续保留。</label>
      <div class="review-actions"><button class="btn" @click="dialog?.close()">继续编辑</button><button class="btn primary" :disabled="!(confirmAction === 'comment' ? canComment : canDecide) || !confirmationMatches || (!!previousDecision && confirmAction !== 'comment' && !supersede)" @click="submit">确认提交</button></div>
    </dialog>
    <dialog ref="attachmentDialog" class="review-dialog"><h2>{{ attachmentTitle }}</h2><pre>{{ attachment }}</pre><button class="btn" @click="attachmentDialog?.close()">关闭</button></dialog>
  </main>
</template>

<style scoped>
.set-review{color:var(--ink);max-width:1440px;margin:0 auto;padding:28px 36px 48px}.review-crumb{display:flex;gap:8px;flex-wrap:wrap;font-size:12px;color:var(--muted);margin-bottom:18px}.review-eyebrow{font-size:10px;letter-spacing:1.4px;color:var(--accent)}.review-actions{display:flex;gap:8px;align-items:center;flex-wrap:wrap}.review-tag{display:inline-flex;border:1px solid var(--line);border-radius:5px;padding:3px 8px;font-size:11px;color:var(--muted)}.review-fixed{display:flex;gap:15px;flex-wrap:wrap;padding:12px 0;font-size:12px;color:var(--muted)}.review-notice{border-left:2px solid var(--accent);background:var(--shell-panel);padding:14px 16px;margin:16px 0;border-radius:0 7px 7px 0;font-size:12px}.review-notice p{margin:6px 0}.review-panel{min-width:0;border:1px solid var(--line);border-radius:10px;background:var(--shell-panel);overflow:hidden;box-shadow:var(--vf-shadow-card)}.review-hero-grid{display:grid;grid-template-columns:minmax(0,1fr) 260px;gap:18px;margin-top:16px}.review-padded{padding:20px}.review-image{padding:0;width:100%;position:relative;background:var(--subtle);display:block;cursor:zoom-in}.review-image img{display:block;width:100%;max-height:70vh;object-fit:contain;min-height:180px}.review-image>span{position:absolute;right:12px;bottom:12px;border-radius:6px;background:var(--paper);color:var(--button-ink);padding:5px 9px;font-size:11px}.review-image-empty{min-height:280px;display:grid;align-content:center;justify-items:center;gap:12px;color:var(--muted);padding:30px}.review-caption{padding:16px;display:flex;gap:8px;flex-wrap:wrap;font-size:12px}.review-caption span,.review-caption small{color:var(--muted)}.review-padded h2,.review-section h2{font-size:18px;margin:0 0 12px}.review-padded h3{font-size:14px;margin-bottom:12px}.review-padded dl{font-size:12px}.review-padded dt{color:var(--muted);margin:14px 0 4px}.review-muted{color:var(--muted);font-size:12px;line-height:1.8;margin:10px 0}.review-section{margin:30px 0}.review-evidence-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(160px,1fr));gap:12px}.review-evidence{border:1px solid var(--line);border-radius:7px;text-align:left;overflow:hidden;background:var(--shell-panel);padding-bottom:12px}.review-evidence.active{border-color:var(--accent)}.review-evidence img{width:100%;aspect-ratio:16/10;object-fit:contain;display:block}.review-evidence strong,.review-evidence small{display:block;margin:8px 12px 0;font-size:11px;overflow-wrap:anywhere}.review-evidence small{color:var(--muted)}.review-evidence-placeholder{display:grid;place-items:center;aspect-ratio:16/10;background:var(--subtle);color:var(--muted);font-size:24px}.review-two{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:18px}.review-list{list-style:none}.review-list li{display:flex;gap:8px;flex-wrap:wrap;padding:11px 0;border-bottom:1px solid var(--line);font-size:12px}.review-list small,.review-list code{display:block;width:100%;overflow-wrap:anywhere;color:var(--muted);font-size:10px}.review-list span{margin-left:auto}.review-radio{display:flex;gap:8px;font-size:12px;align-items:flex-start;margin:12px 0}.review-radio input{margin-top:4px;accent-color:var(--accent)}.review-scope-options{border:1px solid var(--line);border-radius:7px;padding:8px 12px;margin:12px 0;min-width:0}.review-scope-options legend{font-size:12px;color:var(--muted)}.review-scope-options span{overflow-wrap:anywhere}.review-scope-evidence ul{padding-left:18px;margin:10px 0;font-size:11px;overflow-wrap:anywhere}.review-field,.review-compare-form label{display:grid;gap:7px;font-size:12px}.review-field textarea{width:100%;min-height:140px;resize:vertical;margin-bottom:14px}.set-review input:not([type]),.set-review textarea,.set-review select{color:var(--ink);border:1px solid var(--line);border-radius:7px;background:var(--panel);padding:8px;font:inherit;max-width:100%}.review-record{padding:15px 0;border-bottom:1px solid var(--line);font-size:12px}.review-record p,.review-body{white-space:pre-wrap;overflow-wrap:anywhere;margin:10px 0}.review-record small{color:var(--muted);font-size:10px}.review-compare-form{display:flex;gap:10px;align-items:end;margin:15px 0}.review-compare-form label{flex:1;min-width:0}.review-comparison{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px;margin-top:16px}.review-comparison h3{font-size:13px;margin-bottom:10px}.review-candidate-switch{display:none}.review-state{padding:45px 20px;text-align:center;background:var(--shell-panel);border-radius:10px;color:var(--muted)}.review-state p{margin:14px 0}.review-dialog{width:min(600px,calc(100vw - 32px));max-height:80vh;margin:auto;padding:24px;border:1px solid var(--line);border-radius:12px;background:var(--panel);color:var(--ink)}.review-dialog::backdrop{background:rgba(0,0,0,.55)}.review-dialog h2{font-size:20px;margin-bottom:16px}.review-dialog pre{white-space:pre-wrap;overflow-wrap:anywhere;font-size:12px;line-height:1.8;margin:16px 0}.review-dialog .review-actions{margin-top:20px}.set-review details{margin-top:14px}.set-review summary{cursor:pointer;font-size:12px}.set-review :focus-visible{outline:2px solid var(--accent);outline-offset:3px}
@media(max-width:1050px){.review-hero-grid{grid-template-columns:1fr}.review-two{grid-template-columns:1fr}}@media(max-width:600px){.set-review{padding:20px 16px 36px}.review-evidence-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.review-compare-form{align-items:stretch;flex-direction:column}.review-candidate-switch{display:flex;gap:8px;margin:12px 0}.review-comparison{grid-template-columns:1fr}.review-comparison[data-candidate=current]>[data-candidate=other],.review-comparison[data-candidate=other]>[data-candidate=current]{display:none}.review-padded{padding:15px}.review-image-empty{min-height:180px}.review-caption{padding:12px}}
</style>
