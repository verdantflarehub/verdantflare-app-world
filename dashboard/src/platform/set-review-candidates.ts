import { createReadClient } from './client.ts'
import { readAssetList, type AssetItem } from './world-model.ts'
import { canonicalJSON, ReviewError, reviewID, validLocator, type ReviewLocator } from './set-review.ts'
import type { AppSDK } from './types.ts'

export interface ReviewCandidate { key: string; label: string; title?: string; locator: ReviewLocator }
export interface ReviewCandidateOptions { signal?: AbortSignal; cursor?: string }
export interface ReviewCandidatePage { items: ReviewCandidate[]; nextCursor: string }

/** A catalog response fixes each advertised head now; no later implicit head lookup. */
export function worldReviewCandidates(items: readonly AssetItem[]): ReviewCandidate[] {
  return items.map(item => {
    if (item.asset_type !== 'scene') throw new ReviewError('INVALID_RESPONSE')
    const locator: ReviewLocator = { kind: 'world', asset_id: item.asset_id, asset_version_id: item.head_asset_version_id }
    if (!validLocator(locator)) throw new ReviewError('INVALID_RESPONSE')
    return { key: canonicalJSON(locator), title: item.name, label: `${item.name} · 固定版本 ${item.head_asset_version_id}`, locator }
  })
}

export async function loadReviewCandidates(sdk: AppSDK, locator: ReviewLocator, options: ReviewCandidateOptions = {}): Promise<ReviewCandidatePage> {
  if (locator.kind !== 'world' || !validLocator(locator) || options.cursor && !reviewID(options.cursor)) throw new ReviewError('INVALID_RESPONSE')
  if (options.signal?.aborted) throw new DOMException('候选读取已取消。', 'AbortError')
  const initial = canonicalJSON(sdk.context.get()), client = createReadClient(sdk, 'world')
  let invalidated = false
  const cancel = () => { invalidated = true; client.dispose() }
  const stopLife = sdk.lifecycle.subscribe(event => { if (event !== 'active') cancel() })
  const stopContext = sdk.context.subscribe(value => { if (canonicalJSON(value) !== initial) cancel() })
  options.signal?.addEventListener('abort', cancel, { once: true })
  const active = () => { if (invalidated || options.signal?.aborted || canonicalJSON(sdk.context.get()) !== initial) throw new DOMException('候选读取已取消。', 'AbortError') }
  try {
    const data = await client.call<unknown>('world.list', { domain: 'scene', limit: 50, ...(options.cursor ? { cursor: options.cursor } : {}) })
    active()
    const list = readAssetList(data)
    if (list.next_cursor && list.next_cursor === options.cursor) throw new ReviewError('INVALID_RESPONSE')
    return { items: worldReviewCandidates(list.items), nextCursor: list.next_cursor }
  } finally {
    options.signal?.removeEventListener('abort', cancel)
    stopLife(); stopContext(); client.dispose()
  }
}
