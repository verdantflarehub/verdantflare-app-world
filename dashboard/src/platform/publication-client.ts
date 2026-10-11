import type { AppSDK, Context, Json, Unsubscribe } from './types.ts'

export class PublicationError extends Error { constructor(readonly code: string) { super(code) } }
export const publicationIdentity = (c: Readonly<Context>) => JSON.stringify([c.userId, c.organizationId, c.stationId, c.appId, c.installationId, c.contextRevision])
export const publicationPrincipal = (c: Readonly<Context>) => [c.userId, c.organizationId, c.stationId, c.appId, c.installationId]
export function publicationError(error: unknown): string {
  const code = error instanceof PublicationError ? error.code : ''
  return ({
    PERMISSION_DENIED: '当前身份没有执行本次入库或保存项目关联的权限。',
    NOT_READY: '入库结果尚未确认，请保留原操作，稍后查询或恢复。',
    NOT_FOUND: '尚未查到原操作或固定内容，请保留记录并核对来源。',
    REVISION_CONFLICT: '项目已更新，请核对原操作后恢复关联。',
    REQUEST_MISMATCH: '原操作、固定来源或请求摘要不一致，已停止处理。',
    IDEMPOTENCY_CONFLICT: '原操作编号对应不同内容，已停止处理。',
    STORAGE_UNAVAILABLE: '无法保存恢复记录，本次没有发起新的写入。',
    SESSION_REVOKED: '会话已变化，请重新打开应用。原操作恢复记录仍保留。',
    DASHBOARD_HIDDEN: '页面已隐藏，请返回页面后恢复原操作。',
    INVALID_RESPONSE: '返回内容或恢复记录不完整，已保留原操作。',
    TERMINAL_FAILURE: '原操作已失败或冲突，请核对记录，不能直接创建新的入库操作。',
  } as Record<string, string>)[code] || '暂时无法确认结果，请保留记录并恢复原操作。'
}
/** Only this application's publication API; accepted writes survive page hiding. */
export function createPublicationAPI(sdk: AppSDK, domain: 'project' | 'world') {
  const initial = publicationIdentity(sdk.context.get())
  if (sdk.context.get().appId !== domain) throw new PublicationError('SESSION_REVOKED')
  let disposed = false, hidden = false
  const pending = new Map<AbortController, boolean>(), stops: Unsubscribe[] = []
  const allowed = domain === 'world'
    ? new Set(['publication_source', 'register', 'commit_status'])
    : new Set(['publication_prepare', 'publication_associate', 'publication_receipt', 'commit_status', 'open'])
  const writes = new Set(['register', 'publication_prepare', 'publication_associate'])
  function live() { if (disposed || publicationIdentity(sdk.context.get()) !== initial) throw new PublicationError('SESSION_REVOKED') }
  function dispose() { if (disposed) return; disposed = true; pending.forEach((_read, controller) => controller.abort()); pending.clear(); stops.splice(0).forEach(stop => stop()) }
  stops.push(sdk.context.subscribe(() => { if (publicationIdentity(sdk.context.get()) !== initial) dispose() }))
  stops.push(sdk.lifecycle.subscribe(event => { if (event === 'dispose') dispose(); else { hidden = event === 'hidden'; if (hidden) pending.forEach((read, controller) => { if (read) controller.abort() }) } }))
  async function call<T>(operation: string, input: unknown): Promise<T> {
    live(); if (hidden) throw new PublicationError('DASHBOARD_HIDDEN')
    if (!allowed.has(operation)) throw new PublicationError('PERMISSION_DENIED')
    const controller = new AbortController(); pending.set(controller, !writes.has(operation))
    try {
      const body = JSON.parse(JSON.stringify(input)) as Json
      const response = await sdk.app.request({ method: 'POST', path: `/api/v1/${domain}/${operation}`, body }, { signal: controller.signal })
      live(); if (controller.signal.aborted) throw new PublicationError('DASHBOARD_HIDDEN')
      if (response.status !== 200) {
        const code = response.data && typeof response.data === 'object' && !Array.isArray(response.data) ? response.data.code : null
        throw new PublicationError(typeof code === 'string' ? code : 'DEPENDENCY_UNAVAILABLE')
      }
      return response.data as T
    } finally { pending.delete(controller) }
  }
  return { call, live, dispose }
}
