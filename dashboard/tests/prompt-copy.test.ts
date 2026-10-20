import test from 'node:test'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { preparePromptCopy,promptEdit,revisePromptCopy } from '../src/platform/prompt-copy.ts'
import { createPromptSave } from '../src/platform/prompt-save.ts'
import { readLearningResults,promptVariants } from '../src/platform/learning-results.ts'
import { readAsset } from '../src/platform/world-model.ts'
import type { AppSDK,Context,Json,Lifecycle } from '../src/platform/types.ts'
const id=(n:number)=>'01900000-0000-7000-8000-'+n.toString(16).padStart(12,'0')
const copy=<T>(v:T):T=>JSON.parse(JSON.stringify(v))
test('remote service fixture is produced by the current Dashboard prompt derivation',()=>{
 const f=JSON.parse(readFileSync(new URL('../../internal/artifactclient/testdata/prompt-copy.generated.json',import.meta.url),'utf8'))
 const source=readAsset(f.source_asset,f.source_asset.asset_id,f.source_asset.asset_version_id)
 const result=readLearningResults(JSON.stringify(f.source_document)).results[0]
 assert.deepEqual(preparePromptCopy(source,result,f.variant_key,f.edit),f.plan)
})
const ref={store_id:id(10),artifact_id:id(11),version_id:id(12)}
const asset=readAsset({asset_id:id(1),asset_version_id:id(2),manifest:{schema_version:2,kind:'asset-version',asset_id:id(1),name:'Original',asset_type:'prompt',subjects:[],source:{kind:'world_draft',draft_id:id(3),draft_revision_id:id(4),relation:'curated_in'},files:[{file_id:id(5),path:'learning.json',role:'reference',content_ref:ref},{file_id:id(6),path:'image.png',role:'reference',content_ref:ref}],domain_documents:[{document_type:'reference-learning',file_id:id(5)}],depends_on:[]}},id(1),id(2))
const result=readLearningResults(JSON.stringify({schema_version:1,document_kind:'reference-learning',origin:'manual',sources:[],coverage:[],relations:[],uses:[],results:[{key:'old_prompt',title:'Old prompt',type_key:'prompt',type_version:1,domain:'knowledge',basis:'created',basis_note:'Original',source_refs:[],evidence:[],use_conditions:[{mode:'direct',availability:'ready',purpose:'generation',required_inputs:['image'],missing_items:[]}],payload:{creative_intent:'Original intent',variables:[{key:'image',value_type:'ref',required:true,description:'Image',default:{kind:'file',file_id:id(6)}},{key:'ordinary',value_type:'string',required:false,description:'Literal',default:'keep'}],variants:[{key:'first',revision:1,requested_label:'Example model',model_id:'example',model_version:'v1',task_type:'video_generation',input_mode:'image_to_video',prompt_text:'Original body',parameters:{opaque:{kind:'source',key:'do-not-remap'}},reference_bindings:[{key:'reference',role:'image',input_ref:{kind:'source',key:'source-one'}}],compatibility:'schema_checked',capability_ref:{kind:'file',file_id:id(6)},capability_sha256:'a'.repeat(64),execution_evidence:[],quality_review:{state:'accepted',note:'Previous version only'}}]}}]})).results[0]
function plan(){return preparePromptCopy(asset,result,'first',{...promptEdit(result,promptVariants(result)[0]),name:'New prompt',prompt_text:'New body'})}
test('derivation preserves originals and fixed references but never inherits verification',()=>{
 const before=JSON.stringify({asset,result}),p=plan(),r=p.document.results[0],v=promptVariants(r)[0]
 assert.equal(JSON.stringify({asset,result}),before);assert.equal(v.prompt_text,'New body');assert.equal(v.revision,2)
 assert.equal(v.compatibility,'draft');assert.equal(v.capability_ref,undefined);assert.equal(v.capability_sha256,undefined);assert.deepEqual(v.execution_evidence,[]);assert.equal(v.quality_review.state,'unreviewed')
 assert.deepEqual(v.parameters,{opaque:{kind:'source',key:'do-not-remap'}})
 const variables=r.payload.variables as any[];assert.equal(variables[0].default.kind,'asset');assert.equal(variables[0].default.file_id,id(6))
 const binding=v.reference_bindings[0] as any;assert.equal(binding.input_ref.object_key,'source-one');assert.equal(binding.input_ref.file_id,id(5))
 assert.equal(p.source_refs.length,3);assert.deepEqual(p.asset_refs,[{asset_id:id(1),asset_version_id:id(2),purpose:'prompt_reference'}]);assert.equal(r.use_conditions[0].availability,'unknown')
 const full=copy(result);full.use_conditions=Array.from({length:16},(_,i)=>({...full.use_conditions[0],purpose:'purpose-'+i}))
 assert.equal(preparePromptCopy(asset,full,'first',promptEdit(full,promptVariants(full)[0])).document.results[0].use_conditions.length,16)
})
function fixture(){
 const lifecycle=new Set<(e:Lifecycle)=>void>(),contexts=new Set<(c:Context)=>void>(),storageMap=new Map<string,string>()
 const context:Context={userId:'u',organizationId:'o',stationId:'s',appId:'world',installationId:'i',contextRevision:'r',projectId:null}
 let lost='',denyRegistration=false,denyDraft=false,denyCommit=false,loseStatus=false,latest:any
 const creates=new Map<string,any>(),writes=new Map<string,any>(),commits=new Map<string,any>(),registrations=new Map<string,any>(),calls:string[]=[]
 const revision=(manifest:any,n:number)=>({kind:'revision',draft_id:manifest.draft_id,revision_id:id(n),head_revision_id:id(n),manifest_ref:ref,created_at:'2026-10-19T00:00:00Z',manifest})
 const sdk:AppSDK={version:'1.1.0',context:{get:()=>context,subscribe:f=>{contexts.add(f);return()=>contexts.delete(f)}},theme:{get:()=>'dark',subscribe:()=>()=>{}},lifecycle:{subscribe:f=>{lifecycle.add(f);return()=>lifecycle.delete(f)}},navigation:{get:()=>({path:'/',revision:1}),subscribe:()=>()=>{},request:async()=>{}},
 capabilities:{call:async(name,input)=>{assert.equal(name,'artifact.write');const a=input as any;calls.push('write');assert.equal(a.source.project_id,undefined);let v=writes.get(a.write_id);if(!v){v={version:{...ref,source:a.source,size:Buffer.byteLength(a.text),sha256:createHash('sha256').update(a.text).digest('hex'),mime:a.mime}};writes.set(a.write_id,v)}if(lost==='write'){lost='';throw Error('lost')}return copy(v)}},
 app:{request:async input=>{const a=input.body as any,stage=input.path.split('/').at(-1)==='draft'?a.mode:input.path.endsWith('/commit_status')?'status':input.path.split('/').at(-1);assert.notEqual(input.path,'/api/v1/world/status');calls.push(stage);if(stage==='status'&&loseStatus){loseStatus=false;throw Error('lost status')};assert.equal(a.project_id,undefined);assert.equal(a.source_project_id,undefined);let data:any
 if(stage==='create'){data=creates.get(a.commit_id);if(!data){data=revision({schema_version:1,kind:'world-draft',draft_id:id(20),purpose:'creation',name:a.name,subjects:a.subjects,files:[],domain_documents:[],source_refs:[],asset_refs:[],run_refs:[]},21);creates.set(a.commit_id,data);latest=data}}
 else if(stage==='commit'){if(denyCommit)return{status:409,data:{code:'REVISION_CONFLICT'},requestId:'x'};assert.equal(a.expected_revision_id,id(21));data=commits.get(a.commit_id);if(!data){data=revision(a.manifest,22);commits.set(a.commit_id,data);latest=data}}
 else if(stage==='get'&&input.path.endsWith('/draft')){if(denyDraft)throw Error('draft denied');data=latest}
 else if(stage==='register'){assert.equal(a.asset_id,undefined);if(denyRegistration)return{status:403,data:{code:'PERMISSION_DENIED'},requestId:'x'};data=registrations.get(a.commit_id);if(!data){data={asset_id:id(30),asset_version_id:id(31),manifest_ref:ref,manifest:{schema_version:2,kind:'asset-version',asset_id:id(30),name:a.name,asset_type:'prompt',subjects:a.subjects,files:latest.manifest.files,domain_documents:latest.manifest.domain_documents,source:{kind:'world_draft',draft_id:a.source_draft_id,draft_revision_id:a.source_revision_id,relation:a.relation},depends_on:a.depends_on}};registrations.set(a.commit_id,data)}}
 else if(stage==='get'){assert.equal(a.asset_id,id(30));data=[...registrations.values()][0]}
 else if(stage==='status')data=denyCommit?{kind:'status',state:'conflict',error_code:'REVISION_CONFLICT'}:denyRegistration?{state:'failed',error_code:'PERMISSION_DENIED'}:{state:'committed',result:[...registrations.values()][0]}
 else throw Error('unexpected '+stage)
 const lossName=stage==='get'?(input.path.endsWith('/draft')?'draft_get':'asset_get'):stage
 if(lost===lossName){lost='';throw Error('lost')}
 return{status:200,data:copy(data) as Json,requestId:'x'}
 }}}
 const storage={getItem:(k:string)=>storageMap.get(k)||null,setItem:(k:string,v:string)=>{storageMap.set(k,v)},removeItem:(k:string)=>{storageMap.delete(k)}}
 return{sdk,storage,creates,writes,commits,registrations,calls,lose:(s:string)=>{lost=s},deny:(v:boolean)=>{denyRegistration=v},denyDraft:()=>{denyDraft=true},denyCommit:(v:boolean)=>{denyCommit=v},loseStatus:()=>{loseStatus=true}}
}
for(const stage of ['create','write','commit','draft_get','register','asset_get'])test('prompt save recovers lost '+stage+' without replacing the original asset or duplicating work',async()=>{
 const f=fixture();f.lose(stage);let saver=createPromptSave(f.sdk,f.storage)
 await assert.rejects(saver.start(plan()));assert.ok(saver.read());saver.dispose();saver=createPromptSave(f.sdk,f.storage)
 const out=await saver.resume();assert.equal(out.asset_id,id(30));assert.notEqual(out.asset_id,asset.asset_id)
 assert.equal(f.creates.size,1);assert.equal(f.writes.size,1);assert.equal(f.commits.size,1);assert.equal(f.registrations.size,1);assert.equal(saver.read(),null);saver.dispose()
})
test('failed registration retry keeps the saved draft and content; unknown outcome cannot get a new ID',async()=>{
 const f=fixture(),saver=createPromptSave(f.sdk,f.storage);f.deny(true)
 await assert.rejects(saver.start(plan()));assert.equal(saver.read()?.failure_stage,'register');const old=saver.read()!.register_id
 f.deny(false);await saver.retryRegistration();assert.equal(f.creates.size,1);assert.equal(f.writes.size,1);assert.equal(f.commits.size,1);assert.notEqual([...f.registrations.keys()][0],old);saver.dispose()
 const u=fixture(),unknown=createPromptSave(u.sdk,u.storage);u.lose('register');await assert.rejects(unknown.start(plan()));await assert.rejects(unknown.retryRegistration());assert.ok(unknown.read());unknown.dispose()
})
test('failure to store recovery data starts no network operation',()=>{
 const f=fixture(),saver=createPromptSave(f.sdk,{...f.storage,setItem:()=>{throw Error('quota')}})
 assert.throws(()=>saver.start(plan()));assert.equal(f.calls.length,0);saver.dispose()
})

