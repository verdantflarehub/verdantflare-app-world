import type { HostProps } from './types.ts'
import { createReadClient } from './client.ts'

let props: HostProps | undefined
let client: ReturnType<typeof createReadClient> | undefined

export function connectHost(domain: 'project' | 'world'): HostProps {
  const value = (window as Window & { $wujie?: { props?: HostProps } }).$wujie?.props
  const match = value && /^1\.(\d+)\.\d+$/.exec(value.appSDK?.version || '')
  if (!value || !match || Number(match[1]) < 1 || !value.appSDK.theme?.subscribe || !value.appSDK.navigation?.subscribe || !value.dashboardHost?.ready || !value.dashboardHost?.resize) {
    throw new Error('请使用支持此应用的新版 Studio，从应用中心打开。')
  }
  props = value
  client = createReadClient(value.appSDK, domain)
  return value
}
export function useHost(): HostProps {
  if (!props) throw new Error('请从 Studio 应用中心打开。')
  return props
}
export function read<T>(name: string, args: Record<string, unknown>, projectID?: string): Promise<T> {
  if (!client) return Promise.reject(new Error('请从 Studio 应用中心打开。'))
  return client.call<T>(name, args, projectID)
}
export function disconnectHost() { client?.dispose(); client = undefined; props = undefined }
