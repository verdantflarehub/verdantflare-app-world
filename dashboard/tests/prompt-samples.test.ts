import test from 'node:test'
import assert from 'node:assert/strict'
import { promptSamples, sampleFileReference, sampleDownload, sampleKind, sampleContentPath, readSampleBytes } from '../src/platform/prompt-samples.ts'
import { openSamplePreview, closeSamplePreview } from '../src/platform/sample-preview.ts'
import type { AssetOpen } from '../src/platform/world-model.ts'
import type { LearningDocument, PromptVariant } from '../src/platform/learning-results.ts'
import type { AppSDK } from '../src/platform/types.ts'
const id=(n:number)=>'01900000-0000-7000-8000-'+n.toString(16).padStart(12,'0')
const ref={store_id:id(10),artifact_id:id(11),version_id:id(12)}
const path='/studio/dashboards/00000000-0000-4000-8000-000000000001/content/00000000-0000-4000-8000-000000000002'
const asset:AssetOpen={asset_id:id(1),asset_version_id:id(2),manifest:{schema_version:2,kind:'asset-version',asset_id:id(1),name:'Unit fixture',asset_type:'prompt',subjects:[],source:{kind:'world_draft',draft_id:id(3),draft_revision_id:id(4),relation:'curated_in'},files:[{file_id:id(5),path:'output.txt',role:'output',content_ref:ref}],depends_on:[]}}
const document={sources:[{key:'original',acquisition_state:'ready',content:{kind:'file',file_id:id(5)}}],results:[]} as unknown as LearningDocument
const sha=async(bytes:Uint8Array)=>Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256',bytes)),b=>b.toString(16).padStart(2,'0')).join('')
test('only selected variant successful outputs enter samples; reference inputs and failed outputs do not',()=>{
 const v={execution_evidence:[{state:'failed',output_refs:[{kind:'file',file_id:id(8)}]},{state:'succeeded',input_refs:[{kind:'file',file_id:id(9)}],output_refs:[{kind:'file',file_id:id(5)}],model_id:'actual',route:'recorded',note:'synthetic test'}]} as PromptVariant
 assert.deepEqual(promptSamples(v).map(s=>s.reference),[{kind:'file',file_id:id(5)}])
 assert.equal(promptSamples({...v,execution_evidence:[]}).length,0)
 assert.throws(()=>promptSamples({...v,execution_evidence:[{state:'succeeded',output_refs:[]}]}))
})
test('source resolution requires ready files and never guesses multi-file or structured objects',()=>{
 assert.deepEqual(sampleFileReference({kind:'source',key:'original'},document),{kind:'file',file_id:id(5)})
 const d=structuredClone(document) as any;d.sources[0].content={kind:'source',key:'original'}
 assert.throws(()=>sampleFileReference({kind:'source',key:'original'},d),/循环/)
 d.sources[0].acquisition_state='declared';assert.throws(()=>sampleFileReference({kind:'source',key:'original'},d),/尚未/)
 assert.throws(()=>sampleFileReference({kind:'result',key:'unknown'},document),/单个/)
 assert.deepEqual(sampleFileReference({kind:'project',project_id:id(30),revision_id:id(31),file_id:id(5)},document),{kind:'project',project_id:id(30),revision_id:id(31),file_id:id(5)})
})
test('media read uses exact asset and content versions; stale files, external URLs and permission failures reject',async()=>{
 const bytes=new TextEncoder().encode('unit test output'),digest=await sha(bytes)
 const reply={mode:'download',version:{...ref,mime:'text/plain',size:bytes.length,sha256:digest},content_path:path}
 let calls=0
 const read=await sampleDownload(async(name,args)=>{calls++;assert.equal(name,'artifact.read');assert.deepEqual(args,{mode:'download',content_ref:ref,access:{asset_id:id(1),asset_version_id:id(2)}});return reply},asset,{kind:'file',file_id:id(5)})
 assert.equal(read.path,path);assert.equal(calls,1)
 for(const bad of [{...reply,version:{...reply.version,version_id:id(99)}},{...reply,content_path:'https://external.invalid/sample'},{...reply,mode:'text'}])await assert.rejects(sampleDownload(async()=>bad,asset,{kind:'file',file_id:id(5)}))
 await assert.rejects(sampleDownload(async()=>{throw Error('forbidden')},asset,{kind:'file',file_id:id(5)}),/forbidden/)
 await assert.rejects(sampleDownload(async()=>{throw Error('must not call')},asset,{kind:'file',file_id:id(99)}),/清单/)
})
test('external assets must be fixed declared dependencies and authorized independently',async()=>{
 const r={kind:'asset',asset_id:id(20),asset_version_id:id(21),file_id:id(5)}
 await assert.rejects(sampleDownload(async()=>{throw Error('must not call')},asset,r),/依赖/)
 const base=structuredClone(asset);base.manifest.depends_on=[{asset_id:id(20),asset_version_id:id(21),purpose:'sample'}]
 await assert.rejects(sampleDownload(async(name,args)=>{assert.equal(name,'world.get');assert.deepEqual(args,{asset_id:id(20),asset_version_id:id(21)});throw Error('revoked')},base,r),/revoked/)
 await assert.rejects(sampleDownload(async()=>{throw Error('must not call')},base,{...r,object_key:'result'}),/结构化对象/)
})
test('project samples resolve only via fixed asset document and use project authorization',async()=>{
 const base=structuredClone(asset);base.manifest.domain_documents=[{document_type:'reference-learning',file_id:id(5)}]
 const reference={kind:'project',project_id:id(30),revision_id:id(31),file_id:id(32)}
 const requested={document_file_id:id(5),project_id:id(30),revision_id:id(31),file_id:id(32)}
 const reply={...base,sample_file:{...requested,content_ref:ref}}
 const calls:string[]=[]
 const result=await sampleDownload(async(name,args)=>{calls.push(name);if(name==='world.get'){assert.deepEqual(args,{asset_id:base.asset_id,asset_version_id:base.asset_version_id,sample_reference:requested});return reply}
 assert.deepEqual(args,{mode:'download',content_ref:ref,access:{project_id:id(30),project_revision_id:id(31)}});return {mode:'download',version:{...ref,mime:'video/mp4',size:10,sha256:'a'.repeat(64)},content_path:path}},base,reference)
 assert.equal(result.mime,'video/mp4');assert.deepEqual(calls,['world.get','artifact.read'])
 for(const key of Object.keys(requested))await assert.rejects(sampleDownload(async()=>({...reply,sample_file:{...reply.sample_file,[key]:id(99)}}),base,reference),/不一致/)
 await assert.rejects(sampleDownload(async()=>{throw Error('revoked')},base,reference),/revoked/)
 for(const bad of [{...reference,revision_id:''},{...reference,object_key:'result'}])await assert.rejects(sampleDownload(async()=>{throw Error('must not call')},base,bad),/固定/)
 await assert.rejects(sampleDownload(async()=>{throw Error('must not call')},asset,reference),/固定/)
})
test('controlled path and supported media reject executable formats and redirects',()=>{
 for(const bad of [path+'?token=secret','//host'+path,path+'/../x','/v2/artifacts/private','javascript:alert(1)'])assert.throws(()=>sampleContentPath(bad))
 for(const mime of ['image/svg+xml','text/html','application/javascript'])assert.equal(sampleKind(mime),'unsupported')
 for(const [mime,kind] of [['image/png','image'],['audio/mpeg','audio'],['video/mp4','video'],['text/plain','text']])assert.equal(sampleKind(mime),kind)
})
test('content preview verifies byte count and digest, bounds allocation and honors abort',async()=>{
 const bytes=new TextEncoder().encode('<script>text stays text</script>'),digest=await sha(bytes)
 const read={path,mime:'text/plain',size:bytes.length,sha256:digest},controller=new AbortController()
 const fetcher=(async(_url,options)=>{assert.equal(options?.redirect,'error');assert.equal(options?.credentials,'same-origin');return new Response(bytes)}) as typeof fetch
 assert.equal(await (await readSampleBytes(read,controller.signal,fetcher)).text(),new TextDecoder().decode(bytes))
 for(const changed of [{...read,sha256:'0'.repeat(64)},{...read,size:bytes.length-1},{...read,size:bytes.length+1}])await assert.rejects(readSampleBytes(changed,controller.signal,fetcher))
 await assert.rejects(readSampleBytes({...read,size:5*1024*1024},controller.signal,async()=>{throw Error('must not fetch')}),/上限/)
 controller.abort();await assert.rejects(readSampleBytes(read,controller.signal,fetcher),e=>(e as Error).name==='AbortError')
})
test('a hidden or superseded sample cannot reopen or close another sample lightbox',async()=>{
 const events:string[]=[],sdk={media:{preview:async()=>{events.push('open')},close:()=>events.push('close')}} as unknown as AppSDK
 let resolve!:(v:ArrayBuffer)=>void
 const slow={arrayBuffer:()=>new Promise<ArrayBuffer>(r=>resolve=r),type:'image/png'} as Blob
 const first={},second={}
 const pending=openSamplePreview(sdk,first,slow,'first');closeSamplePreview(sdk,first)
 resolve(new ArrayBuffer(0));await pending;assert.deepEqual(events,['close'])
 await openSamplePreview(sdk,second,new Blob(['test'],{type:'image/png'}),'second')
 closeSamplePreview(sdk,first);assert.deepEqual(events,['close','open'])
 closeSamplePreview(sdk,second);assert.deepEqual(events,['close','open','close'])
})
