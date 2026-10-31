import type { AssetFile } from './world-model'
import { controlledReviewPath } from './set-review'

type Call = (name: string, args: Record<string, unknown>) => Promise<unknown>
export interface AssetScope { asset_id: string; asset_version_id: string }

// Download paths are cached per fixed version and file; versions are immutable,
// so a cached path never points at different bytes.
const cache = new Map<string, Promise<string>>()
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
export function assetFileURL(call: Call, scope: AssetScope, file: AssetFile): Promise<string> {
  const key = `${scope.asset_version_id}:${file.file_id}`
  let hit = cache.get(key)
  if (!hit) {
    hit = slot(async () => {
      const res = await call('artifact.read', { mode: 'download', content_ref: file.content_ref, access: { asset_id: scope.asset_id, asset_version_id: scope.asset_version_id } }) as { content_path?: unknown }
      return controlledReviewPath(res?.content_path)
    })
    hit.catch(() => cache.delete(key))
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
