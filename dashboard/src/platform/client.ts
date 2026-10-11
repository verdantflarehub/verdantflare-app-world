import type { AppSDK, Context, Json, Unsubscribe } from './types.ts'

export class MCPRequestError extends Error {
  readonly status: number
  constructor(message: string, status: number) { super(message); this.status = status }
}
const identity = (context: Readonly<Context>) => JSON.stringify([context.userId, context.organizationId, context.stationId, context.appId, context.installationId, context.contextRevision])
const cancelled = () => new DOMException('页面已隐藏或会话已失效。', 'AbortError')

/** Application consumer: explicit read operations, using the injected SDK only. */
export function createReadClient(sdk: AppSDK, domain: 'project' | 'world') {
  const context = sdk.context.get()
  if (context.appId !== domain) throw new Error('应用身份不匹配。')
  const initialIdentity = identity(context)
  let disposed = false, hidden = false, generation = 0
  const controllers = new Set<AbortController>()
  const subscriptions: Unsubscribe[] = []
  function cancel() { generation++; for (const controller of controllers) controller.abort(); controllers.clear() }
  function dispose() { if (disposed) return; disposed = true; cancel(); subscriptions.splice(0).forEach(stop => stop()) }
  subscriptions.push(sdk.lifecycle.subscribe(event => {
    if (event === 'dispose') dispose()
    else { hidden = event === 'hidden'; if (hidden) cancel() }
  }))
  subscriptions.push(sdk.context.subscribe(value => { if (identity(value) !== initialIdentity) dispose() }))

  async function call<T>(name: string, args: Record<string, unknown>, _projectID?: string): Promise<T> {
    if (disposed || hidden) throw cancelled()
    const routes: Record<string, string> = domain === 'project'
      ? { 'project.list': '/api/v1/project/list', 'project.open': '/api/v1/project/open' }
      : { 'world.list': '/api/v1/world/list', 'world.get': '/api/v1/world/get' }
    const artifact = domain === 'project' && name === 'artifact.read'
    if (!artifact && !Object.hasOwn(routes, name)) throw new Error('当前页面不支持此操作。')
    if (artifact && !['text', 'metadata', 'download'].includes(String(args.mode))) throw new Error('内容读取方式无效。')
    for (const key of ['organization_id', 'subject_id', 'user_id', 'token', 'endpoint']) {
      if (Object.hasOwn(args, key)) throw new Error('页面不得指定身份或服务地址。')
    }
    const controller = new AbortController(), current = generation
    controllers.add(controller)
    try {
      const body = JSON.parse(JSON.stringify(args)) as Json
      const response = artifact
        ? { status: 200, data: await sdk.capabilities.call('artifact.read', body, { signal: controller.signal }) }
        : await sdk.app.request({ method: 'POST', path: routes[name], body }, { signal: controller.signal })
      if (disposed || hidden || current !== generation || controller.signal.aborted) throw cancelled()
      if (!Number.isInteger(response.status) || response.status < 200 || response.status >= 300) {
        throw new MCPRequestError(response.status === 403 ? '当前身份没有访问权限。' : response.status === 401 ? '当前会话已失效。' : '服务暂时不可用，请重试。', response.status)
      }
      return response.data as T
    } finally { controllers.delete(controller) }
  }
  return { call, dispose }
}
