// Consumer declarations for the central AppSDK contract; no SDK implementation.
// Canonical API: docs/design/contracts/app-dashboard-v1.md in the design repository.
export type Json = null | boolean | number | string | Json[] | { [key: string]: Json }
export type Lifecycle = 'active' | 'hidden' | 'dispose'
export type Unsubscribe = () => void
export interface Context { userId: string; organizationId: string; stationId: string; projectId: string | null; appId: string; installationId: string; contextRevision: string }
export interface AppSDK {
  readonly version: string
  readonly context: { get(): Readonly<Context>; subscribe(listener: (value: Readonly<Context>) => void): Unsubscribe }
  readonly lifecycle: { subscribe(listener: (value: Lifecycle) => void): Unsubscribe }
  readonly theme: { get(): 'light' | 'dark'; subscribe(listener: (theme: 'light' | 'dark') => void): Unsubscribe }
  readonly navigation: {
    get(): Readonly<{ path: string; revision: number }>
    subscribe(listener: (route: Readonly<{ path: string; revision: number }>) => void): Unsubscribe
    request(input: { path: string; target: 'app' | 'appcenter' }): Promise<void>
  }
  readonly capabilities: { call(name: string, input: Json, options?: { signal?: AbortSignal }): Promise<Json> }
  readonly app: { request(input: { path: string; method: 'POST'; body: Json }, options?: { signal?: AbortSignal }): Promise<{ status: number; data: Json; requestId: string }> }
}
export interface DashboardHost {
  ready(): void
  setDirty(value: boolean): void
  resize(height: number): void
  getScroll(): number
  restoreScroll(top: number): void
}
export interface HostProps { appSDK: AppSDK; dashboardHost: DashboardHost }
