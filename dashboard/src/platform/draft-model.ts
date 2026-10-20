import { isID } from './world-model.ts'
export type Purpose = 'collection' | 'learning' | 'creation'
export interface ContentRef { store_id: string; artifact_id: string; version_id: string }
export interface DraftFile { file_id: string; path: string; role: string; content_ref: ContentRef }
export interface DraftManifest {
  schema_version: 1; kind: 'world-draft'; draft_id: string; purpose: Purpose; name: string; subjects: string[]
  files: DraftFile[]; domain_documents: { document_type: string; file_id: string }[]
  source_refs: Record<string, unknown>[]; asset_refs: Record<string, unknown>[]; run_refs: { service_id: string; run_id: string }[]
}
export interface DraftRevision { kind: 'revision'; draft_id: string; revision_id: string; head_revision_id: string; created_at: string; manifest_ref: ContentRef; manifest: DraftManifest }
export interface DraftItem { draft_id: string; head_revision_id: string; name: string; purpose: Purpose; subjects: string[]; has_content: boolean; created_at: string; updated_at: string }
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
    seen.add(item.draft_id as string)
  }
  return data as unknown as { items: DraftItem[]; next_cursor: string | null }
}
export function readDraft(v: unknown, draft?: string, revision?: string, purpose?: Purpose): DraftRevision {
  const data = object(v), m = object(data.manifest)
  if (data.kind !== 'revision' || !isID(data.draft_id) || !isID(data.revision_id) || !isID(data.head_revision_id) || !text(data.created_at) ||
    draft && data.draft_id !== draft || revision && data.revision_id !== revision || purpose && m.purpose !== purpose ||
    m.schema_version !== 1 || m.kind !== 'world-draft' || m.draft_id !== data.draft_id || !purposes.includes(String(m.purpose)) || !text(m.name) || !strings(m.subjects)) invalid()
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
export interface CollectionForm { name: string; text: string; url: string }
export interface CollectionDocument extends Record<string, unknown> { schema_version: 1; document_kind: 'reference-learning'; origin: string; sources: Record<string, unknown>[]; results: unknown[]; coverage: unknown[]; relations: unknown[]; uses: unknown[] }
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
export function readCollectionDocument(raw: string): CollectionDocument {
  if (new TextEncoder().encode(raw).length > 1 << 20) invalid()
  const d = object(JSON.parse(raw))
  if (d.schema_version !== 1 || d.document_kind !== 'reference-learning' || !['manual','imported','generated'].includes(String(d.origin))) invalid()
  for (const key of ['sources','results','coverage','relations','uses']) if (!Array.isArray(d[key])) invalid()
  for (const v of d.sources as unknown[]) { const s = object(v); if (!text(s.key) || !text(s.title) || !text(s.acquisition_state)) invalid() }
  return d as unknown as CollectionDocument
}
export function editableCollection(d: CollectionDocument): CollectionForm | null {
  // Only the bounded capture shape is edited here. Imported analyses, multi-source
  // documents and file sources need their typed editors; never discard them.
  if ('provenance' in d || d.origin !== 'manual' || d.sources.length !== 1 || d.results.length || d.coverage.length || d.relations.length || d.uses.length) return null
  const s = d.sources[0], content = s.content ? object(s.content) : null
  if (s.key !== 'reference' || s.role !== 'original' || content && (content.kind !== 'inline' || typeof content.text !== 'string') || s.acquisition_state === 'unavailable') return null
  const allowed = ['key','title','media_kind','role','acquisition_state','content','source_url']
  if (Object.keys(s).some(k => !allowed.includes(k))) return null
  if (Object.keys(d).some(k => !['schema_version','document_kind','origin','sources','results','coverage','relations','uses'].includes(k))) return null
  if (content && Object.keys(content).some(k => !['kind','text'].includes(k))) return null
  if (s.media_kind !== (content ? 'text' : 'other') || s.acquisition_state !== (content ? 'ready' : 'declared')) return null
  return { name: String(s.title), text: content ? String(content.text) : '', url: typeof s.source_url === 'string' ? s.source_url : '' }
}
