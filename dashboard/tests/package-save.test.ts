import test from 'node:test'
import assert from 'node:assert/strict'
import { createPackageSave, type RecoveryStore } from '../src/platform/package-save.ts'
import { encodeLearningPackage, type LearningPackageManifest } from '../src/platform/learning-package.ts'
import { bytesHash } from '../src/platform/package-upload.ts'
import type { PackageRecovery } from '../src/platform/package-recovery.ts'
import type { AppSDK } from '../src/platform/types.ts'
const id='01900000-0000-7000-8000-000000000001',revision='01900000-0000-7000-8000-000000000002',savedRevision='01900000-0000-7000-8000-000000000003'
const ref={kind:'world_draft',draft_id:id,revision_id:revision,file_id:id,purpose:'learning_source'},content={store_id:id,artifact_id:id,version_id:id}
const encode=(v:unknown)=>new TextEncoder().encode(JSON.stringify(v))
async function fixture() {
  const document={schema_version:1,document_kind:'reference-learning',origin:'manual',sources:[],results:[],coverage:[],relations:[],uses:[]}
  const entries=(['baseline','working','relations'] as const).map(role=>({key:role,role,editable:role==='working',path:role+'.json',mime:'application/json',source_ref:ref,size_bytes:0,sha256:''}))
  const payloads=new Map([['baseline',encode(document)],['working',encode(document)],['relations',encode({schema_version:1,document_relations:[],entry_refs:Object.fromEntries(entries.map(e=>[e.key,ref]))})]])
  for(const e of entries){e.size_bytes=payloads.get(e.key)!.length;e.sha256=await bytesHash(payloads.get(e.key)!)}
  const manifest:LearningPackageManifest={schema_version:1,kind:'learning-package',package_id:id,created_at:'2026-11-16T00:00:00Z',scope:'complete',selected_result_keys:[],root_refs:[ref],entries,relation_file_path:'relations.json',missing_items:[]}
  const bytes=await encodeLearningPackage(manifest,payloads,'Test package')
  let record:PackageRecovery|null=null
  const faults={storage:false,authorize:false,upload:false,commit:false,readback:false,terminal:false,status:false}
  const storage:RecoveryStore={read:async()=>structuredClone(record),start:async(intent,bytes)=>{if(faults.storage)throw Error('quota');if(record)throw Error('exists');record={intent:structuredClone(intent),package_bytes:new Uint8Array(bytes),sequence:0,progress:{}}},checkpoint:async(commit,sequence,progress)=>{assert.equal(record?.intent.commit_id,commit);assert.equal(record?.sequence,sequence);record!.progress=structuredClone(progress);return ++record!.sequence},release:async(commit,sequence)=>{assert.equal(record?.intent.commit_id,commit);assert.equal(record?.sequence,sequence);record=null}}
  const requests:any[]=[],uploads:any[]=[],creates=new Map(),commits=new Map()
  let saved:any,checks=0
  const sdk={context:{get:()=>({appId:'world',userId:'test',organizationId:'test',stationId:'test',installationId:'test',contextRevision:'1'}),subscribe:()=>()=>{}},lifecycle:{subscribe:()=>()=>{}},app:{request:async({body}:any)=>{
    assert(record,'remote mutation before durability');requests.push(structuredClone(body));let data:any
    if(body.mode==='create'){
      data=creates.get(body.commit_id)
      if(!data){data={kind:'revision',draft_id:id,revision_id:revision,head_revision_id:revision,created_at:'2026-11-16T00:00:00Z',manifest_ref:content,manifest:{schema_version:1,kind:'world-draft',draft_id:id,purpose:'learning',name:body.name,subjects:body.subjects,files:[],domain_documents:[],source_refs:[],asset_refs:[],run_refs:[]}};creates.set(body.commit_id,data)}
    }else if(body.mode==='commit'){
      if(faults.terminal)return {status:409,data:{code:'REVISION_CONFLICT'}}
      data=commits.get(body.commit_id)
      if(!data){data={kind:'revision',draft_id:id,revision_id:savedRevision,head_revision_id:savedRevision,created_at:'2026-11-16T00:00:00Z',manifest_ref:content,manifest:structuredClone(body.manifest)};data.manifest.files.reverse();commits.set(body.commit_id,data);saved=data}
      if(faults.commit){faults.commit=false;throw Error('commit response lost')}
    }else if(body.mode==='status'){if(faults.status)throw Error('status unavailable');data={kind:'status',state:faults.terminal?'conflict':'committed',error_code:'REVISION_CONFLICT'}}
    else if(body.mode==='get'){if(faults.readback){faults.readback=false;throw Error('read unavailable')}data=saved}
    else throw Error('unexpected mode')
    return {status:200,data:structuredClone(data)}
  }}} as unknown as AppSDK
  const writer={dispose(){},write:async(intent:any,raw:Uint8Array)=>{assert(record);uploads.push(structuredClone(intent));assert.equal(intent.sha256,await bytesHash(raw));if(faults.upload){faults.upload=false;throw Error('upload response lost')}return {content_ref:content,mime:intent.mime,size:intent.size,sha256:intent.sha256}}}
  const authorize=async()=>{checks++;if(faults.authorize)throw Error('denied')}
  const client=()=>createPackageSave(sdk,storage,authorize,writer)
  return {bytes,faults,storage,client,requests,uploads,creates,commits,record:()=>record,checks:()=>checks}
}
test('import commits a new learning revision, accepts service ordering and releases only after fixed readback',async()=>{
  const f=await fixture(),saved=await f.client().start(f.bytes,'Imported',['b','a'],[])
  assert.equal(saved.revision_id,savedRevision);assert.equal(f.creates.size,1);assert.equal(f.commits.size,1);assert.equal(f.uploads.length,3);assert.equal(f.record(),null)
  assert.deepEqual(f.requests.map(r=>r.mode),['create','commit','get']);assert.deepEqual(saved.manifest.subjects,['a','b'])
})
test('invalid package, storage failure and permission denial cause zero remote mutations',async()=>{
  for(const scenario of ['invalid','storage','authorize']){const f=await fixture();if(scenario!=='invalid')f.faults[scenario]=true
    await assert.rejects(f.client().start(scenario==='invalid'?new Uint8Array([1]):f.bytes,'Imported',[],[]));assert.equal(f.requests.length,0);assert.equal(f.uploads.length,0)
  }
})
test('unknown upload resumes fixed IDs after a new client is created',async()=>{
  const f=await fixture();f.faults.upload=true;const first=f.client();await assert.rejects(first.start(f.bytes,'Imported',[],[]));const intent=structuredClone(f.record()!.intent);first.dispose()
  await f.client().resume();assert.equal(f.creates.size,1);assert.deepEqual(f.uploads[0],f.uploads[1]);assert.equal(f.requests.find(r=>r.mode==='commit').commit_id,intent.commit_id);assert.equal(f.record(),null)
})
test('lost commit response replays same commit and uploaded files without duplicate create or upload',async()=>{
  const f=await fixture();f.faults.commit=true;await assert.rejects(f.client().start(f.bytes,'Imported',[],[]));const count=f.uploads.length,original=f.record()!.intent.commit_id
  await f.client().resume();assert.equal(f.creates.size,1);assert.equal(f.commits.size,1);assert.equal(f.uploads.length,count);assert(f.requests.filter(r=>r.mode==='commit').every(r=>r.commit_id===original))
})
test('saved receipt remains recoverable until fixed readback succeeds',async()=>{
  const f=await fixture();f.faults.readback=true;await assert.rejects(f.client().start(f.bytes,'Imported',[],[]));assert(f.record()!.progress.saved)
  await f.client().resume();assert.equal(f.requests.filter(r=>r.mode==='commit').length,1);assert.equal(f.record(),null)
})
test('authoritative conflict preserves failed operation and forbids silent retry or replacement',async()=>{
  const f=await fixture();f.faults.terminal=true;await assert.rejects(f.client().start(f.bytes,'Imported',[],[]));const count=f.requests.length
  assert.equal(f.record()!.progress.failure,'REVISION_CONFLICT');await assert.rejects(f.client().resume());await assert.rejects(f.client().start(f.bytes,'New',[],[]));assert.equal(f.requests.length,count)
})

