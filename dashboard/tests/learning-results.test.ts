import test from 'node:test'
import assert from 'node:assert/strict'
import { filterResults, readLearningResults, promptVariants, promptBody, promptConfiguration, loadAssetResults } from '../src/platform/learning-results.ts'
import { readAsset } from '../src/platform/world-model.ts'
const id=(n:number)=>'01900000-0000-7000-8000-'+n.toString(16).padStart(12,'0')
const ref={store_id:id(10),artifact_id:id(11),version_id:id(12)}
const asset=readAsset({asset_id:id(1),asset_version_id:id(2),manifest:{schema_version:2,kind:'asset-version',asset_id:id(1),name:'Saved prompt',asset_type:'prompt',subjects:[],source:{kind:'world_draft',draft_id:id(3),draft_revision_id:id(4),relation:'curated_in'},files:[{file_id:id(5),path:'learning.json',role:'reference',content_ref:ref}],domain_documents:[{document_type:'reference-learning',file_id:id(5)}],depends_on:[]}},id(1),id(2))
function sample(){return {schema_version:1,document_kind:'reference-learning',origin:'manual',sources:[],coverage:[],relations:[],uses:[],results:[{
 key:'prompt-one',title:'Traveling camera',type_key:'prompt',type_version:1,domain:'knowledge',basis:'created',basis_note:'Authored prompt; no generation performed.',source_refs:[],evidence:[],
 use_conditions:[{mode:'direct',availability:'missing',purpose:'video_generation',required_inputs:['reference image'],missing_items:['reference image']}],
 payload:{creative_intent:'Travel along the platform.',variables:[{key:'subject',value_type:'string',required:true,description:'New subject'}],variants:[{
 key:'chat',revision:2,requested_label:'ChatGPT',task_type:'text_generation',input_mode:'messages',compatibility:'unresolved',
 messages:[{role:'system',content:'Describe camera motion.'},{role:'user',content:'<script>do not execute</script>'}],negative_prompt:'no shake',parameters:{temperature:0.4},reference_bindings:[],execution_evidence:[],quality_review:{state:'unreviewed',note:'Not executed.'}
 },{key:'video',revision:1,requested_label:'H3',task_type:'video_generation',input_mode:'image_to_video',prompt_text:'Track the subject.',model_id:'example-model',model_version:'example-version',compatibility:'draft',reference_bindings:[],execution_evidence:[],quality_review:{state:'unreviewed',note:'Not executed.'}}]}
}]}}
test('prompt presentation keeps message roles, negative input, variables, exact model and missing dependencies',()=>{
 const d=readLearningResults(JSON.stringify(sample())),r=d.results[0],variants=promptVariants(r)
 assert.equal(variants.length,2);assert.equal(variants[0].model_id,undefined)
 assert.equal(variants[0].compatibility,'unresolved');assert.equal(r.use_conditions[0].availability,'missing')
 assert.equal(promptBody(variants[0]),'[system]\nDescribe camera motion.\n\n[user]\n<script>do not execute</script>')
 const exported=JSON.parse(promptConfiguration(r,variants[0],asset))
 assert.deepEqual(exported.variant,variants[0]);assert.deepEqual(exported.variables,r.payload.variables)
 assert.deepEqual(exported.source,{asset_id:asset.asset_id,asset_version_id:asset.asset_version_id,result_key:'prompt-one'})
 assert.equal(exported.variant.negative_prompt,'no shake');assert.equal(exported.variant.parameters.temperature,0.4)
 assert.equal(filterResults(d.results,'prompt','example-version').length,1)
 assert.equal(filterResults(d.results,'shot','').length,0)
})
test('malformed or duplicate prompt variants cannot look like usable empty configurations',()=>{
 for(const modify of [
  (d:any)=>d.results.push(structuredClone(d.results[0])),
  (d:any)=>d.results[0].payload.variants.push(structuredClone(d.results[0].payload.variants[0])),
  (d:any)=>delete d.results[0].payload.variants[0].messages,
  (d:any)=>d.results[0].payload.variants[0].messages[0].role='tool',
  (d:any)=>d.results[0].payload.variants[0].compatibility='ready',
  (d:any)=>d.results[0].payload.variants[0].quality_review=null,
 ]){const d=sample();modify(d);assert.throws(()=>readLearningResults(JSON.stringify(d)))}
})
test('unknown future result payloads remain intact without being interpreted as supported prompts',()=>{
 const d:any=sample();d.results[0].type_version=2;d.results[0].payload={future_field:{value:'must remain visible'}}
 assert.deepEqual(readLearningResults(JSON.stringify(d)).results[0].payload,d.results[0].payload)
})
const downloadPath='/studio/dashboards/00000000-0000-4000-8000-000000000001/content/00000000-0000-4000-8000-000000000002'
const downloadVersion={...ref,mime:'application/json',size:100,sha256:'a'.repeat(64)}
const readFixture=async()=>new Blob([JSON.stringify(sample())])
test('asset body reads the selected content under asset access, never its original draft or project',async()=>{
 let calls=0
 const result=await loadAssetResults(async(name,args)=>{
  calls++;assert.equal(name,'artifact.read');assert.deepEqual(args,{mode:'download',content_ref:ref,access:{asset_id:id(1),asset_version_id:id(2)}})
  return {mode:'download',version:downloadVersion,content_path:downloadPath}
 },asset,new AbortController().signal,readFixture)
 assert.equal(result?.results.length,1);assert.equal(calls,1)
 for(const reply of [{mode:'download',version:{...downloadVersion,version_id:id(99)},content_path:downloadPath},{mode:'metadata',version:downloadVersion,content_path:downloadPath}]){
  await assert.rejects(loadAssetResults(async()=>reply,asset),/固定资产/)
 }
 await assert.rejects(loadAssetResults(async()=>{throw Error('permission denied')},asset),/permission denied/)
 const legacy=structuredClone(asset);legacy.manifest.domain_documents=[]
 assert.equal(await loadAssetResults(async()=>{throw Error('must not probe arbitrary files')},legacy),null)
})
