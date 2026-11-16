import type { AppSDK, Json } from './types.ts'
import { contentRef, object, type ContentRef } from './draft-model.ts'
import { DraftError } from './draft-client.ts'
import { publicationIdentity } from './publication-client.ts'
import { isID } from './world-model.ts'

export interface PackageUploadIntent {
  mode: 'prepare'; write_id: string; source: { kind: 'user_import'; world_draft_id: string }
  mime: string; size: number; sha256: string
}
export interface UploadedPackageEntry { content_ref: ContentRef; size: number; sha256: string; mime: string }
const fail = (): never => { throw Error('上传回执与原操作不一致；请保留原操作。') }
export const bytesHash = async (bytes: Uint8Array): Promise<string> => [...new Uint8Array(await crypto.subtle.digest('SHA-256', new Uint8Array(bytes)))].map(b=>b.toString(16).padStart(2,'0')).join('')

/** Caller persists the intent and package before invoking this writer. Never
 * generates a replacement write_id, accepts a caller URL, or creates a draft. */
export function createPackageUploader(sdk: AppSDK, fetcher: typeof fetch = fetch) {
  const context = {...sdk.context.get()}, identity = publicationIdentity(context)
  if (context.appId !== 'world') throw new DraftError('SESSION_REVOKED')
  let disposed = false, hidden = false, busy = false, controller: AbortController | null = null
  const stops: (()=>void)[] = []
  function live() {
    if (disposed || publicationIdentity(sdk.context.get()) !== identity) throw new DraftError('SESSION_REVOKED')
    if (hidden || controller?.signal.aborted) throw new DraftError('DASHBOARD_HIDDEN')
  }
  function dispose() { disposed=true;controller?.abort();stops.splice(0).forEach(s=>s()) }
  stops.push(sdk.context.subscribe(()=>{ if(publicationIdentity(sdk.context.get())!==identity)dispose() }))
  stops.push(sdk.lifecycle.subscribe(e=>{ if(e==='dispose')dispose();else {hidden=e==='hidden';if(hidden)controller?.abort()} }))
  async function write(intent: PackageUploadIntent, input: Uint8Array): Promise<UploadedPackageEntry> {
    if(busy)throw Error('已有文件正在上传，请沿原操作恢复。')
    busy=true;controller=new AbortController()
    try {
      live()
      const raw = object(JSON.parse(JSON.stringify(intent))), source=object(raw.source)
      if(Object.keys(raw).sort().join(',')!=='mime,mode,sha256,size,source,write_id' ||
        Object.keys(source).sort().join(',')!=='kind,world_draft_id' || raw.mode!=='prepare' || !isID(raw.write_id) ||
        source.kind!=='user_import' || !isID(source.world_draft_id) || typeof raw.mime!=='string' || !/^[a-zA-Z0-9!#$&^_.+-]+\/[a-zA-Z0-9!#$&^_.+-]+$/.test(raw.mime) ||
        !Number.isSafeInteger(raw.size) || Number(raw.size)<0 || Number(raw.size)>64*1024*1024 ||
        (raw.mime==='application/json'||raw.mime.endsWith('+json')) && Number(raw.size)>4*1024*1024 ||
        typeof raw.sha256!=='string' || !/^[a-f0-9]{64}$/.test(raw.sha256) || input.byteLength!==raw.size) fail()
      const bytes=new Uint8Array(input)
      if(await bytesHash(bytes)!==raw.sha256)fail()
      live()
      const prepared=object(await sdk.capabilities.call('artifact.write',raw as Json,{signal:controller.signal})), upload=object(prepared.upload)
      live()
      const session='[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}'
      if(!isID(upload.upload_id)||!isID(upload.version_id)||!isID(upload.artifact_id)||!['prepared','committed'].includes(String(upload.state))||
        typeof upload.content_path!=='string'||!new RegExp(`^/studio/dashboards/${session}/uploads/${raw.write_id}$`).test(upload.content_path))fail()
      const response=await fetcher(upload.content_path as string,{method:'PUT',credentials:'same-origin',redirect:'error',cache:'no-store',signal:controller.signal,
        headers:{'Content-Type':'application/octet-stream','X-Artifact-Metadata':JSON.stringify(raw)},body:bytes})
      live()
      if(!response.ok)throw new DraftError(response.status===403?'CONTENT_DENIED':response.status===409?'CONTENT_NOT_READY':'DEPENDENCY_UNAVAILABLE')
      // Receipts are small; never accept an unbounded downstream response.
      if(!response.body)fail()
      const reader=response.body!.getReader(),parts:Uint8Array[]=[];let total=0
      const signal=controller.signal,cancel=()=>{void reader.cancel().catch(()=>{})}
      signal.addEventListener('abort',cancel,{once:true})
      try {for(;;){const next=await reader.read();live();if(next.done)break;total+=next.value.byteLength;if(total>65536)fail();parts.push(next.value)}}
      catch(error){await reader.cancel().catch(()=>{});throw error}finally{signal.removeEventListener('abort',cancel);reader.releaseLock()}
      const joined=new Uint8Array(total);let offset=0;for(const part of parts){joined.set(part,offset);offset+=part.byteLength}
      const version=object(object(JSON.parse(new TextDecoder('utf-8',{fatal:true}).decode(joined))).version), ref=contentRef(version), actualSource=object(version.source)
      if(version.schema_version!==2 || version.organization_id!==context.organizationId || ref.artifact_id!==upload.artifact_id || ref.version_id!==upload.version_id ||
        version.size!==raw.size || version.sha256!==raw.sha256 || version.mime!==raw.mime ||
        Object.keys(actualSource).sort().join(',')!=='kind,world_draft_id' || actualSource.kind!==source.kind || actualSource.world_draft_id!==source.world_draft_id)fail()
      live()
      return {content_ref:ref,size:raw.size as number,sha256:raw.sha256 as string,mime:raw.mime as string}
    } finally {controller=null;busy=false}
  }
  return {write,dispose}
}
