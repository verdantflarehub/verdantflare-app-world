export interface AssetItem {
  asset_id: string; head_asset_version_id: string; name: string; asset_type: string; subjects: string[]; created_at: string
}
export interface AssetOpen {
  asset_id: string; asset_version_id: string
  manifest: {
    name: string; asset_type: string; subjects: string[]
    files: Array<{ file_id: string; path: string; role: string }>
    source: { project_id: string; project_revision_id: string; relation: string }
    depends_on: Array<{ asset_id: string; asset_version_id: string; purpose: string }>
  }
}
export const isID = (value: unknown): value is string => typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(value)
function object(value: unknown): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('资产返回内容不正确。')
  return value as Record<string, unknown>
}
function text(value: unknown): value is string { return typeof value === 'string' }
function strings(value: unknown): value is string[] { return Array.isArray(value) && value.length <= 256 && value.every(text) }
export function readAssetList(value: unknown): { items: AssetItem[]; next_cursor: string } {
  const data = object(value)
  if (!Array.isArray(data.items) || data.items.length > 100 || (data.next_cursor !== '' && !isID(data.next_cursor))) throw new Error('资产列表返回内容不正确。')
  const seen = new Set<string>()
  for (const value of data.items) {
    const item = object(value)
    if (!isID(item.asset_id) || !isID(item.head_asset_version_id) || !text(item.name) || !text(item.asset_type) || !strings(item.subjects) || !text(item.created_at) || seen.has(item.asset_id)) throw new Error('资产列表返回内容不正确。')
    seen.add(item.asset_id)
  }
  return data as unknown as { items: AssetItem[]; next_cursor: string }
}
export function readAsset(value: unknown, assetID: string, versionID: string): AssetOpen {
  const data = object(value), manifest = object(data.manifest), source = object(manifest.source)
  if (!isID(assetID) || !isID(versionID) || data.asset_id !== assetID || data.asset_version_id !== versionID || !text(manifest.name) || !text(manifest.asset_type) || !strings(manifest.subjects)) throw new Error('资产返回版本与所选固定版本不一致。')
  if (!isID(source.project_id) || !isID(source.project_revision_id) || !['produced_in', 'curated_in'].includes(String(source.relation))) throw new Error('资产来源信息不正确。')
  if (!Array.isArray(manifest.files) || !manifest.files.length || manifest.files.length > 10000 || !Array.isArray(manifest.depends_on) || manifest.depends_on.length > 10000) throw new Error('资产文件清单不正确。')
  const seen = new Set<string>()
  for (const value of manifest.files) {
    const file = object(value)
    if (!isID(file.file_id) || !text(file.path) || !text(file.role) || seen.has(file.file_id)) throw new Error('资产文件清单不正确。')
    seen.add(file.file_id)
  }
  for (const value of manifest.depends_on) {
    const reference = object(value)
    if (!isID(reference.asset_id) || !isID(reference.asset_version_id) || !text(reference.purpose)) throw new Error('资产依赖版本不正确。')
  }
  return data as unknown as AssetOpen
}
