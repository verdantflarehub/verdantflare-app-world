import type { AppSDK, Json } from './types.ts'
import { publicationIdentity } from './publication-client.ts'
import { object } from './draft-model.ts'
export class DraftError extends Error { constructor(readonly code: string) { super(code) } }
export function draftError(error: unknown): string {
  const code = error instanceof DraftError ? error.code : error && typeof error === 'object' && 'code' in error ? String(error.code) : ''
  return ({ REQUEST_TOO_LARGE:'本次请求超过当前页面的 1 MiB 上限，内容未提交。请保留原输入，使用支持该大小的 MCP 入口处理。', PERMISSION_DENIED:'没有读取或保存这份内容的权限。', CONTENT_DENIED:'没有读取或保存这份内容的权限。', NOT_FOUND:'没有找到这份内容或原保存操作。', REVISION_CONFLICT:'内容已被更新。你的修改仍保留，请先核对最新版本。', IDEMPOTENCY_CONFLICT:'恢复编号对应的内容不一致，已停止保存。', CONTENT_NOT_READY:'内容尚未就绪，原操作仍保留。', SESSION_REVOKED:'会话已变化，请重新打开数字世界。原保存记录仍保留。', STORAGE_UNAVAILABLE:'无法保存恢复记录，本次没有发起新写入。', DASHBOARD_HIDDEN:'返回页面后可恢复原操作。', CAPABILITY_DENIED:'当前宿主尚未开放所需内容能力。', CAPABILITY_UNAVAILABLE:'当前宿主尚未开放所需内容能力。' } as Record<string,string>)[code] || (error instanceof Error && !code ? error.message : '暂时无法确认结果，请恢复原保存操作。')
}
export function createDraftAPI(sdk: AppSDK) {
  const identity = publicationIdentity(sdk.context.get())
  if (sdk.context.get().appId !== 'world') throw new DraftError('SESSION_REVOKED')
  let disposed = false, hidden = false
  const controllers = new Map<AbortController, boolean>()
  const stops: (() => void)[] = []
  function live() { if (disposed || publicationIdentity(sdk.context.get()) !== identity) throw new DraftError('SESSION_REVOKED') }
  function dispose() { if (disposed) return; disposed = true; controllers.forEach((_read,c) => c.abort()); controllers.clear(); stops.splice(0).forEach(s => s()) }
  stops.push(sdk.context.subscribe(() => { if (publicationIdentity(sdk.context.get()) !== identity) dispose() }))
  stops.push(sdk.lifecycle.subscribe(event => { if (event === 'dispose') dispose(); else { hidden = event === 'hidden'; if (hidden) controllers.forEach((read,c) => { if (read) c.abort() }) } }))
  async function request(input: Record<string, unknown>, capability?: 'artifact.read' | 'artifact.write', assetMode?: 'register' | 'status' | 'get'): Promise<unknown> {
    live(); if (hidden) throw new DraftError('DASHBOARD_HIDDEN')
    if (!capability && !assetMode && !['list','get','status','create','commit'].includes(String(input.mode))) throw new DraftError('INVALID_ARGUMENT')
    if (['organization_id','user_id','subject_id','token','endpoint','project_id'].some(k => k in input)) throw new DraftError('INVALID_ARGUMENT')
    if (assetMode && !['register','status','get'].includes(assetMode)) throw new DraftError('INVALID_ARGUMENT')
    const read = capability ? capability === 'artifact.read' : assetMode ? assetMode !== 'register' : ['list','get','status'].includes(String(input.mode))
    const controller = new AbortController(); controllers.set(controller,read)
    try {
      const serialized = JSON.stringify(input)
      if (!capability && new TextEncoder().encode(serialized).byteLength > 1024*1024) throw new DraftError('REQUEST_TOO_LARGE')
      const body = JSON.parse(serialized) as Json
      const reply = capability ? { status:200, data:await sdk.capabilities.call(capability,body,{signal:controller.signal}) }
        : await sdk.app.request({method:'POST',path:assetMode ? '/api/v1/world/'+(assetMode==='status'?'commit_status':assetMode) : '/api/v1/world/draft',body},{signal:controller.signal})
      live(); if (controller.signal.aborted || hidden && read) throw new DraftError('DASHBOARD_HIDDEN')
      if (reply.status !== 200) { let code='DEPENDENCY_UNAVAILABLE'; try { code=String(object(reply.data).code || code) } catch {} throw new DraftError(code) }
      return reply.data
    } finally { controllers.delete(controller) }
  }
  return { request:(input:Record<string,unknown>,capability?:'artifact.read'|'artifact.write')=>request(input,capability),
    assetRequest:(mode:'register'|'status'|'get',input:Record<string,unknown>)=>request(input,undefined,mode),dispose,live }
}
