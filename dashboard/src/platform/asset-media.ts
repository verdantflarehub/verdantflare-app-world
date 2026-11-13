import type { AssetFile } from './world-model'

type Call = (name: string, args: Record<string, unknown>) => Promise<unknown>
export interface AssetScope { asset_id: string; asset_version_id: string }

// Download paths are cached per fixed version and file; versions are immutable,
// so a cached path never points at different bytes.
const caches = new WeakMap<Call, Map<string, Promise<string>>>()
const queue: Array<() => void> = []
let running = 0
const LIMIT = 6

function slot<T>(work: () => Promise<T>): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const run = () => { running++; work().then(resolve, reject).finally(() => { running--; queue.shift()?.() }) }
    if (running < LIMIT) run(); else queue.push(run)
  })
}

/** Resolve a controlled same-origin path for one file of a fixed asset version. */
export function assetFileURL(call: Call, scope: AssetScope, file: AssetFile, refresh = false): Promise<string> {
  let cache = caches.get(call)
  if (!cache) { cache = new Map(); caches.set(call, cache) }
  const key = `${scope.asset_id}:${scope.asset_version_id}:${file.file_id}`
  if (refresh) cache.delete(key)
  let hit = cache.get(key)
  if (!hit) {
    hit = slot(async () => {
      const res = await call('artifact.read', { mode: 'download', representation: 'original-v1', content_ref: file.content_ref, access: { asset_id: scope.asset_id, asset_version_id: scope.asset_version_id } }) as { content_path?: unknown }
      const path = res?.content_path
      if (typeof path !== 'string' || !/^\/studio\/media\/[a-f0-9]{64}\/[a-f0-9]{64}\/original-v1$/.test(path)) throw new Error('资产图片地址无效。')
      return path
    })
    const pending = hit, entries = cache
    hit.catch(() => { if (entries.get(key) === pending) entries.delete(key) })
    cache.set(key, hit)
  }
  return hit
}

/** Read a small UTF-8 file (such as a domain JSON) of a fixed asset version. */
export async function assetFileText(call: Call, scope: AssetScope, file: AssetFile): Promise<string> {
  const res = await call('artifact.read', { mode: 'text', content_ref: file.content_ref, access: { asset_id: scope.asset_id, asset_version_id: scope.asset_version_id } }) as { text?: unknown }
  if (typeof res?.text !== 'string') throw new Error('资产领域文件无法读取。')
  return res.text
}
