import test from 'node:test'
import assert from 'node:assert/strict'
import { readFilters, gallerySubjects, listArgs, facetRequests, readFacets, chipValues, masonry, subjectValue } from '../src/platform/reference-gallery.ts'
import { canonicalRoute } from '../src/platform/route-links.ts'

const id = (n: number) => `0199b501-0000-7000-8000-${String(n).padStart(12, '0')}`

test('pose filters become exact dimension subjects and a view preview hint', () => {
  const f = readFilters('pose', { sys: '全身单人・标准系', cat: '站姿', people: '单人', v: 'diag', q: '回眸' })
  assert.deepEqual(gallerySubjects('pose', f), ['体系:全身单人・标准系', '动作:站姿', '人数:单人'])
  assert.deepEqual(listArgs('pose', f, id(1)), { domain: 'pose', limit: 60, subjects: ['体系:全身单人・标准系', '动作:站姿', '人数:单人'], q: '回眸', cursor: id(1), preview_hint: 'diag' })
  assert.equal(readFilters('pose', { v: 'bogus', people: '三人' }).view, 'front')
  assert.equal(readFilters('pose', { people: '三人' }).people, '')
})

test('costume tabs scope subjects and facets to outfits or items', () => {
  const outfit = readFilters('costume', { cat: '春季穿搭' }), item = readFilters('costume', { tab: 'item', kind: '裙装', style: '插画' })
  assert.deepEqual(gallerySubjects('costume', outfit), ['范围:穿搭', '分类:春季穿搭'])
  assert.deepEqual(gallerySubjects('costume', item), ['范围:单品', '类型:裙装', '画风:插画'])
  assert.deepEqual(facetRequests('costume', item)[1], { domain: 'costume', limit: 1, subjects: ['范围:单品'], facets: ['类型', '画风'] })
  assert.deepEqual(facetRequests('pose', readFilters('pose', { sys: '上半身单人' }))[1], { domain: 'pose', limit: 1, facets: ['动作'], subjects: ['体系:上半身单人'] })
})

test('facets are read defensively and chips sort by count then name', () => {
  const r = readFacets({ total: 73, facets: { 动作: { 站姿: 18, 坐姿: 12, bad: 'x', 跪立: 12 } } })
  assert.equal(r.total, 73)
  assert.deepEqual(chipValues(r.facets['动作']), ['站姿', '跪立', '坐姿'].sort((a, b) => (b === '站姿' ? 1 : 0) - (a === '站姿' ? 1 : 0) || a.localeCompare(b, 'zh')))
  assert.deepEqual(readFacets(null), { total: 0, facets: {} })
})

test('masonry keeps reading order while filling the shortest column', () => {
  const cols = masonry([1.5, 1, 1, 1, 1], 2, x => x)
  assert.deepEqual(cols, [[1.5, 1], [1, 1, 1]])
})

test('subject values strip their dimension prefix', () => {
  const item = { asset_id: id(1), head_asset_version_id: id(2), name: 'x', asset_type: 'pose-reference', subjects: ['站姿A', '体系:上半身单人', '动作:站姿'], created_at: '' }
  assert.equal(subjectValue(item, '动作'), '站姿')
  assert.equal(subjectValue(item, '人数'), '')
})

test('gallery and detail routes are valid World paths', () => {
  assert.equal(canonicalRoute('world', '/pose?sys=a'), '/pose?sys=a')
  assert.equal(canonicalRoute('world', '/costume?tab=item'), '/costume?tab=item')
  assert.equal(canonicalRoute('world', `/pose/asset/${id(1)}?version=${id(2)}&v=diag`), `/pose/asset/${id(1)}?version=${id(2)}&v=diag`)
  assert.throws(() => canonicalRoute('world', `/pose/asset/${id(1)}`))
  assert.throws(() => canonicalRoute('world', '/pose/asset/not-an-id?version=' + id(2)))
})
