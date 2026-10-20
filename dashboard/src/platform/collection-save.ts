import type { AppSDK } from './types.ts'
import { canonical, type PublicationStorage } from './world-publication.ts'
import { publicationPrincipal } from './publication-client.ts'
import { createDraftAPI, DraftError } from './draft-client.ts'
import { collectionForm, contentRef, makeCollectionDocument, object, readDraft, type CollectionForm, type DraftRevision, type DraftManifest } from './draft-model.ts'
import { isID } from './world-model.ts'

export function operationID(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(16)); let time = Date.now()
  for (let i=5;i>=0;i--) { bytes[i]=time%256; time=Math.floor(time/256) }
  bytes[6]=(bytes[6]&15)|112; bytes[8]=(bytes[8]&63)|128
  const h=[...bytes].map(b=>b.toString(16).padStart(2,'0')).join('')
  return `${h.slice(0,8)}-${h.slice(8,12)}-${h.slice(12,16)}-${h.slice(16,20)}-${h.slice(20)}`
}
interface SaveRecord {
  version: 1; form: CollectionForm; create_id: string; write_id: string; commit_id: string; file_id: string
  base?: DraftRevision; result?: DraftRevision; failure?: 'failed' | 'conflict'
}
const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v)) as T
export function createCollectionSave(sdk: AppSDK, storage: PublicationStorage) {
  const api=createDraftAPI(sdk)
  const key='world.collection-save.v1:'+JSON.stringify(publicationPrincipal(sdk.context.get()))
  let working=false
  function read(): SaveRecord | null {
    api.live()
    let raw: string | null
    try { raw=storage.getItem(key) } catch { throw new DraftError('STORAGE_UNAVAILABLE') }
    if (!raw) return null
    const data=object(JSON.parse(raw))
    if (data.version!==1 || !['create_id','write_id','commit_id','file_id'].every(k=>isID(data[k]))) throw Error('恢复记录不完整；原记录没有覆盖。')
    collectionForm(data.form as unknown as CollectionForm)
    if (data.base) readDraft(data.base,undefined,undefined,'collection')
    if (data.result) readDraft(data.result,undefined,undefined,'collection')
    if (data.failure && !['failed','conflict'].includes(String(data.failure))) throw Error('恢复状态不正确。')
    return data as unknown as SaveRecord
  }
  function persist(record: SaveRecord) { api.live(); try { storage.setItem(key,JSON.stringify(record)) } catch { throw new DraftError('STORAGE_UNAVAILABLE') } }
  async function resume(): Promise<DraftRevision> {
    if (working) throw Error('已有保存正在处理。')
    working=true
    try {
      const record=read(); if (!record) throw Error('没有待恢复的保存。')
      if (record.failure) throw new DraftError(record.failure==='conflict'?'REVISION_CONFLICT':'TERMINAL_FAILURE')
      if (!record.base) {
        record.base=readDraft(await api.request({mode:'create',commit_id:record.create_id,name:record.form.name,purpose:'collection',subjects:[]}),undefined,undefined,'collection')
        persist(record)
      }
      const base=record.base
      if (!record.result) {
        const text=JSON.stringify(makeCollectionDocument(record.form))
        const raw=object(await api.request({mode:'text',write_id:record.write_id,source:{kind:'user_edit',world_draft_id:base.draft_id},mime:'application/json',text},'artifact.write'))
        const version=object(raw.version), ref=contentRef(version), source=object(version.source)
        const hash=await crypto.subtle.digest('SHA-256',new TextEncoder().encode(text))
        const expected=[...new Uint8Array(hash)].map(b=>b.toString(16).padStart(2,'0')).join('')
        if (version.size!==new TextEncoder().encode(text).length || version.sha256!==expected || version.mime!=='application/json' || source.kind!=='user_edit' || source.world_draft_id!==base.draft_id || source.project_id) throw Error('保存返回的正文身份或摘要不一致。')
        const manifest: DraftManifest=clone(base.manifest)
        manifest.name=record.form.name
        const prior=manifest.files.findIndex(f=>f.file_id===record.file_id)
        const file={file_id:record.file_id,path:prior>=0?manifest.files[prior].path:'collection.json',role:prior>=0?manifest.files[prior].role:'reference',content_ref:ref}
        if (prior>=0) manifest.files[prior]=file; else manifest.files.push(file)
        manifest.domain_documents=manifest.domain_documents.filter(d=>d.document_type!=='reference-learning')
        manifest.domain_documents.push({document_type:'reference-learning',file_id:record.file_id})
        const request={mode:'commit',commit_id:record.commit_id,draft_id:base.draft_id,expected_revision_id:base.revision_id,manifest}
        try {
          record.result=readDraft(await api.request(request),base.draft_id,undefined,'collection')
          if (canonical(record.result.manifest)!==canonical(manifest)) throw Error('保存返回的清单与原操作不一致。')
          persist(record)
        } catch (error) {
          // Only an authoritative terminal status permits editing a new intent.
          // A timeout, NOT_FOUND or unavailable status never means it was not saved.
          try {
            const status=object(await api.request({mode:'status',commit_id:record.commit_id}))
            if (status.kind==='status' && ['failed','conflict'].includes(String(status.state))) { record.failure=status.state as 'failed'|'conflict'; persist(record) }
          } catch { /* keep the same IDs and source */ }
          throw error
        }
      }
      const saved=readDraft(await api.request({mode:'get',draft_id:record.result.draft_id,revision_id:record.result.revision_id}),record.result.draft_id,record.result.revision_id,'collection')
      if (canonical(saved.manifest)!==canonical(record.result.manifest)) throw Error('回读内容与已保存修订不一致。')
      try { storage.removeItem(key) } catch { throw new DraftError('STORAGE_UNAVAILABLE') }
      return saved
    } finally { working=false }
  }
  function start(input: CollectionForm, base?: DraftRevision): Promise<DraftRevision> {
    if (working || read()) return Promise.reject(Error('请先恢复已有保存，避免重复创建。'))
    const form=collectionForm(input)
    if (base && (base.revision_id!==base.head_revision_id || base.manifest.purpose!=='collection')) return Promise.reject(new DraftError('REVISION_CONFLICT'))
    const file=base?.manifest.domain_documents.find(d=>d.document_type==='reference-learning')?.file_id
    persist({version:1,form,create_id:operationID(),write_id:operationID(),commit_id:operationID(),file_id:file||operationID(),...(base?{base:clone(base)}:{})})
    return resume()
  }
  function releaseFailed(): CollectionForm {
    const record=read(); if (!record?.failure || working) throw Error('尚未确认原操作失败，不能另建保存。')
    try { storage.removeItem(key) } catch { throw new DraftError('STORAGE_UNAVAILABLE') }
    return record.form
  }
  return { read, start, resume, releaseFailed, dispose:api.dispose }
}
