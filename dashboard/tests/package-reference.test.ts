import test from 'node:test'
import assert from 'node:assert/strict'
import { createPackageReferenceReader } from '../src/platform/package-reference.ts'
import { bytesHash } from '../src/platform/package-upload.ts'
import type { AppSDK, Lifecycle } from '../src/platform/types.ts'
const id='01900000-0000-7000-8000-000000000001',session='01900000-0000-4000-8000-000000000001'
async function fixture(){
  const ref={kind:'project',project_id:id,revision_id:id,file_id:id,purpose:'learning_source'},content={store_id:id,artifact_id:id,version_id:id}
  const bytes=new TextEncoder().encode('Authorized original bytes'),hash=await bytesHash(bytes)
  const reply:any={kind:'reference',reference:ref,content_ref:content,size:bytes.length,mime:'application/octet-stream',sha256:hash}
  const download:any={mode:'download',version:{schema_version:2,organization_id:id,...content,size:bytes.length,mime:reply.mime,sha256:hash},content_path:`/studio/dashboards/${session}/content/${session}`}
  const context={userId:id,organizationId:id,stationId:id,appId:'world',installationId:id,contextRevision:'1',projectId:null}
  const events:((e:Lifecycle)=>void)[]=[],changes:(()=>void)[]=[],calls:any[]=[];let fetches=0
  const sdk={context:{get:()=>context,subscribe:(f:()=>void)=>{changes.push(f);return()=>{}}},lifecycle:{subscribe:(f:(e:Lifecycle)=>void)=>{events.push(f);return()=>{}}},app:{request:async({body}:any)=>{calls.push(body);return {status:200,data:reply}}},capabilities:{call:async(name:string,body:any)=>{assert.equal(name,'artifact.read');assert.deepEqual(body.access,{project_id:id,project_revision_id:id});calls.push(body);return download}}} as unknown as AppSDK
  const fetcher=(async(path,options)=>{fetches++;assert.equal(path,download.content_path);assert.equal(options?.redirect,'error');return new Response(bytes)}) as typeof fetch
  return {ref,reply,download,context,sdk,fetcher,bytes,calls,fetches:()=>fetches,hide:()=>events.forEach(f=>f('hidden')),change:()=>changes.forEach(f=>f())}
}
test('fixed reference reads only bounded same-origin bytes with original scope and digest',async()=>{
  const f=await fixture(),reader=createPackageReferenceReader(f.sdk,f.fetcher),r=await reader.read(f.ref)
  assert.deepEqual(r.bytes,f.bytes);assert.deepEqual(f.calls.map(c=>c.mode),['resolve_reference','download']);reader.dispose()
})
test('unknown fields or missing fixed revision reject before network',async()=>{
  const f=await fixture(),reader=createPackageReferenceReader(f.sdk,f.fetcher)
  await assert.rejects(reader.resolve({...f.ref,token:'not-allowed'}));await assert.rejects(reader.resolve({...f.ref,revision_id:undefined}));assert.equal(f.calls.length,0);reader.dispose()
})
test('wrong source identity, altered metadata and external download paths never fetch bytes',async()=>{
  for(const mutate of [(f:any)=>{f.reply.reference={...f.ref,file_id:'01900000-0000-7000-8000-000000000002'}},(f:any)=>{f.download.version.organization_id='foreign'},(f:any)=>{f.download.version.sha256='f'.repeat(64)},(f:any)=>{f.download.content_path='https://untrusted.invalid/content'}]){
    const f=await fixture();mutate(f);const reader=createPackageReferenceReader(f.sdk,f.fetcher);await assert.rejects(reader.read(f.ref));assert.equal(f.fetches(),0);reader.dispose()
  }
})
test('oversized, truncated and corrupted reads are rejected',async()=>{
  {const f=await fixture(),reader=createPackageReferenceReader(f.sdk,f.fetcher);await assert.rejects(reader.read(f.ref,1));assert.equal(f.fetches(),0);reader.dispose()}
  for(const bytes of [new Uint8Array(1),new Uint8Array(25),new Uint8Array(100)]){
    const f=await fixture(),reader=createPackageReferenceReader(f.sdk,async()=>new Response(bytes));await assert.rejects(reader.read(f.ref));reader.dispose()
  }
})
test('hidden page cancels stalled streams and changed identity prevents further reads',async()=>{
  const f=await fixture();let ready!:()=>void,cancelled=false;const pending=new Promise<void>(r=>ready=r)
  const reader=createPackageReferenceReader(f.sdk,async()=>new Response(new ReadableStream({start(){ready()},cancel(){cancelled=true}})))
  const read=reader.read(f.ref);await pending;f.hide();await assert.rejects(read);assert(cancelled)
  f.context.contextRevision='2';f.change();await assert.rejects(reader.resolve(f.ref));reader.dispose()
})
