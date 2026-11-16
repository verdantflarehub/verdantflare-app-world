import test from 'node:test'
import assert from 'node:assert/strict'
import { bytesHash, createPackageUploader, type PackageUploadIntent } from '../src/platform/package-upload.ts'
import type { AppSDK, Context, Lifecycle } from '../src/platform/types.ts'

const id='01900000-0000-7000-8000-000000000001',session='01900000-0000-4000-8000-000000000002'
async function fixture() {
  const bytes=new TextEncoder().encode('a'.repeat(1901755)),hash=await bytesHash(bytes)
  const intent:PackageUploadIntent={mode:'prepare',write_id:id,source:{kind:'user_import',world_draft_id:id},mime:'application/json',size:bytes.length,sha256:hash}
  const context:Context={userId:id,organizationId:id,stationId:id,projectId:null,appId:'world',installationId:id,contextRevision:'1'}
  let lifecycle:(e:Lifecycle)=>void=()=>{},change:()=>void=()=>{},prepareCount=0,transferCount=0
  const requests:unknown[]=[]
  const upload={upload_id:id,version_id:id,artifact_id:id,state:'prepared',content_path:`/studio/dashboards/${session}/uploads/${id}`}
  const version={schema_version:2,store_id:id,artifact_id:id,version_id:id,organization_id:id,source:intent.source,size:bytes.length,sha256:hash,mime:'application/json'}
  const sdk={context:{get:()=>context,subscribe:(f:()=>void)=>{change=f;return()=>{}}},lifecycle:{subscribe:(f:(e:Lifecycle)=>void)=>{lifecycle=f;return()=>{}}},capabilities:{call:async(name:string,input:unknown)=>{assert.equal(name,'artifact.write');prepareCount++;requests.push(input);return {upload}}}} as unknown as AppSDK
  const fetcher=(async(path,options)=>{transferCount++;assert.equal(path,upload.content_path);assert.equal(options?.redirect,'error');assert.equal(options?.credentials,'same-origin');assert.equal(options?.method,'PUT');assert.deepEqual(JSON.parse((options?.headers as Record<string,string>)['X-Artifact-Metadata']),intent);assert.deepEqual(options?.body,bytes);return new Response(JSON.stringify({version}))}) as typeof fetch
  return {bytes,intent,sdk,fetcher,upload,version,requests,context,lifecycle:(e:Lifecycle)=>lifecycle(e),change:()=>change(),counts:()=>[prepareCount,transferCount]}
}

test('large source upload validates exact receipt and reuses immutable write id',async()=>{
  const f=await fixture(),client=createPackageUploader(f.sdk,f.fetcher)
  for(let i=0;i<2;i++){const saved=await client.write(f.intent,f.bytes);assert.equal(saved.sha256,f.intent.sha256)}
  assert.deepEqual(f.counts(),[2,2]);assert.deepEqual(f.requests,[f.intent,f.intent]);client.dispose()
})
test('wrong bytes and overlimit inputs produce no request',async()=>{
  const f=await fixture(),client=createPackageUploader(f.sdk,f.fetcher)
  const wrong=new Uint8Array(f.bytes);wrong[0]=0
  await assert.rejects(client.write(f.intent,wrong));await assert.rejects(client.write({...f.intent,size:5*1024*1024},f.bytes))
  assert.deepEqual(f.counts(),[0,0]);client.dispose()
})
test('external URL, another write id, and query descriptors never receive bytes',async()=>{
  for(const path of ['https://foreign.invalid/upload',`/studio/dashboards/${session}/uploads/${id}?token=x`,`/studio/dashboards/${session}/uploads/01900000-0000-7000-8000-000000000003`]){
    const f=await fixture();f.upload.content_path=path;const client=createPackageUploader(f.sdk,f.fetcher)
    await assert.rejects(client.write(f.intent,f.bytes));assert.deepEqual(f.counts(),[1,0]);client.dispose()
  }
})
test('wrong receipt org, digest, scope or size rejected',async()=>{
  for(const mutate of [(v:any)=>{v.organization_id='other'},(v:any)=>{v.sha256='0'.repeat(64)},(v:any)=>{v.source={kind:'user_import',world_draft_id:'other'}},(v:any)=>{v.size++}]){
    const f=await fixture();mutate(f.version);const client=createPackageUploader(f.sdk,f.fetcher)
    await assert.rejects(client.write(f.intent,f.bytes));client.dispose()
  }
})
test('changed identity and hidden page prevent starting transfer',async()=>{
  const f=await fixture(),client=createPackageUploader(f.sdk,f.fetcher)
  f.lifecycle('hidden');await assert.rejects(client.write(f.intent,f.bytes));assert.deepEqual(f.counts(),[0,0])
  f.lifecycle('active');f.context.contextRevision='2';f.change();await assert.rejects(client.write(f.intent,f.bytes));assert.deepEqual(f.counts(),[0,0])
})
test('unknown upload response preserves original input for same-id retry',async()=>{
  const f=await fixture();let calls=0
  const client=createPackageUploader(f.sdk,(async(...args)=>{if(calls++===0)throw Error('response lost');return f.fetcher(...args)}) as typeof fetch)
  await assert.rejects(client.write(f.intent,f.bytes));await client.write(f.intent,f.bytes)
  assert.deepEqual(f.requests,[f.intent,f.intent]);client.dispose()
})
test('hiding cancels a stalled receipt and leaves original intent reusable',async()=>{
  const f=await fixture();let ready!:()=>void,cancelled=false
  const started=new Promise<void>(resolve=>{ready=resolve})
  const client=createPackageUploader(f.sdk,(async()=>new Response(new ReadableStream<Uint8Array>({pull(){ready()},cancel(){cancelled=true}},{highWaterMark:0}))) as typeof fetch)
  const attempt=client.write(f.intent,f.bytes)
  await started;f.lifecycle('hidden');await assert.rejects(attempt);assert.equal(cancelled,true)
  assert.deepEqual(f.requests,[f.intent]);client.dispose()
})
