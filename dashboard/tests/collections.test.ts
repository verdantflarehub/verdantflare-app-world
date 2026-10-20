import test from 'node:test'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { createCollectionSave } from '../src/platform/collection-save.ts'
import { createDraftAPI } from '../src/platform/draft-client.ts'
import { readDraft, readDraftList, makeCollectionDocument, editableCollection } from '../src/platform/draft-model.ts'
import { canonicalRoute } from '../src/platform/route-links.ts'
import type { AppSDK, Context, Json, Lifecycle } from '../src/platform/types.ts'
const id=(n:number)=>'01900000-0000-7000-8000-'+n.toString(16).padStart(12,'0')
const copy=<T>(v:T):T=>JSON.parse(JSON.stringify(v))
function fixture(){
 const events=new Set<(e:Lifecycle)=>void>(),contexts=new Set<(c:Context)=>void>(),values=new Map<string,string>()
 let context:Context={userId:'a',organizationId:'o',stationId:'s',appId:'world',installationId:'i',contextRevision:'r',projectId:null},lost='',conflict=false,requests=0
 const creates=new Map<string,any>(),writes=new Map<string,any>(),commits=new Map<string,any>(),revisions=new Map<string,any>()
 const revision=(manifest:any,rev:string)=>({kind:'revision',draft_id:manifest.draft_id,revision_id:rev,head_revision_id:rev,created_at:'2026-10-19T00:00:00Z',manifest_ref:{store_id:id(1),artifact_id:id(2),version_id:rev},manifest})
 const sdk:AppSDK={version:'1.1.0',context:{get:()=>context,subscribe:f=>{contexts.add(f);return()=>contexts.delete(f)}},theme:{get:()=>'dark',subscribe:()=>()=>{}},lifecycle:{subscribe:f=>{events.add(f);return()=>events.delete(f)}},navigation:{get:()=>({path:'/',revision:1}),subscribe:()=>()=>{},request:async()=>{}},
 capabilities:{call:async(name,input)=>{
  assert.equal(name,'artifact.write');const a=input as any;assert.equal(a.source.project_id,undefined)
  let data=writes.get(a.write_id)
  if(!data){data={version:{store_id:id(3),artifact_id:id(4),version_id:id(5),source:a.source,mime:a.mime,size:Buffer.byteLength(a.text),sha256:createHash('sha256').update(a.text).digest('hex')}};writes.set(a.write_id,data)}
  if(lost==='write'){lost='';throw Error('lost write reply')};return copy(data)
 }},app:{request:async(input)=>{
  requests++;assert.equal(input.path,'/api/v1/world/draft');const a=input.body as any;assert.equal(a.project_id,undefined);let data:any
  if(a.mode==='create'){data=creates.get(a.commit_id);if(!data){data=revision({schema_version:1,kind:'world-draft',draft_id:id(20),purpose:'collection',name:a.name,subjects:[],files:[],domain_documents:[],source_refs:[],asset_refs:[],run_refs:[]},id(21));creates.set(a.commit_id,data);revisions.set(data.revision_id,data)}}
  else if(a.mode==='commit'){if(conflict)return{status:409,data:{code:'REVISION_CONFLICT'},requestId:'x'};data=commits.get(a.commit_id);if(!data){assert.equal(a.expected_revision_id,id(21));data=revision(a.manifest,id(22));commits.set(a.commit_id,data);revisions.set(data.revision_id,data)}}
  else if(a.mode==='get')data=revisions.get(a.revision_id)
  else if(a.mode==='status')data=conflict?{kind:'status',state:'conflict',request_sha256:'a'.repeat(64),error_code:'REVISION_CONFLICT',current_revision_id:id(23)}:{kind:'status',state:'committed',request_sha256:'a'.repeat(64),result:commits.get(a.commit_id)}
  else throw Error('unexpected operation')
  if(lost===a.mode){lost='';throw Error('lost '+a.mode+' reply')};return{status:200,data:copy(data) as Json,requestId:'x'}
 }}}
 const storage={getItem:(k:string)=>values.get(k)||null,setItem:(k:string,v:string)=>{values.set(k,v)},removeItem:(k:string)=>{values.delete(k)}}
 return{sdk,storage,creates,writes,commits,values,lose:(s:string)=>{lost=s},conflict:()=>{conflict=true},count:()=>requests,emit:(e:Lifecycle)=>events.forEach(f=>f(e)),change:()=>{context={...context,organizationId:'other'};contexts.forEach(f=>f(context))}}
}
const form={name:'A scene worth keeping',text:'A complete source text.',url:''}
for(const step of ['create','write','commit','get'])test('collection recovers lost '+step+' reply without duplicate work',async()=>{
 const f=fixture();f.lose(step);let writer=createCollectionSave(f.sdk,f.storage)
 await assert.rejects(writer.start(form));assert.ok(writer.read());writer.dispose();writer=createCollectionSave(f.sdk,f.storage)
 const result=await writer.resume();assert.equal(result.manifest.purpose,'collection');assert.equal(result.manifest.files.length,1)
 assert.equal(f.creates.size,1);assert.equal(f.writes.size,1);assert.equal(f.commits.size,1);assert.equal(writer.read(),null);assert.equal(f.values.size,0);writer.dispose()
})
test('storage failure prevents writes and authoritative conflict retains the edit',async()=>{
 const f=fixture(),writer=createCollectionSave(f.sdk,{...f.storage,setItem:()=>{throw Error('quota')}})
 assert.throws(()=>writer.start(form));assert.equal(f.count(),0);writer.dispose()
 f.conflict();const other=createCollectionSave(f.sdk,f.storage);await assert.rejects(other.start(form));assert.equal(other.read()?.failure,'conflict')
 const before=f.count();await assert.rejects(other.resume());assert.equal(f.count(),before);assert.deepEqual(other.releaseFailed(),form);other.dispose()
})
test('links stay declarations and file/analysis sources cannot be flattened',()=>{
 const d=makeCollectionDocument({name:'link',text:'',url:'https://example.test/movie'});assert.equal(d.sources[0].acquisition_state,'declared');assert.equal(d.sources[0].content,undefined);assert.deepEqual(d.results,[])
 for(const url of ['javascript:alert(1)','https://user:pass@example.test/'])assert.throws(()=>makeCollectionDocument({...form,url}))
 d.sources[0].content={kind:'file',file_id:id(1)};d.sources[0].acquisition_state='ready';assert.equal(editableCollection(d),null)
 const annotated=makeCollectionDocument(form);annotated.sources[0].content={kind:'inline',text:form.text,language:'zh'};assert.equal(editableCollection(annotated),null)
 const extended=makeCollectionDocument(form);extended.notes='must not be lost';assert.equal(editableCollection(extended),null)
 assert.deepEqual(editableCollection(makeCollectionDocument(form)),form)
})
test('editing keeps sibling files, references and original IDs; recovery never mutates the base',async()=>{
 const f=fixture()
 const reply=await f.sdk.app.request({method:'POST',path:'/api/v1/world/draft',body:{mode:'create',commit_id:id(30),name:form.name}})
 const base=readDraft(reply.data)
 const ref={store_id:id(1),artifact_id:id(2),version_id:id(3)}
 base.manifest.files=[{file_id:id(40),path:'sources/collection.json',role:'reference',content_ref:ref},{file_id:id(41),path:'notes.txt',role:'reference',content_ref:ref}]
 base.manifest.domain_documents=[{document_type:'reference-learning',file_id:id(40)}]
 base.manifest.subjects=['camera','reference']
 base.manifest.source_refs=[{kind:'world_draft',draft_id:id(50),revision_id:id(51),file_id:id(41),purpose:'reference'}]
 base.manifest.asset_refs=[{asset_id:id(52),asset_version_id:id(53),purpose:'reference'}]
 base.manifest.run_refs=[{service_id:'video',run_id:id(54)}]
 const before=copy(base),writer=createCollectionSave(f.sdk,f.storage)
 f.lose('commit');await assert.rejects(writer.start({...form,name:'Edited'},base))
 const pending=writer.read()!;assert.equal(pending.base?.revision_id,base.revision_id)
 const result=await writer.resume()
 assert.deepEqual(base,before)
 assert.equal(f.creates.size,1);assert.equal(f.writes.size,1);assert.equal(f.commits.size,1)
 assert.equal(result.manifest.name,'Edited');assert.equal(result.manifest.files.length,2)
 assert.deepEqual(result.manifest.files[1],before.manifest.files[1])
 assert.equal(result.manifest.files[0].file_id,id(40));assert.equal(result.manifest.files[0].path,'sources/collection.json')
 for(const key of ['subjects','domain_documents','source_refs','asset_refs','run_refs'] as const)assert.deepEqual(result.manifest[key],before.manifest[key])
 writer.dispose()
})
test('separate collection routes preserve fixed source; other-purpose lists are rejected',()=>{
 const item={draft_id:id(1),head_revision_id:id(2),name:'x',purpose:'collection',subjects:[],has_content:false,created_at:'now',updated_at:'now'}
 assert.equal(readDraftList({kind:'list',items:[item],next_cursor:null},'collection').items.length,1)
 assert.throws(()=>readDraftList({kind:'list',items:[{...item,purpose:'creation'}],next_cursor:null},'collection'))
 assert.throws(()=>readDraftList({kind:'list',items:[item,item],next_cursor:null},'collection'))
 for(const path of ['/collections/'+id(1)+'?revision='+id(2),'/learn?draft='+id(1)+'&revision='+id(2)])assert.equal(canonicalRoute('world',path),path)
 assert.throws(()=>canonicalRoute('world','/collections/not-an-id'));assert.throws(()=>canonicalRoute('world','/collections/'+id(1)+'/extra'))
})
test('hiding cancels reads but not accepted writes; changed identity revokes both',async()=>{
 const f=fixture();let finish!:(v:{status:number;data:Json;requestId:string})=>void,signal:AbortSignal|undefined
 f.sdk.app.request=async(_input,options)=>{signal=options?.signal;return new Promise(resolve=>{finish=resolve})};const api=createDraftAPI(f.sdk)
 const read=api.request({mode:'list'});f.emit('hidden');assert.equal(signal?.aborted,true);finish({status:200,data:{private:1},requestId:'x'});await assert.rejects(read)
 f.emit('active');const write=api.request({mode:'create'});f.emit('hidden');assert.equal(signal?.aborted,false);finish({status:200,data:{saved:true},requestId:'x'});assert.deepEqual(await write,{saved:true})
 f.emit('active');const old=api.request({mode:'list'});f.change();assert.equal(signal?.aborted,true);finish({status:200,data:{private:2},requestId:'x'});await assert.rejects(old);await assert.rejects(api.request({mode:'list'}));api.dispose()
})

// Service HTTP routes are distinct from the UI's short operation names.
test('asset status recovery uses commit_status and large draft requests stay local',async()=>{
 const f=fixture(),paths:string[]=[]
 f.sdk.app.request=async input=>{paths.push(input.path);return {status:200,data:{state:'preparing'},requestId:'test'}}
 const api=createDraftAPI(f.sdk)
 await api.assetRequest('status',{commit_id:id(90)})
 assert.deepEqual(paths,['/api/v1/world/commit_status'])
 await assert.rejects(api.request({mode:'commit',manifest:{notes:'世'.repeat(400000)}}),/REQUEST_TOO_LARGE/)
 assert.equal(paths.length,1)
 api.dispose()
})
