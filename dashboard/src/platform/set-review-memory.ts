import type { Context } from './types.ts'
interface ReviewContext { path: string; top: number; evidence?: string; camera?: string; work?: string; label?: string }
const entries = new Map<string, ReviewContext>()
export function reviewContextKey(c: Readonly<Context>, owner: string) { return JSON.stringify([c.userId,c.organizationId,c.stationId,c.installationId,owner]) }
export function rememberReviewContext(key: string, path: string, top: number, evidence?: string, camera?: string, work?: string, label?: string) {
  if (path && (!path.startsWith('/') || path.startsWith('//') || /[\\#]/.test(path))) return
  entries.delete(key); entries.set(key, { path, top: Math.max(0, top), evidence, camera, work, label })
  while (entries.size > 100) entries.delete(entries.keys().next().value!)
}
export function reviewContext(key: string) { return entries.get(key) }