test('explicit return to editing rechecks terminal status and retains original package and mapping',async()=>{
  const f=await fixture();f.faults.terminal=true;await assert.rejects(f.client().start(f.bytes,'Imported',[],[]))
  const original=structuredClone(f.record()),writes=f.uploads.length
  const result=await f.client().returnFailedToEditing()
  assert.deepEqual(result,original);assert.equal(f.record(),null)
  assert.equal(f.requests.at(-1).mode,'status');assert.equal(f.uploads.length,writes)
  assert.equal(f.creates.size,1);assert.equal(f.commits.size,0)
})

test('unknown or contradictory status never releases a failed recovery record',async()=>{
  for(const scenario of ['unknown','committed']){
    const f=await fixture();f.faults.terminal=true;await assert.rejects(f.client().start(f.bytes,'Imported',[],[]))
    const original=structuredClone(f.record())
    if(scenario==='unknown')f.faults.status=true;else f.faults.terminal=false
    await assert.rejects(f.client().returnFailedToEditing());assert.deepEqual(f.record(),original)
  }
})

test('unfinished or saved imports cannot be discarded through the failure editor',async()=>{
  for(const stage of ['upload','readback']){
    const f=await fixture();f.faults[stage]=true;await assert.rejects(f.client().start(f.bytes,'Imported',[],[]))
    const original=structuredClone(f.record()),count=f.requests.length
    await assert.rejects(f.client().returnFailedToEditing());assert.deepEqual(f.record(),original);assert.equal(f.requests.length,count)
  }
})
