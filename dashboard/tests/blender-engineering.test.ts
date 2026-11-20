import test from 'node:test'
import assert from 'node:assert/strict'
import { sampleDownload, sampleKind } from '../src/platform/prompt-samples.ts'
import { producedMedia } from '../src/platform/produced-media.ts'
import type { AssetOpen } from '../src/platform/world-model.ts'
import type { LearningResult } from '../src/platform/learning-results.ts'

// Synthetic metadata exercises download routing; it is not a generated .blend.
const id=(n:number)=>'01900000-0000-7000-8000-'+n.toString(16).padStart(12,'0')
const content={store_id:id(10),artifact_id:id(11),version_id:id(12)}
const native={file_id:id(5),path:'camera-motion/003/003.blend',role:'source',content_ref:content}
const preview={file_id:id(6),path:'camera-motion/003/previs.mp4',role:'preview',content_ref:{...content,version_id:id(13)}}
const asset={asset_id:id(1),asset_version_id:id(2),manifest:{files:[native,preview]}} as AssetOpen
const path='/studio/dashboards/00000000-0000-4000-8000-000000000001/content/00000000-0000-4000-8000-000000000002'

test('native engineering formats download through the fixed asset without entering media preview',async()=>{
 for(const mime of ['application/x-blender','application/octet-stream']){
  const result=await sampleDownload(async(name,args)=>{
   assert.equal(name,'artifact.read')
   assert.deepEqual(args,{mode:'download',content_ref:content,access:{asset_id:asset.asset_id,asset_version_id:asset.asset_version_id}})
   return {mode:'download',version:{...content,mime,size:1048576,sha256:'a'.repeat(64)},content_path:path}
  },asset,{kind:'file',file_id:native.file_id})
  assert.equal(result.path,path)
  assert.equal(result.mime,mime)
  assert.equal(sampleKind(result.mime),'unsupported') // Download only; never execute or embed a .blend.
 }
 const animation={type_key:'camera_animation',type_version:1,payload:{engineering_ref:{kind:'file',file_id:native.file_id},previs_ref:{kind:'file',file_id:preview.file_id}}} as unknown as LearningResult
 assert.deepEqual(producedMedia(animation,asset)?.clips,[{kind:'file',file_id:preview.file_id}])
 assert.equal(producedMedia({...animation,payload:{previs_ref:{kind:'file',file_id:native.file_id}}},asset),null)
})

test('stale native versions and files absent from this fixed asset are rejected',async()=>{
 await assert.rejects(sampleDownload(async()=>({mode:'download',version:{...content,version_id:id(99),mime:'application/x-blender',size:1,sha256:'a'.repeat(64)},content_path:path}),asset,{kind:'file',file_id:native.file_id}),/不一致/)
 await assert.rejects(sampleDownload(async()=>assert.fail('No read before manifest validation'),asset,{kind:'file',file_id:id(99)}),/清单/)
})
