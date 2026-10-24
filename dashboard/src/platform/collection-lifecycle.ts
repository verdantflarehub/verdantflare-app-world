import type { AppSDK } from './types.ts'
import type { PublicationStorage } from './world-publication.ts'
import { publicationPrincipal } from './publication-client.ts'
import { createDraftAPI, DraftError } from './draft-client.ts'
import { operationID } from './collection-save.ts'
import { object, readLifecycle, type DraftLifecycle } from './draft-model.ts'
import { isID } from './world-model.ts'

export interface CollectionOperation {
  mode: 'trash' | 'restore'; draft_id: string; operation_id: string; expected_lifecycle_version: number
}
interface PendingOperation { request: CollectionOperation; failure?: string }
export function createCollectionLifecycle(sdk: AppSDK, storage: PublicationStorage) {
  const api = createDraftAPI(sdk)
  const key = 'world.collection-lifecycle.v1:' + JSON.stringify(publicationPrincipal(sdk.context.get()))
  let working = false
  function pending(): PendingOperation | null {
    api.live()
    let raw: string | null
    try { raw = storage.getItem(key) } catch { throw new DraftError('STORAGE_UNAVAILABLE') }
    if (!raw) return null
    const record = object(JSON.parse(raw)), r = object(record.request)
    if (!['trash','restore'].includes(String(r.mode)) || !isID(r.draft_id) || !isID(r.operation_id) || !Number.isSafeInteger(r.expected_lifecycle_version) || Number(r.expected_lifecycle_version) < 1) throw Error('目录操作恢复记录不完整，请保留原记录。')
    return record as unknown as PendingOperation
  }
  function persist(record: PendingOperation) {
    api.live()
    try { storage.setItem(key, JSON.stringify(record)) } catch { throw new DraftError('STORAGE_UNAVAILABLE') }
  }
  async function resume(): Promise<{draft_id: string; lifecycle: DraftLifecycle}> {
    if (working) throw Error('目录操作正在确认中。')
    const record = pending()
    if (!record) throw Error('没有待确认的目录操作。')
    if (record.failure) throw new DraftError(record.failure)
    working = true
    try {
      const r = record.request, result = object(await api.request({...r})), lifecycle = readLifecycle(result.lifecycle)
      if (result.kind !== 'lifecycle' || result.draft_id !== r.draft_id || result.operation_id !== r.operation_id || !lifecycle || lifecycle.version !== r.expected_lifecycle_version + 1 || lifecycle.state !== (r.mode === 'trash' ? 'trashed' : 'active')) throw Error('目录操作回执不一致，原操作编号仍保留。')
      api.live()
      try { storage.removeItem(key) } catch { throw new DraftError('STORAGE_UNAVAILABLE') }
      return {draft_id:r.draft_id,lifecycle}
    } catch (error) {
      if (error instanceof DraftError && ['REVISION_CONFLICT','IDEMPOTENCY_CONFLICT','PERMISSION_DENIED','INVALID_ARGUMENT'].includes(error.code)) {
        record.failure = error.code; persist(record)
      }
      throw error
    } finally { working = false }
  }
  function start(mode: 'trash' | 'restore', draft: string, lifecycle: DraftLifecycle) {
    if (pending() || working) throw Error('请先确认上一次目录操作的结果。')
    if (!isID(draft) || !readLifecycle(lifecycle) || !(mode === 'trash' ? lifecycle.can_trash : lifecycle.can_restore)) throw Error('当前内容不能执行这个操作，请刷新目录。')
    persist({request:{mode,draft_id:draft,operation_id:operationID(),expected_lifecycle_version:lifecycle.version}})
    return resume()
  }
  function releaseFailure() {
    if (!pending()?.failure || working) throw Error('结果尚未明确，请继续确认原操作。')
    api.live(); storage.removeItem(key)
  }
  return {pending, start, resume, releaseFailure, dispose:api.dispose}
}
