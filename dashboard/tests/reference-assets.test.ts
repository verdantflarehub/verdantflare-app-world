import test from 'node:test'
import assert from 'node:assert/strict'
import { readReferenceDomain, isReferenceAsset } from '../src/platform/reference-assets.ts'
import { readAssetList } from '../src/platform/world-model.ts'

const id = (n: number) => `0199b501-0000-7000-8000-${String(n).padStart(12, '0')}`
const source = { collection: 'anhao/ai-pose-gallery-zh', source_ids: ['k_front'], author: 'ギア', rights: 'third-party-internal-only' }

test('pose reference orders views by angle and keeps the default view', () => {
  const d = readReferenceDomain({ schema_version: 1, kind: 'pose-reference', pose_key: 'k', name: { zh: '站姿', ja: '立ち' }, system: { key: 'upper-single', zh: '上半身单人' }, category: { zh: '站姿' }, people: 1, framing: 'upper-body',
    views: [{ angle: 'low', image: 'k/views/low.png' }, { angle: 'front', image: 'k/views/front.png', thumb: 'k/thumbs/front.jpg' }], default_view: 'front', source })
  assert.deepEqual(d.views.map(v => v.key), ['front', 'low'])
  assert.equal(d.default, 'front')
  assert.match(d.summary, /上半身单人 · 站姿 · 单人 · 2 个视角/)
})

test('costume item reports its scope and keeps source rights', () => {
  const d = readReferenceDomain({ schema_version: 1, kind: 'costume-reference', costume_key: 'sheet-01-r1c1', scope: 'item', worn_on_body: true, name: { zh: '薄纱裙' }, category: { key: 'skirt', zh: '裙装' }, items: ['薄纱裙'], tags: { zh: ['裙装', '插画'] }, image: 'sheet-01-r1c1/image.png', source: { ...source, rights: 'original' } })
  assert.equal(d.views[0].image, 'sheet-01-r1c1/image.png')
  assert.equal(d.source.rights, 'original')
  assert.ok(d.overview.some(([k, v]) => k === '资产类型' && v === '服装参考 · 单品'))
})

test('unknown domain kinds are rejected and only reference types route to the detail', () => {
  assert.throws(() => readReferenceDomain({ schema_version: 1, kind: 'character-image', source }))
  assert.ok(isReferenceAsset('pose-reference') && isReferenceAsset('costume-reference') && !isReferenceAsset('character-image'))
})

test('list previews are kept when valid and dropped without failing the list', () => {
  const ref = { store_id: id(4), artifact_id: id(5), version_id: id(6) }
  const base = { head_asset_version_id: id(2), name: 'x', asset_type: 'pose-reference', subjects: [], created_at: '2026-10-31T00:00:00Z' }
  const out = readAssetList({ items: [{ ...base, asset_id: id(1), preview: { file_id: id(3), path: 'k/thumbs/front.jpg', role: 'thumbnail', content_ref: ref } }, { ...base, asset_id: id(7), preview: { path: 'bad' } }], next_cursor: '' })
  assert.equal(out.items[0].preview?.path, 'k/thumbs/front.jpg')
  assert.equal(out.items[1].preview, undefined)
})
