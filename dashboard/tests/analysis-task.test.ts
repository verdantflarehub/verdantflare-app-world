import test from 'node:test'
import assert from 'node:assert/strict'
import { createAnalysisSubmission, readSubmit, readTask, readAvailability, promptTargets, shouldPoll, taskLabel, type AnalysisTask } from '../src/platform/analysis-task.ts'
import { createDraftAPI } from '../src/platform/draft-client.ts'
import type { AppSDK,Context,Json,Lifecycle } from '../src/platform/types.ts'
const id=(n:number)=>'01900000-0000-7000-8000-'+n.toString(16).padStart(12,'0')
const input={source:{draft_id:id(1),revision_id:id(2),file_id:id(3)},model_id:'gemini-3.1-pro-preview',budget_limit:'10.00',budget_per_call:'0.20',prompt_targets:promptTargets}
const request={...input,mode:'submit',task_id:id(4)}
function fixture(){
 const values=new Map<string,string>(),events=new Set<(e:Lifecycle)=>void>(),contexts=new Set<(c:Context)=>void>(),requests:any[]=[]
 let context:Context={userId:'u',organizationId:'o',stationId:'s',appId:'world',installationId:'i',contextRevision:'r',projectId:null},lose=false,mismatch=false
 const records=new Map<string,any>()
 const sdk:AppSDK={version:'1.1.0',context:{get:()=>context,subscribe:f=>{contexts.add(f);return()=>contexts.delete(f)}},theme:{get:()=>'dark',subscribe:()=>()=>{}},lifecycle:{subscribe:f=>{events.add(f);return()=>events.delete(f)}},navigation:{get:()=>({path:'/',revision:1}),subscribe:()=>()=>{},request:async()=>{}},capabilities:{call:async()=>{throw Error('no Artifact needed for submission')}},app:{request:async r=>{
  assert.equal(r.path,'/api/v1/world/analyze');assert.equal(r.method,'POST');const body=r.body as any;requests.push(structuredClone(body));assert.equal(body.project_id,undefined)
  assert.equal(values.size,1,'persist intent before POST')
  let t=records.get(body.task_id);if(!t){t={task_id:body.task_id,name:'Original',request_state:'pending',source:body.source,model_id:body.model_id,prompt_targets:body.prompt_targets,created_at:'2026-10-20T00:00:00Z',budget:{limit:body.budget_limit,per_call:body.budget_per_call,reserved:'0'}};records.set(body.task_id,t)}
  if(lose){lose=false;throw Error('response lost')}
  const result=structuredClone(t);if(mismatch)result.source.file_id=id(99)
  return {status:200,data:result as Json,requestId:'test'}
 }}}
 const storage={getItem:(k:string)=>values.get(k)||null,setItem:(k:string,v:string)=>{values.set(k,v)},removeItem:(k:string)=>{values.delete(k)}}
 return {sdk,storage,values,requests,records,lose:()=>{lose=true},mismatch:()=>{mismatch=true},emit:(e:Lifecycle)=>events.forEach(f=>f(e)),change:()=>{context={...context,organizationId:'other'};contexts.forEach(f=>f(context))}}
}
test('lost admission response resumes exact World intent after page restart without a Project',async()=>{
 const f=fixture(),first=createAnalysisSubmission(f.sdk,f.storage);f.lose();await assert.rejects(first.start(input),/response lost/)
 assert.equal(f.records.size,1);const old=first.pending();assert.ok(old);assert.equal(old.source.file_id,input.source.file_id)
 await assert.rejects(first.start(input),/恢复/);assert.equal(f.requests.length,1);first.dispose()
 const second=createAnalysisSubmission(f.sdk,f.storage);const t=await second.resume();assert.equal(t.task_id,old.task_id);assert.deepEqual(f.requests[0],f.requests[1]);assert.equal(f.records.size,1);assert.equal(second.pending(),null);second.dispose()
})
test('cannot submit if recovery storage fails, or erase intent on mismatched response',async()=>{
 const f=fixture(),s=createAnalysisSubmission(f.sdk,{...f.storage,setItem:()=>{throw Error('denied')}})
 await assert.rejects(s.start(input),/STORAGE_UNAVAILABLE/);assert.equal(f.requests.length,0);s.dispose()
 const w=createAnalysisSubmission(f.sdk,f.storage);f.mismatch();await assert.rejects(w.start(input),/不一致/);assert.ok(w.pending());assert.equal(f.records.size,1);w.dispose()
})
test('hidden and changed identity cannot start analysis or consume old callbacks',async()=>{
 const f=fixture(),api=createDraftAPI(f.sdk);let finish!:(v:any)=>void,signal:AbortSignal|undefined
 f.sdk.app.request=async(_r,options)=>{signal=options?.signal;return new Promise(resolve=>{finish=resolve})}
 const p=api.analyzeRequest({mode:'get',task_id:id(4)});f.emit('hidden');assert.equal(signal?.aborted,true);finish({status:200,data:{},requestId:'r'});await assert.rejects(p)
 await assert.rejects(api.analyzeRequest(request),/DASHBOARD_HIDDEN/)
 f.emit('active');const write=api.analyzeRequest(request);f.emit('hidden');assert.equal(signal?.aborted,false);finish({status:200,data:{accepted:true},requestId:'r'});assert.deepEqual(await write,{accepted:true})
 f.emit('active');const old=api.analyzeRequest({mode:'get',task_id:id(4)});f.change();assert.equal(signal?.aborted,true);finish({status:200,data:{private:true},requestId:'r'});await assert.rejects(old);await assert.rejects(api.analyzeRequest(request),/SESSION_REVOKED/);api.dispose()
})
test('exact decimal budgets and narrow input reject spoofing and repeated target keys',()=>{
 assert.equal(readSubmit(request).budget_limit,'10.00')
 assert.equal(readSubmit({...request,budget_limit:'999999999.99999999',budget_per_call:'999999999.99999998'}).mode,'submit')
 for(const patch of [{budget_limit:'0'},{budget_per_call:'0'},{budget_limit:'0.19999999'},{budget_limit:'1e3'},{project_id:id(9)},{owner_id:id(8)},{model_id:'bad/model'},{source:{...input.source,url:'https://example.invalid'}},{prompt_targets:[promptTargets[0],promptTargets[0]]},{prompt_targets:[]}])assert.throws(()=>readSubmit({...request,...patch}))
 assert.throws(()=>readAvailability({enabled:true,models:[],input_mime_types:['video/mp4'],max_media_bytes:100}))
})
test('progress never turns terminal failures or budget stops into endless active analysis',()=>{
 const t={task_id:id(4),name:'Example',request_state:'queued',source:input.source,model_id:input.model_id,prompt_targets:promptTargets,created_at:'2026-10-20T00:00:00Z',budget:{limit:'10.00',per_call:'0.20',reserved:'0.20'}} as AnalysisTask
 assert.equal(shouldPoll(t),true)
 t.progress={stage:'needs_attention',last_error_code:'budget_exhausted',running:0};assert.equal(shouldPoll(t),false);assert.match(taskLabel(t),/预算已用尽/)
 t.progress.running=1;assert.equal(shouldPoll(t),true)
 t.progress={stage:'needs_attention',failed:1};assert.equal(shouldPoll(t),false)
 t.learning={task_id:t.task_id,state:'saved'};assert.equal(shouldPoll(t),false)
 assert.throws(()=>readTask(t),/不完整|不一致/)
})
test('a saved response must point to an actual fixed learning revision of the same task',()=>{
 const t:any={task_id:id(4),name:'Example',request_state:'queued',source:input.source,model_id:input.model_id,prompt_targets:promptTargets,created_at:'2026-10-20T00:00:00Z',budget:{limit:'10.00',per_call:'0.20',reserved:'1'}}
 assert.equal(readTask(t,id(4)).name,'Example');assert.throws(()=>readTask(t,id(5)))
 const ref={store_id:id(10),artifact_id:id(11),version_id:id(12)}
 const revision={kind:'revision',draft_id:id(20),revision_id:id(21),head_revision_id:id(22),created_at:t.created_at,manifest_ref:ref,manifest:{schema_version:1,kind:'world-draft',draft_id:id(20),purpose:'learning',name:'Learning',subjects:[],files:[],domain_documents:[],source_refs:[],asset_refs:[],run_refs:[{service_id:'world',run_id:'analysis-v1:'+id(4)}]}}
 t.learning={task_id:id(4),state:'saved',revision};assert.equal(readTask(t).learning?.revision?.revision_id,id(21))
 revision.manifest.run_refs[0].run_id='analysis-v1:'+id(5);assert.throws(()=>readTask(t))
})
