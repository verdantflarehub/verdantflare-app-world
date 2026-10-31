export interface AssetFile { file_id: string; path: string; role: string; content_ref: { store_id: string; artifact_id: string; version_id: string } }
export interface AssetItem {
  asset_id: string; head_asset_version_id: string; name: string; asset_type: string; subjects: string[]; created_at: string
  preview?: AssetFile
}
export type AssetSource =
  | { kind?: never; project_id: string; project_revision_id: string; relation: string }
  | { kind: 'world_draft'; draft_id: string; draft_revision_id: string; relation: string }
export interface AssetOpen {
  asset_id: string; asset_version_id: string
  manifest: {
    schema_version: 1 | 2; kind: 'asset-version'; asset_id: string
    name: string; asset_type: string; subjects: string[]
    files: AssetFile[]
    source: AssetSource
    domain_documents?: Array<{ document_type: string; file_id: string }>
    depends_on: Array<{ asset_id: string; asset_version_id: string; purpose: string }>
  }
}
export const isID = (value: unknown): value is string => typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value)
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('资产返回内容不正确。')
  return value as Record<string, unknown>
}
function text(value: unknown): value is string { return typeof value === 'string' }
function assetFile(value: unknown): value is AssetFile {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return false
  const file = value as Record<string, unknown>, ref = file.content_ref as Record<string, unknown> | undefined
  return isID(file.file_id) && text(file.path) && text(file.role) && !!ref && typeof ref === 'object' && isID(ref.store_id) && isID(ref.artifact_id) && isID(ref.version_id)
}
function strings(value: unknown): value is string[] { return Array.isArray(value) && value.length <= 256 && value.every(text) }
export function readAssetList(value: unknown): { items: AssetItem[]; next_cursor: string } {
  const data = object(value)
  if (!Array.isArray(data.items) || data.items.length > 100 || (data.next_cursor !== '' && !isID(data.next_cursor))) throw new Error('资产列表返回内容不正确。')
  const seen = new Set<string>()
  for (const value of data.items) {
    const item = object(value)
    if (!isID(item.asset_id) || !isID(item.head_asset_version_id) || !text(item.name) || !text(item.asset_type) || !strings(item.subjects) || !text(item.created_at) || seen.has(item.asset_id)) throw new Error('资产列表返回内容不正确。')
    seen.add(item.asset_id)
    if ('preview' in item && !assetFile(item.preview)) delete item.preview // a bad preview only loses the picture, never the list
  }
  return data as unknown as { items: AssetItem[]; next_cursor: string }
}
export function readAsset(value: unknown, assetID: string, versionID: string): AssetOpen {
  const data = object(value), manifest = object(data.manifest), source = object(manifest.source)
  if (!isID(assetID) || !isID(versionID) || data.asset_id !== assetID || data.asset_version_id !== versionID || manifest.asset_id !== assetID || manifest.kind !== 'asset-version' || !text(manifest.name) || !text(manifest.asset_type) || !strings(manifest.subjects)) throw new Error('资产返回版本与所选固定版本不一致。')
  const projectSource = manifest.schema_version === 1 && isID(source.project_id) && isID(source.project_revision_id)
    && !('kind' in source) && !('draft_id' in source) && !('draft_revision_id' in source) && !('domain_documents' in manifest)
  const draftSource = manifest.schema_version === 2 && source.kind === 'world_draft' && isID(source.draft_id) && isID(source.draft_revision_id)
    && !('project_id' in source) && !('project_revision_id' in source) && Array.isArray(manifest.domain_documents) && manifest.domain_documents.length <= 256
  if ((!projectSource && !draftSource) || !['produced_in', 'curated_in'].includes(String(source.relation))) throw new Error('资产来源信息不正确。')
  if (!Array.isArray(manifest.files) || !manifest.files.length || manifest.files.length > 10000 || !Array.isArray(manifest.depends_on) || manifest.depends_on.length > 10000) throw new Error('资产文件清单不正确。')
  const seen = new Set<string>()
  for (const value of manifest.files) {
    const file = object(value)
    if (!isID(file.file_id) || !text(file.path) || !text(file.role) || seen.has(file.file_id)) throw new Error('资产文件清单不正确。')
    const ref = object(file.content_ref)
    if (!isID(ref.store_id) || !isID(ref.artifact_id) || !isID(ref.version_id)) throw new Error('资产文件缺少固定内容引用。')
    seen.add(file.file_id)
  }
  if (draftSource) {
    const domains = new Set<string>()
    for (const value of manifest.domain_documents as unknown[]) {
      const document = object(value)
      if (!text(document.document_type) || !/^[a-z][a-z0-9_-]{0,63}$/.test(document.document_type) || !isID(document.file_id) || !seen.has(document.file_id) || domains.has(document.document_type)) throw new Error('资产领域文件不正确。')
      domains.add(document.document_type)
    }
  }
  for (const value of manifest.depends_on) {
    const reference = object(value)
    if (!isID(reference.asset_id) || !isID(reference.asset_version_id) || !text(reference.purpose)) throw new Error('资产依赖版本不正确。')
  }
  return data as unknown as AssetOpen
}
