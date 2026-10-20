import test from 'node:test'
import assert from 'node:assert/strict'
import { promptProjectTarget } from '../src/platform/prompt-project-target.ts'
import type { AssetOpen } from '../src/platform/world-model.ts'
import type { LearningResult } from '../src/platform/learning-results.ts'
const id=(n:number)=>'01900000-0000-7000-8000-'+n.toString(16).padStart(12,'0')
const asset={asset_id:id(1),asset_version_id:id(2),manifest:{files:[{file_id:id(6),content_ref:{store_id:id(3),artifact_id:id(4),version_id:id(5)}}],domain_documents:[{document_type:'reference-learning',file_id:id(6)}]}} as AssetOpen
const variant={key:'image',revision:2,requested_label:'Private model label',task_type:'image_generation',input_mode:'text',prompt_text:'Private prompt; never put into URL',compatibility:'draft',reference_bindings:[],execution_evidence:[],quality_review:{state:'unreviewed',note:'Fixture only'}}
const result={key:'prompt',type_key:'prompt',type_version:1,payload:{creative_intent:'Private intent',variables:[],variants:[variant,{...variant,key:'other',revision:8}]}} as unknown as LearningResult
test('handoff contains only the selected fixed content and variant locator',()=>{
 const target=promptProjectTarget(asset,result,'image'),url=new URL(target.path,'https://host.invalid')
 assert.equal(target.appId,'project');assert.equal(url.pathname,'/create/from-prompt')
 assert.deepEqual(Object.fromEntries(url.searchParams),{asset:id(1),version:id(2),store:id(3),artifact:id(4),content_version:id(5),result:'prompt',variant:'image',variant_revision:'2'})
 assert.ok(!target.path.includes('Private'));assert.ok(!target.path.includes('other'))
 assert.equal(new URL(promptProjectTarget(asset,result,'other').path,'https://host.invalid').searchParams.get('variant_revision'),'8')
})
test('missing variant, domain, files and invalid identity cannot fall back to another prompt',()=>{
 assert.throws(()=>promptProjectTarget(asset,result,'missing'))
 for(const change of [(a:AssetOpen)=>a.manifest.files=[],(a:AssetOpen)=>a.manifest.domain_documents=[],(a:AssetOpen)=>a.asset_version_id='latest']){const copy=structuredClone(asset);change(copy);assert.throws(()=>promptProjectTarget(copy,result,'image'))}
})