test('known new asset can recover final read after draft access is unavailable',async()=>{
 const f=fixture(),saver=createPromptSave(f.sdk,f.storage);f.lose('asset_get');await assert.rejects(saver.start(plan()));assert.ok(saver.read()?.asset)
 f.denyDraft();const before=f.calls.length;await saver.resume();assert.deepEqual(f.calls.slice(before),['get']);saver.dispose()
})
test('confirmed failed commit can revise content without creating another draft',async()=>{
 const f=fixture(),saver=createPromptSave(f.sdk,f.storage);f.denyCommit(true);await assert.rejects(saver.start(plan()));const old=saver.read()!
 assert.equal(old.failure_stage,'commit');const r=old.plan.document.results[0]
 const revised=revisePromptCopy(old.plan,{...promptEdit(r,promptVariants(r)[0]),prompt_text:'Corrected body'})
 assert.deepEqual(revised.source_refs,old.plan.source_refs);assert.deepEqual(revised.asset_refs,old.plan.asset_refs)
 f.denyCommit(false);await saver.retryCommit(revised);assert.equal(f.creates.size,1);assert.equal(f.commits.size,1);assert.equal(f.writes.size,2);saver.dispose()
})

test('lost registration and status replies recover via status without requiring the source draft',async()=>{
 const f=fixture(),saver=createPromptSave(f.sdk,f.storage);f.lose('register');f.loseStatus();await assert.rejects(saver.start(plan()))
 assert.equal(saver.read()?.asset,undefined);assert.equal(saver.read()?.registration_started,true)
 f.denyDraft();const before=f.calls.length;await saver.resume();assert.deepEqual(f.calls.slice(before),['status','get']);assert.equal(f.registrations.size,1);saver.dispose()
})
