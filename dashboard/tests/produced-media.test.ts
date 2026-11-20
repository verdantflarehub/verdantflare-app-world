import test from 'node:test'
import assert from 'node:assert/strict'
import { producedMedia } from '../src/platform/produced-media.ts'
import type { AssetOpen } from '../src/platform/world-model.ts'
import type { LearningResult } from '../src/platform/learning-results.ts'
const asset={manifest:{files:[{file_id:'preview',role:'preview'},{file_id:'poster',role:'thumbnail'},{file_id:'source',role:'source'}]}} as AssetOpen
const clip={kind:'file',file_id:'preview'},poster={kind:'file',file_id:'poster'}
const shot={type_key:'shot',type_version:1,payload:{clip_refs:[clip],storyboard_refs:[poster]}} as unknown as LearningResult
test('shot and continuous camera previews resolve explicit files from the selected manifest',()=>{
 assert.deepEqual(producedMedia(shot,asset),{clips:[clip],poster})
 assert.deepEqual(producedMedia({...shot,type_key:'camera_animation',payload:{previs_ref:clip}},asset),{clips:[clip]})
})
test('originals, missing files, external references and unsupported versions are not production previews',()=>{
 for(const ref of [{kind:'file',file_id:'source'},{kind:'file',file_id:'absent'},{kind:'source',key:'original'},{kind:'asset',file_id:'preview',asset_id:'other'},{kind:'file',file_id:'poster'}]){
  assert.equal(producedMedia({...shot,payload:{clip_refs:[ref]}},asset),null)
 }
 assert.equal(producedMedia({...shot,type_version:2},asset),null)
 assert.equal(producedMedia({...shot,type_key:'character'},asset),null)
 assert.equal(producedMedia(shot,{manifest:{files:[]}} as unknown as AssetOpen),null)
})
