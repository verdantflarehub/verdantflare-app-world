import test from 'node:test'
import assert from 'node:assert/strict'
import { readAsset, readAssetList } from '../src/platform/world-model.ts'

const id = (n: number) => `0192f3d4-0000-7000-8000-${String(n).padStart(12, '0')}`
const version = { asset_id: id(1), asset_version_id: id(2), manifest: { schema_version: 1, kind: 'asset-version', asset_id: id(1), name: 'Example', asset_type: 'character-image', subjects: [], files: [{ file_id: id(3), path: 'portrait.png', role: 'image', content_ref: { store_id: id(6), artifact_id: id(7), version_id: id(8) } }], source: { project_id: id(4), project_revision_id: id(5), relation: 'produced_in' }, depends_on: [] } }

test('asset details preserve the selected version and valid fixed source', () => {
  assert.equal(readAsset(version, id(1), id(2)).asset_version_id, id(2))
  assert.throws(() => readAsset(version, id(1), id(9)), /固定版本/)
  assert.throws(() => readAsset({ ...version, manifest: { ...version.manifest, source: { ...version.manifest.source, project_revision_id: 'latest' } } }, id(1), id(2)), /来源/)
  assert.throws(() => readAsset({ ...version, manifest: { ...version.manifest, files: [...version.manifest.files, ...version.manifest.files] } }, id(1), id(2)), /文件/)
})

test('invalid pagination and duplicated records cannot masquerade as a complete list', () => {
  const item = { asset_id: id(1), head_asset_version_id: id(2), name: 'Example', asset_type: 'character-image', subjects: [], created_at: '2026-10-11T00:00:00Z' }
  assert.deepEqual(readAssetList({ items: [item], next_cursor: '' }).items, [item])
  assert.throws(() => readAssetList({ items: [item, item], next_cursor: '' }))
  assert.throws(() => readAssetList({ items: [item], next_cursor: 'invalid' }))
  assert.throws(() => readAssetList({ items: null, next_cursor: '' }))
})


test('independent draft assets remain usable without inventing a source project', () => {
  const manifest = { ...version.manifest, schema_version: 2, source: { kind: 'world_draft', draft_id: id(4), draft_revision_id: id(5), relation: 'curated_in' }, domain_documents: [{ document_type: 'reference-learning', file_id: id(3) }] }
  const asset = { ...version, manifest }
  const opened = readAsset(asset, id(1), id(2))
  assert.equal(opened.manifest.source.kind, 'world_draft')
  assert.equal('project_id' in opened.manifest.source, false)
  for (const bad of [
    { ...manifest, schema_version: 1 },
    { ...manifest, schema_version: 3 },
    { ...manifest, domain_documents: undefined },
    { ...manifest, source: { ...manifest.source, project_id: id(9) } },
    { ...manifest, domain_documents: [{ document_type: 'reference-learning', file_id: id(9) }] },
    { ...manifest, domain_documents: [...manifest.domain_documents, ...manifest.domain_documents] },
    { ...manifest, source: { ...manifest.source, draft_revision_id: 'latest' } },
  ]) assert.throws(() => readAsset({ ...asset, manifest: bad }, id(1), id(2)))
})

test('pose and costume references browse under the character domain', async () => {
  const { assetDomain, assetTypeLabel } = await import('../src/platform/world-catalog.ts')
  assert.equal(assetDomain('pose-reference')?.key, 'character')
  assert.equal(assetDomain('costume-reference')?.key, 'character')
  assert.equal(assetTypeLabel('pose-reference'), '姿势参考')
  assert.equal(assetTypeLabel('costume-reference'), '服装参考')
})
