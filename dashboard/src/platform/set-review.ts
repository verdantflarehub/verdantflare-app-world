import type { AppSDK, Context, Json } from './types.ts'

export type ReviewLocator = { kind: 'project'; project_id: string; content_revision_id: string; material_key: string; state_key: string; version_key: string } | { kind: 'world'; asset_id: string; asset_version_id: string }
export interface ReviewScope { kind: 'evidence' | 'camera' | 'region' | 'full_target'; evidence_ids: string[]; camera_ids: string[]; region_ids: string[]; claims: string[] }
export interface Evidence { evidence_id: string; file_id: string; content_ref: { store_id: string; artifact_id: string; version_id: string }; sha256: string; size_bytes: number; mime: string; role: string; camera_ids: string[]; region_ids: string[]; frame_range: unknown }
export interface TargetView { schema_version: number; target_locator: ReviewLocator; definition: Record<string, unknown>; evidence: Evidence[]; coverage: { camera_ids: string[]; region_ids: string[]; [key: string]: unknown }; requirements: { requirement_id: string; claim: string; evidence_ids: string[]; state: string; reason_code: string }[]; dependencies: Record<string, unknown>[]; source_manifest: unknown }
export interface ReviewRecord { record_id: string; target_hash?: string; action?: 'comment' | 'request_changes' | 'approve'; scope?: ReviewScope; body?: string; actor?: { origin?: string; subject_id?: string }; created_at?: string; visibility?: string; record_view?: Partial<ReviewRecord> | null }
export interface ReviewOpen { target_locator: ReviewLocator; target_hash: string | null; target_view: TargetView | null; mapping_state?: string; availability: { code: string; evidence_id?: string }[]; missing: { code: string; evidence_id?: string }[]; records_head_record_id: string | null; review_state: string; scope_decisions: { scope_hash: string; scope: ReviewScope; action: string; record_id: string }[]; records: ReviewRecord[]; next_cursor: string | null; capabilities: { can_comment: boolean; can_decide: boolean; reason_codes: string[] }; head_revision_id?: string; records_revision_id?: string }
export interface ReviewAppend { target_locator: ReviewLocator; expected_target_hash: string; commit_id: string; expected_record_id: string | null; expected_revision_id?: string; action: 'comment' | 'request_changes' | 'approve'; scope: ReviewScope; body: string; supersedes_record_id?: string }
export interface ReviewResult { state: 'committed'; commit_id: string; record_id: string; target_hash: string; review_state: string; records_head_record_id: string; revision_id?: string; projection_applied?: boolean }
export interface Recovery { request: ReviewAppend; requestHash: string }
export interface StorageLike { getItem(key: string): string | null; setItem(key: string, value: string): void; removeItem(key: string): void }
export type ReviewScopeChoice = 'current' | 'images' | 'camera' | 'region' | 'full'
export interface ReviewScopeSelection { kind: ReviewScopeChoice; evidenceID?: string; cameraIDs?: string[]; regionIDs?: string[] }
export function fullReviewEligible(page?: ReviewOpen): boolean {
  const target = page?.target_view
  return !!target && target.requirements.length > 0 && ['space_layout','source_recovery','full_set'].every(claim => target.requirements.some(r => r.claim === claim && r.state === 'satisfied')) && target.requirements.every(r => r.state === 'satisfied') && !page?.availability.length
}
export function mappedReviewOptions(page: ReviewOpen | undefined, kind: 'camera' | 'region', readableIDs: readonly string[]) {
  const target = page?.target_view, field = kind === 'camera' ? 'camera_ids' : 'region_ids'
  return (target?.coverage[field] || []).map(id => ({ id, evidenceIDs: target!.evidence.filter(e => e.mime.startsWith('image/') && readableIDs.includes(e.evidence_id) && e[field].includes(id)).map(e => e.evidence_id).sort() }))
}
export function resolveReviewScope(page: ReviewOpen | undefined, selection: ReviewScopeSelection, readableIDs: readonly string[]): { scope?: ReviewScope; reason: string } {
  const target = page?.target_view
  if (!target || !page?.target_hash) return { reason: '尚无已授权的固定审阅目标。' }
  if (selection.kind === 'full') {
    if (!fullReviewEligible(page)) return { reason: '完整版本已不具备必需证据，请重新选择检查范围。' }
    return { scope: { kind: 'full_target', evidence_ids: target.evidence.map(e => e.evidence_id).sort(), camera_ids: [...target.coverage.camera_ids].sort(), region_ids: [...target.coverage.region_ids].sort(), claims: ['full_set','source_recovery','space_layout'] }, reason: '' }
  }
  const scope: ReviewScope = { kind: 'evidence', evidence_ids: [], camera_ids: [], region_ids: [], claims: ['image_reading'] }
  if (selection.kind === 'camera' || selection.kind === 'region') {
    const ids = selection.kind === 'camera' ? selection.cameraIDs || [] : selection.regionIDs || [], options = mappedReviewOptions(page, selection.kind, readableIDs), label = selection.kind === 'camera' ? '机位' : '区域'
    if (!ids.length) return { reason: `请选择有实际可读图片映射的${label}。` }
    if (new Set(ids).size !== ids.length || ids.some(id => !options.some(option => option.id === id && option.evidenceIDs.length))) return { reason: `所选${label}缺少已登记的可读图片映射，请重新选择。` }
    scope.kind = selection.kind
    scope[selection.kind === 'camera' ? 'camera_ids' : 'region_ids'] = [...ids].sort()
    scope.evidence_ids = [...new Set(options.filter(option => ids.includes(option.id)).flatMap(option => option.evidenceIDs))].sort()
  } else {
    const images = target.evidence.filter(e => e.mime.startsWith('image/'))
    const selected = selection.kind === 'images' ? images : images.filter(e => e.evidence_id === selection.evidenceID)
    if (!selected.length || selected.some(e => !readableIDs.includes(e.evidence_id))) return { reason: selection.kind === 'images' ? '本目标图片尚未全部读取，请逐张检查后提交。' : '当前固定图片尚未读取成功，不能提交此范围。' }
    scope.evidence_ids = selected.map(e => e.evidence_id).sort()
  }
  return { scope, reason: '' }
}
export const reviewID = (v: unknown): v is string => typeof v === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v)
const hashOK = (v: unknown): v is string => typeof v === 'string' && /^[0-9a-f]{64}$/.test(v)
const obj = (v: unknown): Record<string, unknown> => v && typeof v === 'object' && !Array.isArray(v) ? v as Record<string, unknown> : {}
const identity = (c: Readonly<Context>) => JSON.stringify([c.userId, c.organizationId, c.stationId, c.projectId, c.appId, c.installationId, c.contextRevision])
const owner = (l: ReviewLocator) => l.kind === 'project' ? l.project_id : l.asset_id
export class ReviewError extends Error { constructor(readonly code: string, readonly status = 0) { super(code) } }
export function reviewMessage(error: unknown): string {
  const code = error instanceof ReviewError ? error.code : ''
  const messages: Record<string, string> = { PERMISSION_DENIED: '没有当前目标的访问或审阅权限。', SESSION_REVOKED: '会话已变化，请重新打开应用。', LEGACY_TARGET_UNMAPPED: '版本关联待补齐，历史附件仅供阅读。', TARGET_MISMATCH: '固定目标已变化，未迁移旧意见。请重新核对目标。', REVISION_CONFLICT: '项目已产生新修订，请保留意见并重新读取。', REVIEW_CONFLICT: '已有新的审阅记录，请重读后重新决定。', IDEMPOTENCY_CONFLICT: '原请求编号不匹配，已停止提交并保留恢复记录。', CONTENT_NOT_READY: '固定内容暂时不可读，请稍后重试。', NOT_FOUND: '未找到固定对象或原请求。不会改为读取最新版本。', REVIEW_SCOPE_INCOMPLETE: '本次检查范围缺少所需证据，不能提交此结论。', STORAGE_UNAVAILABLE: '无法保存请求恢复记录，本次未提交。', INVALID_RESPONSE: '返回内容与固定目标不一致，已停止操作。', READBACK_FAILED: '已提交，回读失败。请恢复原请求结果，不要重复新建。', STATE_UNKNOWN: '尚未确认提交结果，请查询原请求。' }
  return messages[code] || (error instanceof Error && error.name === 'AbortError' ? '页面已隐藏，恢复后会重新验证读取权限。' : '读取或保存未完成，请保留输入并重试原操作。')
}
export function canonicalJSON(value: unknown): string {
  if (value === null || typeof value !== 'object') return JSON.stringify(value)
  if (Array.isArray(value)) return '[' + value.map(canonicalJSON).join(',') + ']'
  return '{' + Object.keys(value as object).sort().map(key => JSON.stringify(key) + ':' + canonicalJSON((value as Record<string, unknown>)[key])).join(',') + '}'
}
export async function reviewRequestHash(value: unknown) { return [...new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(canonicalJSON(value))))].map(v => v.toString(16).padStart(2, '0')).join('') }
export function newReviewCommit() {
  const bytes = crypto.getRandomValues(new Uint8Array(16)); let time = Date.now()
  for (let i = 5; i >= 0; i--) { bytes[i] = time % 256; time = Math.floor(time / 256) }
  bytes[6] = (bytes[6] & 15) | 112; bytes[8] = (bytes[8] & 63) | 128
  const h = [...bytes].map(v => v.toString(16).padStart(2, '0')).join('')
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`
}
export function validLocator(v: unknown): v is ReviewLocator {
  const l = obj(v), key = (x: unknown) => typeof x === 'string' && /^[A-Za-z0-9_-]{1,128}$/.test(x)
  return l.kind === 'project' ? reviewID(l.project_id) && reviewID(l.content_revision_id) && key(l.material_key) && (l.state_key === '' || key(l.state_key)) && (l.version_key === '' || key(l.version_key)) : l.kind === 'world' && reviewID(l.asset_id) && reviewID(l.asset_version_id)
}
export function reviewPath(locator: ReviewLocator, hash?: string | null) {
  const q = new URLSearchParams()
  let path: string
  if (locator.kind === 'project') {
    path = `/ai_short_drama/${locator.project_id}/materials/location/${locator.material_key}/review`
    q.set('revision', locator.content_revision_id)
    if (locator.version_key) q.set('version', locator.version_key)
    if (locator.state_key) q.set('state', locator.state_key)
  } else path = `/scene/${locator.asset_id}/versions/${locator.asset_version_id}/review`
  if (hash) q.set('target', hash)
  return path + (q.size ? '?' + q.toString() : '')
}
export function parseReviewPath(path: string): { locator: ReviewLocator; hash?: string } | undefined {
  if (path.length > 2048 || !path.startsWith('/') || path.startsWith('//') || /[\\#]/.test(path)) return
  const u = new URL(path, 'https://app.invalid'), p = u.pathname.split('/').filter(Boolean)
  let duplicate = false; u.searchParams.forEach((_, k) => { if (u.searchParams.getAll(k).length > 1) duplicate = true })
  if (u.origin !== 'https://app.invalid' || u.pathname !== path.split('?')[0] || u.pathname !== '/' + p.join('/') || duplicate) return
  let l: ReviewLocator
  if (p.length === 6 && p[0] === 'ai_short_drama' && p[2] === 'materials' && p[3] === 'location' && p[5] === 'review') l = { kind: 'project', project_id: p[1], content_revision_id: u.searchParams.get('revision') || '', material_key: p[4], state_key: u.searchParams.get('state') || '', version_key: u.searchParams.get('version') || '' }
  else if (p.length === 5 && p[0] === 'scene' && p[2] === 'versions' && p[4] === 'review') l = { kind: 'world', asset_id: p[1], asset_version_id: p[3] }
  else return
  let unknown = false; u.searchParams.forEach((_, k) => { if (!(l.kind === 'project' ? ['revision','state','version','target'] : ['target']).includes(k)) unknown = true }); if (unknown) return
  const hash = u.searchParams.get('target') || undefined
  return validLocator(l) && (!hash || hashOK(hash)) ? { locator: l, hash } : undefined
}
export function readReview(value: unknown, locator: ReviewLocator, expectedHash?: string): ReviewOpen {
  const v = obj(value), l = v.target_locator
  if (!validLocator(l) || l.kind !== locator.kind || owner(l) !== owner(locator) || (l.kind === 'world' && locator.kind === 'world' && l.asset_version_id !== locator.asset_version_id) || (l.kind === 'project' && locator.kind === 'project' && (l.material_key !== locator.material_key || l.state_key !== locator.state_key || l.version_key !== locator.version_key))) throw new ReviewError('INVALID_RESPONSE')
  if (v.target_hash !== null && !hashOK(v.target_hash) || expectedHash && v.target_hash !== expectedHash || !['unreviewed', 'in_review', 'approved', 'rejected'].includes(String(v.review_state))) throw new ReviewError('INVALID_RESPONSE')
  const c = obj(v.capabilities), t = v.target_view === null ? null : obj(v.target_view)
  if (typeof c.can_comment !== 'boolean' || typeof c.can_decide !== 'boolean' || !Array.isArray(c.reason_codes) || !Array.isArray(v.records) || !Array.isArray(v.missing) || !Array.isArray(v.availability) || !Array.isArray(v.scope_decisions) || (v.records_head_record_id !== null && !reviewID(v.records_head_record_id)) || (v.next_cursor != null && typeof v.next_cursor !== 'string')) throw new ReviewError('INVALID_RESPONSE')
  if (!hashOK(v.target_hash) && (c.can_comment || c.can_decide)) throw new ReviewError('INVALID_RESPONSE')
  if (t && (t.schema_version !== 1 || canonicalJSON(t.target_locator) !== canonicalJSON(l) || !Array.isArray(t.evidence) || !Array.isArray(t.requirements) || !Array.isArray(t.dependencies) || !obj(t.coverage) || !Array.isArray(obj(t.coverage).camera_ids) || !Array.isArray(obj(t.coverage).region_ids))) throw new ReviewError('INVALID_RESPONSE')
  for (const raw of (t?.evidence || []) as unknown[]) {
    const e = obj(raw), ref = obj(e.content_ref)
    if (typeof e.evidence_id !== 'string' || !reviewID(e.file_id) || ![ref.store_id, ref.artifact_id, ref.version_id].every(reviewID) || typeof e.mime !== 'string' || typeof e.role !== 'string' || !Array.isArray(e.camera_ids) || !Array.isArray(e.region_ids) || [...e.camera_ids, ...e.region_ids].some(id => typeof id !== 'string' || !id) || (e.sha256 !== '' && !hashOK(e.sha256)) || !Number.isSafeInteger(e.size_bytes) || Number(e.size_bytes) < 0) throw new ReviewError('INVALID_RESPONSE')
  }
  if (v.record_views !== undefined || !t && (c.can_comment || c.can_decide)) throw new ReviewError('INVALID_RESPONSE')
  for (const raw of v.records) {
    const r = obj(raw)
    if (!reviewID(r.record_id)) throw new ReviewError('INVALID_RESPONSE')
    if (r.visibility === 'restricted') {
      if (r.record_view !== null || Object.keys(r).some(k => !['record_id','visibility','record_view'].includes(k))) throw new ReviewError('INVALID_RESPONSE')
    } else if (r.target_hash !== v.target_hash || !['comment','request_changes','approve'].includes(String(r.action)) || typeof r.body !== 'string' || !Array.isArray(obj(r.scope).evidence_ids)) throw new ReviewError('INVALID_RESPONSE')
  }
  return v as unknown as ReviewOpen
}
export function createReviewClient(sdk: AppSDK, domain: 'project' | 'world', locator: ReviewLocator, storage: StorageLike) {
  const context = sdk.context.get(), initial = identity(context)
  if (context.appId !== domain || locator.kind !== domain || !validLocator(locator)) throw new ReviewError('INVALID_RESPONSE')
  const key = 'set-review.pending.v1:' + JSON.stringify([context.userId, context.organizationId, context.stationId, context.installationId, domain, owner(locator)])
  let disposed = false, hidden = false, working = false, generation = 0, terminal = false
  const controllers = new Set<AbortController>()
  function live(g = generation) { if (disposed || hidden || g !== generation || identity(sdk.context.get()) !== initial) throw new ReviewError('SESSION_REVOKED') }
  function cancel() { generation++; controllers.forEach(c => c.abort()); controllers.clear() }
  const stopContext = sdk.context.subscribe(c => { if (identity(c) !== initial) { disposed = true; cancel() } })
  const stopLife = sdk.lifecycle.subscribe(e => { hidden = e === 'hidden'; if (e === 'dispose') disposed = true; if (hidden || disposed) cancel() })
  function pending(): Recovery | undefined {
    let raw: string | null
    try { raw = storage.getItem(key) } catch { throw new ReviewError('STORAGE_UNAVAILABLE') }
    if (!raw) return
    try {
      if (raw.length > 1200000) throw 0
      const v = JSON.parse(raw) as Recovery, r = v.request
      if (!hashOK(v.requestHash) || !validLocator(r.target_locator) || r.target_locator.kind !== domain || owner(r.target_locator) !== owner(locator) || !reviewID(r.commit_id) || !hashOK(r.expected_target_hash) || typeof r.body !== 'string' || Object.keys(r).some(k => !['target_locator','expected_target_hash','commit_id','expected_record_id','expected_revision_id','action','scope','body','supersedes_record_id'].includes(k))) throw 0
      return v
    } catch { throw new ReviewError('INVALID_RESPONSE') }
  }
  async function exact() { const r = pending(); if (!r || await reviewRequestHash(r.request) !== r.requestHash || canonicalJSON(pending()) !== canonicalJSON(r)) throw new ReviewError('INVALID_RESPONSE'); live(); return r }
  async function request(operation: 'open' | 'append' | 'status', body: unknown): Promise<unknown> {
    live(); const g = generation, controller = new AbortController(); controllers.add(controller)
    try {
      const response = await sdk.app.request({ method: 'POST', path: `/api/v1/${domain}/review/${operation}`, body: body as Json }, { signal: controller.signal })
      live(g)
      if (response.status !== 200) {
        if (operation === 'append' && [400, 409, 422].includes(response.status)) terminal = true
        throw new ReviewError(String(obj(response.data).code || (response.status === 403 ? 'PERMISSION_DENIED' : 'STATE_UNKNOWN')), response.status)
      }
      return response.data
    } finally { controllers.delete(controller) }
  }
  async function open(l = locator, hash?: string, cursor?: string, recordsRevision?: string) {
    return readReview(await request('open', { target_locator: l, ...(hash ? { expected_target_hash: hash } : {}), ...(cursor ? { cursor } : {}), ...(recordsRevision ? { records_revision_id: recordsRevision } : {}) }), l, hash)
  }
  function result(value: unknown, r: ReviewAppend): ReviewResult {
    const v = obj(value)
    if (v.state !== 'committed' || v.commit_id !== r.commit_id || v.target_hash !== r.expected_target_hash || !reviewID(v.record_id) || !reviewID(v.records_head_record_id)) throw new ReviewError('INVALID_RESPONSE')
    return v as unknown as ReviewResult
  }
  async function begin(page: ReviewOpen, action: ReviewAppend['action'], scope: ReviewScope, body: string, supersedes?: string) {
    live(); if (page.target_locator.kind !== domain || owner(page.target_locator) !== owner(locator)) throw new ReviewError('INVALID_RESPONSE'); if (working || pending() || !page.target_hash || !(action === 'comment' ? page.capabilities.can_comment : page.capabilities.can_decide)) throw new ReviewError('PERMISSION_DENIED')
    if (!body.trim() || [...body].length > 20000) throw new ReviewError('INVALID_RESPONSE')
    if (domain === 'project' && !reviewID(page.head_revision_id)) throw new ReviewError('INVALID_RESPONSE')
    const r: ReviewAppend = { target_locator: page.target_locator, expected_target_hash: page.target_hash, commit_id: newReviewCommit(), expected_record_id: page.records_head_record_id, ...(domain === 'project' ? { expected_revision_id: page.head_revision_id } : {}), action, scope: { ...scope, evidence_ids: [...scope.evidence_ids].sort(), camera_ids: [...scope.camera_ids].sort(), region_ids: [...scope.region_ids].sort(), claims: [...scope.claims].sort() }, body: body.normalize('NFC').trim(), ...(supersedes ? { supersedes_record_id: supersedes } : {}) }
    const record = { request: r, requestHash: await reviewRequestHash(r) }; live()
    if (pending()) throw new ReviewError('REVIEW_CONFLICT')
    try { const raw = JSON.stringify(record); storage.setItem(key, raw); if (storage.getItem(key) !== raw) throw 0 } catch { throw new ReviewError('STORAGE_UNAVAILABLE') }
    terminal = false
  }
  async function submit() {
    if (working) throw new ReviewError('STATE_UNKNOWN'); working = true
    try { const r = await exact(); return result(await request('append', r.request), r.request) } finally { working = false }
  }
  async function status(): Promise<{ state: string; result?: ReviewResult }> {
    if (working) throw new ReviewError('STATE_UNKNOWN'); working = true
    try {
      const r = await exact()
      const v = obj(await request('status', { [domain === 'project' ? 'project_id' : 'asset_id']: owner(r.request.target_locator), commit_id: r.request.commit_id }))
      if (v.request_sha256 !== r.requestHash || !['preparing','committed','rejected'].includes(String(v.state))) throw new ReviewError('INVALID_RESPONSE')
      terminal = v.state === 'rejected'
      return { state: String(v.state), result: v.state === 'committed' ? result(v.result, r.request) : undefined }
    } finally { working = false }
  }
  async function readback(r: ReviewResult) {
    const original = await exact(); let page = await open(original.request.target_locator, r.target_hash), all = [...page.records], cursor = page.next_cursor
    const head = page.records_head_record_id
    for (let i = 0; !all.some(v => v.record_id === r.record_id) && cursor && i < 100; i++) {
      const next = await open(original.request.target_locator, r.target_hash, cursor, page.records_revision_id)
      if (next.records_head_record_id !== head || next.next_cursor === cursor) throw new ReviewError('READBACK_FAILED')
      all.push(...next.records); cursor = next.next_cursor
    }
    if (!all.some(v => v.record_id === r.record_id && v.target_hash === r.target_hash)) throw new ReviewError('READBACK_FAILED')
    if (canonicalJSON(pending()) !== canonicalJSON(original)) throw new ReviewError('INVALID_RESPONSE')
    page = { ...page, records: all, next_cursor: cursor }; terminal = true; clear(); return page
  }
  function clear() { live(); if (!terminal || working) throw new ReviewError('STATE_UNKNOWN'); try { storage.removeItem(key); if (storage.getItem(key)) throw 0 } catch { throw new ReviewError('STORAGE_UNAVAILABLE') } terminal = false }
  return { open, begin, submit, status, readback, pending, clearRejected: clear, isTerminal: () => terminal, dispose: () => { disposed = true; cancel(); stopContext(); stopLife() } }
}
export function controlledReviewPath(value: unknown) {
  if (typeof value !== 'string' || !/^\/studio\/dashboards\/[0-9a-f-]{36}\/content\/[0-9a-f-]{36}$/.test(value)) throw new ReviewError('INVALID_RESPONSE')
  return value
}
/** Consume the viewer's approved fixed-package route; the host resolves installation and authority. */
export function reviewViewerTarget(locator: ReviewLocator, manifest: unknown) {
  const value = obj(manifest)
  if (!validLocator(locator) || value.schema_version !== '1.0' || typeof value.package_id !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/.test(value.package_id)) throw new ReviewError('INVALID_RESPONSE')
  const query = new URLSearchParams(locator.kind === 'project' ? { revision: locator.content_revision_id, pkg: value.package_id } : { version: locator.asset_version_id, pkg: value.package_id })
  return { appId: 'viewer', path: `/${locator.kind}/${owner(locator)}?${query}` }
}
export async function readEvidence(sdk: AppSDK, locator: ReviewLocator, e: Evidence, signal: AbortSignal): Promise<Blob> {
  const access = locator.kind === 'project' ? { project_id: locator.project_id, project_revision_id: locator.content_revision_id } : { asset_id: locator.asset_id, asset_version_id: locator.asset_version_id }
  const r = obj(await sdk.capabilities.call('artifact.read', { mode: 'download', content_ref: e.content_ref, access } as unknown as Json, { signal })), v = obj(r.version)
  if (v.store_id !== e.content_ref.store_id || v.artifact_id !== e.content_ref.artifact_id || v.version_id !== e.content_ref.version_id || !hashOK(v.sha256) || typeof v.mime !== 'string' || !Number.isSafeInteger(v.size) || Number(v.size) <= 0 || Number(v.size) > 128 * 1024 * 1024 || e.sha256 && v.sha256 !== e.sha256 || e.size_bytes && v.size !== e.size_bytes) throw new ReviewError('INVALID_RESPONSE')
  const response = await fetch(controlledReviewPath(r.content_path), { credentials: 'same-origin', cache: 'no-store', redirect: 'error', signal })
  if (!response.ok) throw new ReviewError(response.status === 403 ? 'PERMISSION_DENIED' : 'CONTENT_NOT_READY', response.status)
  const bytes = await response.arrayBuffer()
  if (signal.aborted || bytes.byteLength !== v.size || await reviewRequestHashBytes(bytes) !== v.sha256) throw new ReviewError('INVALID_RESPONSE')
  return new Blob([bytes], { type: v.mime.split(';')[0].toLowerCase() })
}
async function reviewRequestHashBytes(bytes: ArrayBuffer) { return [...new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))].map(v => v.toString(16).padStart(2, '0')).join('') }

/** No pairing by label: matching requires fixed spatial and camera mappings, identical role, frame range and image dimensions. */
export function compareEvidence(a: TargetView, x: Evidence, b: TargetView, y: Evidence, dimensions: [{ width: number; height: number }, { width: number; height: number }], camera: string): string[] {
  const reasons: string[] = [], ac = obj(a.coverage), bc = obj(b.coverage)
  if (!camera || !x.camera_ids.includes(camera) || !y.camera_ids.includes(camera)) reasons.push('缺少共同机位对应关系')
  const ab = obj(ac.space_basis), bb = obj(bc.space_basis)
  if (!hashOK(ab.sha256) || ab.sha256 !== bb.sha256 || !a.evidence.some(e=>e.evidence_id===ab.evidence_id&&e.sha256===ab.sha256) || !b.evidence.some(e=>e.evidence_id===bb.evidence_id&&e.sha256===bb.sha256)) reasons.push('空间基线未核实或不同')
  const am = obj((Array.isArray(ac.cameras)?ac.cameras:[]).find(v=>obj(v).camera_id===camera)), bm = obj((Array.isArray(bc.cameras)?bc.cameras:[]).find(v=>obj(v).camera_id===camera))
  if (am.verified !== true || bm.verified !== true || !hashOK(am.parameters_sha256) || am.parameters_sha256 !== bm.parameters_sha256 || !(Array.isArray(am.evidence_ids)&&am.evidence_ids.includes(x.evidence_id)) || !(Array.isArray(bm.evidence_ids)&&bm.evidence_ids.includes(y.evidence_id)) || !a.evidence.some(e=>e.evidence_id===am.parameters_evidence_id&&e.sha256===am.parameters_sha256) || !b.evidence.some(e=>e.evidence_id===bm.parameters_evidence_id&&e.sha256===bm.parameters_sha256)) reasons.push('相机参数映射未核实或不同')
  if (canonicalJSON(x.content_ref) === canonicalJSON(y.content_ref) || x.sha256 === y.sha256) reasons.push('需要两份不同的固定候选证据')
  if (x.role !== y.role || !['render','photograph','generated','composite'].includes(x.role)) reasons.push('证据类型不匹配或尚未登记')
  if (canonicalJSON(x.frame_range) !== canonicalJSON(y.frame_range)) reasons.push('帧段或帧率不一致')
  if (!dimensions[0].width || !dimensions[1].width || dimensions[0].width !== dimensions[1].width || dimensions[0].height !== dimensions[1].height) reasons.push('原始画幅不同或尚未读取')
  return reasons
}
