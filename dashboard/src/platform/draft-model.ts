import { isID } from './world-model.ts'
export type Purpose = 'collection' | 'learning' | 'creation'
export interface ContentRef { store_id: string; artifact_id: string; version_id: string }
export interface DraftFile { file_id: string; path: string; role: string; content_ref: ContentRef }
export interface DraftManifest {
  schema_version: 1; kind: 'world-draft'; draft_id: string; purpose: Purpose; name: string; subjects: string[]
  files: DraftFile[]; domain_documents: { document_type: string; file_id: string }[]
  source_refs: Record<string, unknown>[]; asset_refs: Record<string, unknown>[]; run_refs: { service_id: string; run_id: string }[]
}
export interface DraftRevision { lifecycle?: DraftLifecycle; kind: 'revision'; draft_id: string; revision_id: string; head_revision_id: string; created_at: string; manifest_ref: ContentRef; manifest: DraftManifest }
export interface DraftItem { lifecycle?: DraftLifecycle; draft_id: string; head_revision_id: string; name: string; purpose: Purpose; subjects: string[]; has_content: boolean; created_at: string; updated_at: string }
export const object = (value: unknown): Record<string, unknown> => {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw Error('返回的数据结构不完整。')
  return value as Record<string, unknown>
}
const invalid = (): never => { throw Error('返回的数据与所选收录或固定版本不一致。') }
const text = (v: unknown, max = 256): v is string => typeof v === 'string' && v.trim().length > 0 && v.length <= max
const strings = (v: unknown): v is string[] => Array.isArray(v) && v.length <= 256 && v.every(x => text(x))
const purposes = ['collection', 'learning', 'creation']
export function contentRef(v: unknown): ContentRef {
  const ref = object(v)
  if (!isID(ref.store_id) || !isID(ref.artifact_id) || !isID(ref.version_id)) invalid()
  return { store_id: ref.store_id as string, artifact_id: ref.artifact_id as string, version_id: ref.version_id as string }
}
export function readDraftList(v: unknown, purpose: Purpose): { items: DraftItem[]; next_cursor: string | null } {
  const data = object(v)
  if (data.kind !== 'list' || !Array.isArray(data.items) || data.items.length > 100 || !(data.next_cursor === null || text(data.next_cursor, 2048))) invalid()
  const seen = new Set<string>()
  for (const value of data.items as unknown[]) {
    const item = object(value)
    if (!isID(item.draft_id) || !isID(item.head_revision_id) || item.purpose !== purpose || !text(item.name) || !strings(item.subjects) || typeof item.has_content !== 'boolean' || !text(item.created_at) || !text(item.updated_at) || seen.has(item.draft_id)) invalid()
    readLifecycle(item.lifecycle)
    seen.add(item.draft_id as string)
  }
  return data as unknown as { items: DraftItem[]; next_cursor: string | null }
}
export function readDraft(v: unknown, draft?: string, revision?: string, purpose?: Purpose): DraftRevision {
  const data = object(v), m = object(data.manifest)
  if (data.kind !== 'revision' || !isID(data.draft_id) || !isID(data.revision_id) || !isID(data.head_revision_id) || !text(data.created_at) ||
    draft && data.draft_id !== draft || revision && data.revision_id !== revision || purpose && m.purpose !== purpose ||
    m.schema_version !== 1 || m.kind !== 'world-draft' || m.draft_id !== data.draft_id || !purposes.includes(String(m.purpose)) || !text(m.name) || !strings(m.subjects)) invalid()
  readLifecycle(data.lifecycle)
  contentRef(data.manifest_ref)
  for (const [key, limit] of [['files', 10000], ['domain_documents', 256], ['source_refs', 1000], ['asset_refs', 10000], ['run_refs', 1000]] as const) {
    if (!Array.isArray(m[key]) || (m[key] as unknown[]).length > limit) invalid()
  }
  const ids = new Set<string>(), paths = new Set<string>(), domains = new Set<string>()
  for (const raw of m.files as unknown[]) {
    const f = object(raw)
    if (!isID(f.file_id) || !text(f.path, 4096) || !text(f.role) || ids.has(f.file_id) || paths.has(String(f.path).normalize('NFC').toLowerCase()) || /[\\\x00-\x1f]/.test(String(f.path)) || String(f.path).split('/').some(x => !x || x === '.' || x === '..')) invalid()
    contentRef(f.content_ref); ids.add(f.file_id as string); paths.add(String(f.path).normalize('NFC').toLowerCase())
  }
  for (const raw of m.domain_documents as unknown[]) {
    const d = object(raw)
    if (!text(d.document_type) || !ids.has(String(d.file_id)) || domains.has(String(d.document_type))) invalid()
    domains.add(String(d.document_type))
  }
  return data as unknown as DraftRevision
}
export interface DraftLifecycle { state: 'active' | 'trashed'; version: number; trashed_at: string | null; trashed_by: string | null; can_trash: boolean; can_restore: boolean }
export function readLifecycle(value: unknown): DraftLifecycle | undefined {
  if (value === undefined) return undefined
  const v = object(value)
  if (!['active','trashed'].includes(String(v.state)) || !Number.isSafeInteger(v.version) || Number(v.version)<1 || typeof v.can_trash !== 'boolean' || typeof v.can_restore !== 'boolean') invalid()
  if (v.state === 'trashed') {
    if (!isID(v.trashed_by) || typeof v.trashed_at !== 'string' || !Number.isFinite(Date.parse(v.trashed_at)) || v.can_trash) invalid()
  } else if (v.trashed_by !== null || v.trashed_at !== null || v.can_restore) invalid()
  return v as unknown as DraftLifecycle
}
export interface CollectionForm { name: string; text: string; url: string }
export interface CollectionDocument extends Record<string, unknown> { schema_version: 1 | 2; document_kind: 'reference-learning'; origin: string; sources: Record<string, unknown>[]; results: unknown[]; coverage: unknown[]; relations: unknown[]; uses: unknown[] }
export function collectionForm(input: CollectionForm): CollectionForm {
  const value = { name: input.name.trim(), text: input.text, url: input.url.trim() }
  if (!value.name || value.name.length > 256 || (!value.text.trim() && !value.url) || new TextEncoder().encode(value.text).length > 64 * 1024) throw Error('请填写名称，以及正文或来源链接；正文最多 64 KiB。')
  if (value.url) {
    let url: URL
    try { url = new URL(value.url) } catch { throw Error('请输入完整的 http:// 或 https:// 来源链接。') }
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || value.url.length > 2048) throw Error('来源链接格式不正确，不能包含登录凭据。')
  }
  return value
}
export function makeCollectionDocument(input: CollectionForm): CollectionDocument {
  const value = collectionForm(input)
  const source: Record<string, unknown> = { key: 'reference', title: value.name, media_kind: value.text.trim() ? 'text' : 'other', role: 'original', acquisition_state: value.text.trim() ? 'ready' : 'declared' }
  if (value.text.trim()) source.content = { kind: 'inline', text: value.text }
  if (value.url) source.source_url = value.url
  return { schema_version: 1, document_kind: 'reference-learning', origin: 'manual', sources: [source], coverage: [], results: [], relations: [], uses: [] }
}
export function readCollectionDocument(raw: string, maxBytes = 1 << 20): CollectionDocument {
  if (new TextEncoder().encode(raw).length > maxBytes) invalid()
  const d = object(JSON.parse(raw))
  if (![1, 2].includes(Number(d.schema_version)) || typeof d.schema_version !== 'number' || d.document_kind !== 'reference-learning' || !['manual','imported','generated'].includes(String(d.origin))) invalid()
  for (const key of ['sources','results','coverage','relations','uses']) if (!Array.isArray(d[key])) invalid()
  for (const v of d.sources as unknown[]) { const s = object(v); if (!text(s.key) || !text(s.title) || !text(s.acquisition_state)) invalid() }
  if (d.schema_version === 2) readCuration(d)
  return d as unknown as CollectionDocument
}

