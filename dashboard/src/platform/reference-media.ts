import { inject, onBeforeUnmount, provide, ref, type InjectionKey, type Ref } from 'vue'
import { useHost } from './host'
import { createDraftAPI } from './draft-client'
import { readDraft, object } from './draft-model'
import { isID } from './world-model'
import { publicationIdentity } from './publication-client'
import { readSampleBytes, sampleContentPath } from './prompt-samples'
import { openSamplePreview, closeSamplePreview } from './sample-preview'
import { record, type ResultReference } from './result-reference'
import { captureReferenceFrame } from './reference-frame'
export interface ReferenceMedia {
  epoch: Ref<number>; active: Ref<boolean>
  load(reference: ResultReference): Promise<string>
  frame(reference: ResultReference, at: number): Promise<string>
  preview(url: string, title: string): Promise<void>
}
export const referenceMediaKey: InjectionKey<ReferenceMedia> = Symbol('world-reference-media')
/** One verified original per source and page. All consumers lose access on identity/lifecycle change. */
export function provideReferenceMedia(): ReferenceMedia {
  const inherited = inject(referenceMediaKey, null)
  if (inherited) { provide(referenceMediaKey, inherited); return inherited }
  const { appSDK } = useHost(), api = createDraftAPI(appSDK), epoch = ref(0), active = ref(true)
  const identity=publicationIdentity(appSDK.context.get()), previewKey={}
  const originals=new Map<string,Promise<string>>(), frames=new Map<string,Promise<string>>(), blobs=new Map<string,Blob>()
  let frameQueue:Promise<unknown>=Promise.resolve()
  let controller=new AbortController(), revoked=false, disposed=false
  function clear() { controller.abort(); controller=new AbortController(); epoch.value++; originals.clear(); frames.clear(); for(const url of blobs.keys())URL.revokeObjectURL(url); blobs.clear(); closeSamplePreview(appSDK,previewKey) }
  function live(generation:number) { if(!active.value||revoked||disposed||generation!==epoch.value) throw Error('预览已停止，请重新打开此成果。') }
  const key=(r:ResultReference)=>JSON.stringify([r.source.content,r.sha256])
  async function read(r:ResultReference):Promise<string> {
    const generation=epoch.value; live(generation)
    const content=record(r.source.content)
    if(content.kind!=='world_draft'||!isID(content.draft_id)||!isID(content.revision_id)||!isID(content.file_id))throw Error('缺少可读取的固定原片引用。')
    const owner=readDraft(await api.request({mode:'get',draft_id:content.draft_id,revision_id:content.revision_id}),content.draft_id,content.revision_id)
    live(generation)
    const file=owner.manifest.files.find(f=>f.file_id===content.file_id)
    if(!file)throw Error('原片不在所引用版本的文件清单中。')
    const response=object(await api.request({mode:'download',content_ref:file.content_ref,access:{world_draft_id:owner.draft_id,world_draft_revision_id:owner.revision_id}},'artifact.read'))
    live(generation)
    const v=object(response.version), fixed=file.content_ref
    if(response.mode!=='download'||v.store_id!==fixed.store_id||v.artifact_id!==fixed.artifact_id||v.version_id!==fixed.version_id||typeof v.mime!=='string'||!Number.isSafeInteger(v.size)||Number(v.size)<=0||typeof v.sha256!=='string'||!/^[a-f0-9]{64}$/.test(v.sha256)||r.sha256!==v.sha256)throw Error('原片与分析证据的固定内容不一致。')
    const mime=v.mime.split(';')[0].trim().toLowerCase()
    if(!['video/mp4','video/webm','audio/wav','audio/mpeg','audio/mp4','audio/ogg'].includes(mime))throw Error('此原片格式暂不支持预览。')
    const blob=await readSampleBytes({path:sampleContentPath(response.content_path),mime,size:Number(v.size),sha256:v.sha256},controller.signal)
    live(generation); const url=URL.createObjectURL(blob);blobs.set(url,blob);return url
  }
  function load(r:ResultReference) {
    const k=key(r);if(!originals.has(k)) { const pending=read(r).catch(e=>{if(originals.get(k)===pending)originals.delete(k);throw e}); originals.set(k,pending) }
    return originals.get(k)!
  }
  async function capture(r:ResultReference,at:number):Promise<string>{
    const generation=epoch.value,url=await load(r);live(generation)
    const blob=await captureReferenceFrame(url,at,controller.signal)
    live(generation);const frame=URL.createObjectURL(blob);blobs.set(frame,blob);return frame
  }

  function frame(r:ResultReference,at:number) { const k=key(r)+':'+at;if(!frames.has(k)){const generation=epoch.value;const pending=frameQueue.then(()=>{live(generation);return capture(r,at)}).catch(e=>{if(frames.get(k)===pending)frames.delete(k);throw e});frames.set(k,pending);frameQueue=pending.catch(()=>undefined)}return frames.get(k)! }
  const service:ReferenceMedia={epoch,active,load,frame,preview:async(url,title)=>{live(epoch.value);const blob=blobs.get(url);if(!blob)throw Error('画面已失效，请重新打开。');await openSamplePreview(appSDK,previewKey,blob,title)}}
  const stop=appSDK.lifecycle.subscribe(event=>{if(event==='hidden'||event==='dispose'){active.value=false;if(event==='dispose')disposed=true;clear()}else if(!revoked&&!disposed){active.value=true;epoch.value++}})
  const stopContext=appSDK.context.subscribe(context=>{if(publicationIdentity(context)!==identity){revoked=true;active.value=false;clear()}})
  onBeforeUnmount(()=>{disposed=true;active.value=false;clear();api.dispose();stop();stopContext()})
  provide(referenceMediaKey,service);return service
}
