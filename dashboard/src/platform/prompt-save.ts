import type { AppSDK } from './types.ts'
import { publicationPrincipal } from './publication-client.ts'
import { canonical, type PublicationStorage } from './world-publication.ts'
import { createDraftAPI, DraftError } from './draft-client.ts'
import { object, contentRef, readDraft, type DraftRevision } from './draft-model.ts'
import { readAsset, isID, type AssetOpen } from './world-model.ts'
import { readLearningResults } from './learning-results.ts'
import { operationID } from './collection-save.ts'
import type { PromptCopyPlan } from './prompt-copy.ts'
interface RecordV1 {version:1;plan:PromptCopyPlan;create_id:string;write_id:string;commit_id:string;register_id:string;file_id:string;base?:DraftRevision;saved?:DraftRevision;asset?:AssetOpen;registration_started?:boolean;failure?:string;failure_stage?:'commit'|'register'}
const clone=<T>(v:T):T=>JSON.parse(JSON.stringify(v)) as T
export function createPromptSave(sdk:AppSDK,storage:PublicationStorage){
 const api=createDraftAPI(sdk),key='world.prompt-save.v1:'+JSON.stringify(publicationPrincipal(sdk.context.get()))
 let busy=false
 function read():RecordV1|null{
  api.live();let raw:string|null
  try{raw=storage.getItem(key)}catch{throw new DraftError('STORAGE_UNAVAILABLE')}
  if(!raw)return null
  const r=object(JSON.parse(raw))
  if(r.version!==1||!['create_id','write_id','commit_id','register_id','file_id'].every(k=>isID(r[k])))throw Error('另存恢复记录不完整，原记录仍保留。')
  const plan=object(r.plan);readLearningResults(JSON.stringify(plan.document))
  if(typeof plan.name!=='string'||!plan.name.trim()||plan.name.length>256||!Array.isArray(plan.subjects)||!Array.isArray(plan.source_refs)||!Array.isArray(plan.asset_refs))throw Error('另存恢复数据不完整。')
  if(r.base)readDraft(r.base,undefined,undefined,'creation')
  if(r.saved)readDraft(r.saved,undefined,undefined,'creation')
  if(r.asset){const a=object(r.asset);readAsset(a,String(a.asset_id),String(a.asset_version_id))}
  return r as unknown as RecordV1
 }
 function persist(r:RecordV1){api.live();try{storage.setItem(key,JSON.stringify(r))}catch{throw new DraftError('STORAGE_UNAVAILABLE')}}
 function checkAsset(raw:unknown,r:RecordV1):AssetOpen{
  const value=object(raw),a=readAsset(value,String(value.asset_id),String(value.asset_version_id)),m=a.manifest,source=m.source,saved=r.saved!
  if(source.kind!=='world_draft'||source.draft_id!==saved.draft_id||source.draft_revision_id!==saved.revision_id||source.relation!=='curated_in'||m.asset_type!=='prompt'||m.name!==r.plan.name||canonical(m.subjects)!==canonical(r.plan.subjects)||canonical(m.files)!==canonical(saved.manifest.files)||canonical(m.domain_documents)!==canonical(saved.manifest.domain_documents)||canonical(m.depends_on)!==canonical(r.plan.asset_refs))throw Error('另存资产与已确认的工作副本不一致。')
  return a
 }
 async function resume():Promise<AssetOpen>{
  if(busy)throw Error('正在处理原另存操作。');busy=true
  try{
   const r=read();if(!r)throw Error('没有待恢复的另存操作。')
   if(r.failure)throw new DraftError(r.failure)
   if(!r.base){r.base=readDraft(await api.request({mode:'create',commit_id:r.create_id,purpose:'creation',name:r.plan.name,subjects:r.plan.subjects}),undefined,undefined,'creation');persist(r)}
   if(!r.saved){
    const text=JSON.stringify(r.plan.document),bytes=new TextEncoder().encode(text)
    const reply=object(await api.request({mode:'text',write_id:r.write_id,source:{kind:'user_edit',world_draft_id:r.base.draft_id},mime:'application/json',text},'artifact.write'))
    const version=object(reply.version),ref=contentRef(version),source=object(version.source)
    const hash=[...new Uint8Array(await crypto.subtle.digest('SHA-256',bytes))].map(b=>b.toString(16).padStart(2,'0')).join('')
    if(version.size!==bytes.length||version.sha256!==hash||version.mime!=='application/json'||source.kind!=='user_edit'||source.world_draft_id!==r.base.draft_id||source.project_id)throw Error('另存正文的回执与实际内容不一致。')
    const manifest={...clone(r.base.manifest),name:r.plan.name,subjects:r.plan.subjects,files:[{file_id:r.file_id,path:'prompt.json',role:'prompt',content_ref:ref}],domain_documents:[{document_type:'reference-learning',file_id:r.file_id}],source_refs:r.plan.source_refs,asset_refs:r.plan.asset_refs,run_refs:[]}
    try{
     const saved=readDraft(await api.request({mode:'commit',commit_id:r.commit_id,draft_id:r.base.draft_id,expected_revision_id:r.base.revision_id,manifest}),r.base.draft_id,undefined,'creation')
     if(canonical(saved.manifest)!==canonical(manifest))throw Error('另存工作副本清单不一致。')
     r.saved=saved;persist(r)
    }catch(e){
     try{const status=object(await api.request({mode:'status',commit_id:r.commit_id}));if(status.kind==='status'&&['failed','conflict'].includes(String(status.state))){r.failure=String(status.error_code||'REVISION_CONFLICT');r.failure_stage='commit';persist(r)}}catch{}
     throw e
    }
   }
   // Fixed readback precedes registration. An uncertain registration never starts
   // a second draft, and new assets never supply a source asset ID to overwrite.
   if(!r.asset&&r.registration_started){
    let status:Record<string,unknown>|undefined
    try{status=object(await api.assetRequest('status',{commit_id:r.register_id}))}catch(e){if(!(e instanceof DraftError&&e.code==='NOT_FOUND'))throw e}
    if(status?.state==='committed'){r.asset=checkAsset(status.result,r);persist(r)}
    else if(status&&['failed','conflict'].includes(String(status.state))){r.failure=String(status.error_code||'REGISTRATION_FAILED');r.failure_stage='register';persist(r);throw new DraftError(r.failure)}
    else if(status&&status.state!=='preparing')throw Error('登记状态尚无法确认，原操作仍保留。')
   }
   if(!r.asset){
   const saved=readDraft(await api.request({mode:'get',draft_id:r.saved.draft_id,revision_id:r.saved.revision_id}),r.saved.draft_id,r.saved.revision_id,'creation')
   if(canonical(saved.manifest)!==canonical(r.saved.manifest))throw Error('工作副本固定回读不一致。')
    r.registration_started=true;persist(r)
    try{
     r.asset=checkAsset(await api.assetRequest('register',{commit_id:r.register_id,source_draft_id:saved.draft_id,source_revision_id:saved.revision_id,file_ids:[r.file_id],name:r.plan.name,asset_type:'prompt',subjects:r.plan.subjects,relation:'curated_in',depends_on:r.plan.asset_refs}),r)
     persist(r)
    }catch(e){
     try{const status=object(await api.assetRequest('status',{commit_id:r.register_id}));if(status.state==='committed'&&status.result){r.asset=checkAsset(status.result,r);persist(r)}else if(['failed','conflict'].includes(String(status.state))){r.failure=String(status.error_code||'REGISTRATION_FAILED');r.failure_stage='register';persist(r)}}catch{}
     throw e
    }
   }
   const asset=checkAsset(await api.assetRequest('get',{asset_id:r.asset.asset_id,asset_version_id:r.asset.asset_version_id}),r)
   if(asset.asset_id!==r.asset.asset_id||asset.asset_version_id!==r.asset.asset_version_id||canonical(asset.manifest)!==canonical(r.asset.manifest))throw Error('新资产固定回读不一致。')
   api.live();try{storage.removeItem(key)}catch{throw new DraftError('STORAGE_UNAVAILABLE')}
   return asset
  }finally{busy=false}
 }
 function start(plan:PromptCopyPlan){
  if(busy||read())return Promise.reject(Error('请先恢复已有的另存操作。'))
  readLearningResults(JSON.stringify(plan.document))
  persist({version:1,plan:clone(plan),create_id:operationID(),write_id:operationID(),commit_id:operationID(),register_id:operationID(),file_id:operationID()})
  return resume()
 }
 async function retryRegistration():Promise<AssetOpen>{
  const r=read();if(!r?.failure||r.failure_stage!=='register'||!r.saved||busy)throw Error('只能重试已明确失败的登记；原工作副本仍保留。')
  r.register_id=operationID();r.registration_started=false;delete r.failure;delete r.failure_stage;persist(r)
  return resume()
 }
 async function retryCommit(plan:PromptCopyPlan):Promise<AssetOpen>{
  const r=read();if(!r?.failure||r.failure_stage!=='commit'||!r.base||r.saved||busy)throw Error('只能修改已明确失败且尚未提交的工作副本。')
  busy=true
  try{
   const latest=readDraft(await api.request({mode:'get',draft_id:r.base.draft_id}),r.base.draft_id,undefined,'creation')
   if(latest.revision_id!==r.base.revision_id||canonical(latest.manifest)!==canonical(r.base.manifest))throw Error('工作副本已有其他修改，不能覆盖；原修改和恢复记录仍保留。')
   readLearningResults(JSON.stringify(plan.document));r.plan=clone(plan);r.write_id=operationID();r.commit_id=operationID();delete r.failure;delete r.failure_stage;persist(r)
  }finally{busy=false}
  return resume()
 }
 return {read,start,resume,retryRegistration,retryCommit,dispose:api.dispose}
}