// Presentation integrity only; the service authenticates fixed references and runs.
function readCuration(d: Record<string, unknown>): void {
  const c = object(d.curation)
  const array = (v: unknown, max = 10000): unknown[] => { if (!Array.isArray(v) || v.length > max) invalid(); return v as unknown[] }
  const keys = (v: unknown): string[] => {
    const a = array(v)
    if (!a.every(x => text(x)) || new Set(a).size !== a.length) invalid()
    return a as string[]
  }
  const results = new Map<string, Set<string>>()
  for (const item of array(d.results)) {
    const r = object(item)
    if (!text(r.key) || results.has(String(r.key))) invalid()
    results.set(String(r.key), new Set())
  }
  const packages = new Set<string>()
  for (const item of array(c.packages, 1000)) {
    const p = object(item)
    if (!text(p.key) || !text(p.purpose) || packages.has(String(p.key))) invalid()
    packages.add(String(p.key))
    const members = keys(p.result_keys)
    if (!members.length) invalid()
    for (const key of members) { if (!results.has(key)) invalid(); results.get(key)!.add(String(p.key)) }
  }
  const seen = new Set<string>()
  for (const item of array(c.assessments)) {
    const a = object(item), key = String(a.result_key), membership = results.get(key)
    if (!text(a.result_key) || !membership || seen.has(key)) invalid()
    seen.add(key)
    const assigned = keys(a.package_keys)
    if (assigned.length !== membership!.size || assigned.some(k => !membership!.has(k))) invalid()
    if (!['unassessed', 'recommended', 'needs_review', 'excluded'].includes(String(a.selection)) || !text(a.note, 65536)) invalid()
    if (!keys(a.reason_codes).length) invalid()
    const evidence = array(a.evidence_refs)
    if (['recommended', 'excluded'].includes(String(a.selection)) && !evidence.length) invalid()
    for (const ref of [...evidence, ...array(a.merged_from_refs)]) object(ref)
    const previews = new Set<string>()
    for (const item of array(a.previews)) {
      const p = object(item)
      if (!text(p.key) || previews.has(String(p.key)) || !['ready', 'missing'].includes(String(p.state)) || !['image', 'audio', 'video'].includes(String(p.media_kind)) || !['original_reference', 'generated_effect', 'reconstruction_preview'].includes(String(p.role)) || !text(p.note, 65536)) invalid()
      previews.add(String(p.key))
      if (p.role === 'original_reference' && ('run_ref' in p || 'input_refs' in p)) invalid()
      if (p.state === 'ready') {
        if ('reason_code' in p) invalid()
        object(p.file_ref); object(p.locator)
        if (typeof p.sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(p.sha256)) invalid()
        if (p.role !== 'original_reference') { const run = object(p.run_ref); if (!text(run.service_id) || !text(run.run_id)) invalid(); array(p.input_refs).forEach(object) }
      } else if (!text(p.reason_code) || ['file_ref', 'locator', 'sha256', 'run_ref', 'input_refs'].some(key => key in p)) invalid()
    }
  }
  if (seen.size !== results.size) invalid()
  if (d.origin === 'generated') {
    object(d.provenance)
    const snapshot = object(d.processing_snapshot)
    if (!isID(snapshot.task_id) || typeof snapshot.pipeline_version !== 'number' || ![0, 2].includes(snapshot.pipeline_version) || !text(snapshot.assembly_version) || !text(snapshot.adapter_revision) || typeof snapshot.plan_sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(snapshot.plan_sha256)) invalid()
  } else if ('processing_snapshot' in d) invalid()
}
export function editableCollection(d: CollectionDocument): CollectionForm | null {
  // Only the bounded capture shape is edited here. Imported analyses, multi-source
  // documents and file sources need their typed editors; never discard them.
  if (d.schema_version !== 1 || 'provenance' in d || d.origin !== 'manual' || d.sources.length !== 1 || d.results.length || d.coverage.length || d.relations.length || d.uses.length) return null
  const s = d.sources[0], content = s.content ? object(s.content) : null
  if (s.key !== 'reference' || s.role !== 'original' || content && (content.kind !== 'inline' || typeof content.text !== 'string') || s.acquisition_state === 'unavailable') return null
  const allowed = ['key','title','media_kind','role','acquisition_state','content','source_url']
  if (Object.keys(s).some(k => !allowed.includes(k))) return null
  if (Object.keys(d).some(k => !['schema_version','document_kind','origin','sources','results','coverage','relations','uses'].includes(k))) return null
  if (content && Object.keys(content).some(k => !['kind','text'].includes(k))) return null
  if (s.media_kind !== (content ? 'text' : 'other') || s.acquisition_state !== (content ? 'ready' : 'declared')) return null
  return { name: String(s.title), text: content ? String(content.text) : '', url: typeof s.source_url === 'string' ? s.source_url : '' }
}
