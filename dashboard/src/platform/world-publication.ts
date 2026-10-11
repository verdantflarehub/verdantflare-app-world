import type { AppSDK } from './types.ts'
import { createPublicationAPI, PublicationError, publicationPrincipal } from './publication-client.ts'

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/
const hash = /^[a-f0-9]{64}$/
const fail = (): never => { throw new PublicationError('INVALID_RESPONSE') }
const object = (v: unknown): Record<string, unknown> => v && typeof v === 'object' && !Array.isArray(v) ? v as Record<string, unknown> : fail()
const id = (v: unknown): string => typeof v === 'string' && uuid.test(v) ? v : fail()
const text = (v: unknown): string => typeof v === 'string' && v.trim() ? v : fail()
const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v)) as T
export const canonical = (v: unknown): string => JSON.stringify(v, (_key, value) => value && typeof value === 'object' && !Array.isArray(value) ? Object.fromEntries(Object.entries(value).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0)) : value)
export interface PublicationPointer { project_id: string; prepared_revision_id: string; candidate_key: string; commit_id: string }
interface AssetRef { asset_id: string; asset_version_id: string; purpose: string }
interface Registration { commit_id: string; source_project_id: string; source_revision_id: string; file_ids: string[]; name: string; asset_type: string; subjects: string[]; relation: 'produced_in' | 'curated_in'; depends_on: AssetRef[]; asset_id?: string; expected_asset_version_id?: string; review_file_id?: string }
export interface PublicationPreview extends PublicationPointer { request: Registration; request_sha256: string; versions: { version_key: string; label: string; file_ids: string[] }[] }
export interface PublicationResult { asset_id: string; asset_version_id: string; manifest_ref: { store_id: string; artifact_id: string; version_id: string }; manifest: Record<string, unknown> }
interface PublicationStatus { state: 'preparing' | 'committed' | 'conflict' | 'failed'; request_sha256: string; result?: PublicationResult }
export interface PublicationRecord { schemaVersion: 1; preview: PublicationPreview; result?: PublicationResult; state: 'unknown' | PublicationStatus['state'] }
export interface PublicationStorage { getItem(key: string): string | null; setItem(key: string, value: string): void; removeItem(key: string): void }
export function publicationPointer(v: unknown): PublicationPointer {
  const p = object(v), key = text(p.candidate_key)
  if (!/^[a-zA-Z0-9_-]+$/.test(key) || key.length > 256) fail()
  return { project_id: id(p.project_id), prepared_revision_id: id(p.prepared_revision_id), candidate_key: key, commit_id: id(p.commit_id) }
}
const ids = (v: unknown): string[] => {
  if (!Array.isArray(v) || !v.length || v.length > 10000) return fail()
  const result = v.map(id); if (new Set(result).size !== result.length) fail(); return result
}
function previewValue(value: unknown, pointer: PublicationPointer): PublicationPreview {
  const p = object(value), request = object(p.request)
  if (canonical(publicationPointer(p)) !== canonical(pointer) || !hash.test(text(p.request_sha256)) || request.commit_id !== pointer.commit_id || request.source_project_id !== pointer.project_id) fail()
  if (Object.keys(request).some(k => !['commit_id', 'source_project_id', 'source_revision_id', 'file_ids', 'name', 'asset_type', 'subjects', 'relation', 'depends_on', 'asset_id', 'expected_asset_version_id', 'review_file_id'].includes(k))) fail()
  const fileIDs = ids(request.file_ids)
  id(request.source_revision_id); text(request.name); text(request.asset_type)
  if (!['produced_in', 'curated_in'].includes(String(request.relation)) || !Array.isArray(request.subjects) || !request.subjects.length || request.subjects.some(s => typeof s !== 'string' || !s.trim()) || !Array.isArray(request.depends_on)) fail()
  for (const ref of request.depends_on as unknown[]) { const a = object(ref); id(a.asset_id); id(a.asset_version_id); text(a.purpose) }
  for (const optional of ['asset_id', 'expected_asset_version_id', 'review_file_id']) if (request[optional] !== undefined) id(request[optional])
  if (!!request.asset_id !== !!request.expected_asset_version_id) fail()
  if (!Array.isArray(p.versions) || !p.versions.length || p.versions.length > 1000) fail()
  const versions = (p.versions as unknown[]).map(raw => { const v = object(raw); const selected = ids(v.file_ids); if (selected.some(f => !fileIDs.includes(f))) fail(); return { version_key: text(v.version_key), label: text(v.label), file_ids: selected } })
  if (new Set(versions.map(v => v.version_key)).size !== versions.length) fail()
  return clone({ ...pointer, request: request as unknown as Registration, request_sha256: p.request_sha256 as string, versions })
}
function resultValue(value: unknown, request: Registration): PublicationResult {
  const result = object(value), manifest = object(result.manifest), source = object(manifest.source), content = object(result.manifest_ref)
  id(content.store_id); id(content.artifact_id); id(content.version_id)
  const asset = id(result.asset_id), version = id(result.asset_version_id)
  if (request.asset_id && request.asset_id !== asset || manifest.asset_id !== asset || manifest.kind !== 'asset-version' || manifest.schema_version !== 1 || source.project_id !== request.source_project_id || source.project_revision_id !== request.source_revision_id || source.relation !== request.relation || (source.review_file_id || undefined) !== request.review_file_id || manifest.name !== request.name || manifest.asset_type !== request.asset_type || canonical(manifest.subjects) !== canonical(request.subjects) || canonical(manifest.depends_on) !== canonical(request.depends_on)) fail()
  if (!Array.isArray(manifest.files) || manifest.files.length !== request.file_ids.length) fail()
  const actual = (manifest.files as unknown[]).map(raw => { const f = object(raw); const ref = object(f.content_ref); text(f.path); text(f.role); id(ref.store_id); id(ref.artifact_id); id(ref.version_id); return id(f.file_id) })
  if (canonical([...actual].sort()) !== canonical([...request.file_ids].sort())) fail()
  return clone({ asset_id: asset, asset_version_id: version, manifest_ref: content as unknown as PublicationResult['manifest_ref'], manifest })
}
export function createWorldPublication(sdk: AppSDK, storage: PublicationStorage, input: PublicationPointer) {
  const pointer = publicationPointer(input), api = createPublicationAPI(sdk, 'world')
  const key = 'world.publication.v1:' + JSON.stringify([...publicationPrincipal(sdk.context.get()), pointer.project_id, pointer.prepared_revision_id, pointer.candidate_key, pointer.commit_id])
  let record: PublicationRecord | undefined, preview: PublicationPreview | undefined, working = false
  function read(): PublicationRecord | undefined {
    let raw: string | null
    try { raw = storage.getItem(key) } catch { throw new PublicationError('STORAGE_UNAVAILABLE') }
    if (!raw) return undefined
    if (raw.length > 2 * 1024 * 1024) fail()
    try {
      const r = object(JSON.parse(raw)), p = previewValue(r.preview, pointer)
      if (r.schemaVersion !== 1 || !['unknown', 'preparing', 'committed', 'conflict', 'failed'].includes(String(r.state))) fail()
      const result = r.result === undefined ? undefined : resultValue(r.result, p.request)
      if (r.state === 'committed' && !result) fail()
      return { schemaVersion: 1, preview: p, state: r.state as PublicationRecord['state'], ...(result ? { result } : {}) }
    } catch { return fail() }
  }
  function save(next: PublicationRecord) {
    api.live()
    try { const raw = JSON.stringify(next); storage.setItem(key, raw); if (storage.getItem(key) !== raw) throw Error() } catch { throw new PublicationError('STORAGE_UNAVAILABLE') }
    record = clone(next)
  }
  function exact() {
    api.live(); const saved = read()
    if (!record || !saved || canonical(record) !== canonical(saved)) throw new PublicationError('REQUEST_MISMATCH')
    return record
  }
  try { record = read() } catch (error) { api.dispose(); throw error }
  async function load(): Promise<PublicationPreview> {
    const next = previewValue(await api.call('publication_source', pointer), pointer)
    if (record && (next.request_sha256 !== record.preview.request_sha256 || canonical(next.request) !== canonical(record.preview.request))) throw new PublicationError('REQUEST_MISMATCH')
    preview = next; return clone(next)
  }
  async function status(): Promise<PublicationRecord> {
    const original = exact(), asset = original.result?.asset_id || original.preview.request.asset_id
    if (!asset) throw new PublicationError('NOT_READY')
    const value = object(await api.call('commit_status', { asset_id: asset, commit_id: pointer.commit_id }))
    if (value.request_sha256 !== original.preview.request_sha256) throw new PublicationError('REQUEST_MISMATCH')
    if (!['preparing', 'committed', 'conflict', 'failed'].includes(String(value.state))) fail()
    const result = value.state === 'committed' ? resultValue(value.result, original.preview.request) : original.result
    if (result && original.result && canonical(result) !== canonical(original.result)) throw new PublicationError('REQUEST_MISMATCH')
    save({ ...original, state: value.state as PublicationRecord['state'], ...(result ? { result } : {}) })
    return clone(record!)
  }
  async function submit(): Promise<PublicationRecord> {
    if (working) throw new PublicationError('NOT_READY')
    working = true
    try {
      if (record?.result || record?.preview.request.asset_id) {
        try { return await status() }
        catch (error) {
          // A known register result is never submitted again. For an existing
          // asset, only a confirmed missing original operation permits replay.
          if (record?.result || !(error instanceof PublicationError) || error.code !== 'NOT_FOUND') throw error
        }
      } else {
        await load()
        if (!record) save({ schemaVersion: 1, preview: preview!, state: 'unknown' })
      }
      const original = exact()
      if (original.state === 'committed') return await status()
      if (original.state === 'conflict' || original.state === 'failed') throw new PublicationError('TERMINAL_FAILURE')
      const result = resultValue(await api.call('register', original.preview.request), original.preview.request)
      save({ ...original, result, state: 'unknown' })
      return await status()
    } finally { working = false }
  }
  function returnPath(): string {
    const original = exact()
    if (original.state !== 'committed' || !original.result) throw new PublicationError('NOT_READY')
    return '/publication?' + new URLSearchParams({ project: pointer.project_id, prepared: pointer.prepared_revision_id, candidate: pointer.candidate_key, commit: pointer.commit_id, asset: original.result.asset_id, version: original.result.asset_version_id })
  }
  return { load, submit, status, returnPath, dispose: api.dispose, pending: () => record ? clone(record) : undefined }
}
