import test from 'node:test'
import assert from 'node:assert/strict'
import { catalogQuery, assetDomain } from '../src/platform/world-catalog.ts'
import { canonicalRoute } from '../src/platform/route-links.ts'
test('domain navigation keeps fixed detail versions and composes independent catalog filters',()=>{
 const asset='01900000-0000-7000-8000-000000000001',version='01900000-0000-7000-8000-000000000002'
 const path=`/knowledge?asset_type=prompt&q=布鼠&relation=curated_in&asset=${asset}&version=${version}`
 assert.equal(decodeURI(canonicalRoute('world',path)),path)
 assert.deepEqual(catalogQuery('/knowledge',{asset_type:'prompt',q:' 布鼠 ',relation:'curated_in',asset,version}),{domain:'knowledge',asset_type:'prompt',q:'布鼠',relation:'curated_in'})
 assert.equal(assetDomain('prompt')?.key,'knowledge')
 assert.equal(assetDomain('voice-model')?.key,'sound')
 assert.equal(assetDomain('future-media'),undefined)
 assert.deepEqual(catalogQuery('/',{}),{})
 assert.throws(()=>canonicalRoute('world','/unknown'))
})
