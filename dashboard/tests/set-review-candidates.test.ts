import test from 'node:test'
import assert from 'node:assert/strict'
import { loadReviewCandidates } from '../src/platform/set-review-candidates.ts'
import type { ReviewLocator } from '../src/platform/set-review.ts'
import type { AppSDK, Context, Json, Lifecycle } from '../src/platform/types.ts'

const id = (n: number) => `01900000-0000-7000-8000-${String(n).padStart(12, '0')}`
const fixed: ReviewLocator = { kind: 'world', asset_id: id(1), asset_version_id: id(2) }
function fixture() {
  const lifecycle = new Set<(event: Lifecycle) => void>(), contexts = new Set<(value: Context) => void>(), requests: { path: string; body: Json; signal?: AbortSignal }[] = []
  let context: Context = { userId: 'reader', organizationId: 'org', stationId: 'station', appId: 'world', installationId: 'install', contextRevision: 'grant-1', projectId: null }
  const list = { items: [{ asset_id: id(1), head_asset_version_id: id(3), name: '固定县署', asset_type: 'scene', subjects: [], created_at: '2026-10-24T00:00:00Z' }], next_cursor: id(4) }
  let handle: () => Promise<{ status: number; data: Json; requestId: string }> = async () => ({ status: 200, data: list, requestId: 'test' })
  const sdk: AppSDK = { version: '1.1.0', context: { get: () => context, subscribe: fn => { contexts.add(fn); return () => contexts.delete(fn) } }, lifecycle: { subscribe: fn => { lifecycle.add(fn); return () => lifecycle.delete(fn) } }, theme: { get: () => 'light', subscribe: () => () => {} }, navigation: { get: () => ({ path: '/', revision: 1 }), subscribe: () => () => {}, request: async () => {} }, app: { request: async (input, options) => { requests.push({ ...input, signal: options?.signal }); return handle() } }, capabilities: { call: async () => { throw Error('not a candidate read') } } }
  return { sdk, list, requests, lifecycle, contexts, handle: (next: typeof handle) => { handle = next }, emit: (event: Lifecycle) => { for (const fn of lifecycle) fn(event) }, change: () => { context = { ...context, contextRevision: 'grant-2' }; for (const fn of contexts) fn(context) } }
}

test('authorized scene page fixes the returned version without replacing the current old target', async () => {
  const f = fixture(), result = await loadReviewCandidates(f.sdk, fixed)
  assert.deepEqual(f.requests[0].body, { domain: 'scene', limit: 50 })
  assert.equal(result.items[0].title, '固定县署')
  assert.deepEqual(result.items[0].locator, { kind: 'world', asset_id: id(1), asset_version_id: id(3) })
  assert.equal(fixed.asset_version_id, id(2)); assert.equal(result.nextCursor, id(4))
  f.list.items[0].head_asset_version_id = id(9)
  assert.equal(result.items[0].locator.asset_version_id, id(3))
  assert.equal(f.contexts.size, 0); assert.equal(f.lifecycle.size, 0)
})

test('pagination is explicit and rejects nonadvancing cursors', async () => {
  const f = fixture()
  await assert.rejects(loadReviewCandidates(f.sdk, fixed, { cursor: id(4) }), /INVALID_RESPONSE/)
  assert.deepEqual(f.requests[0].body, { domain: 'scene', limit: 50, cursor: id(4) })
  await assert.rejects(loadReviewCandidates(f.sdk, fixed, { cursor: 'latest' }), /INVALID_RESPONSE/)
  assert.equal(f.requests.length, 1)
})

test('empty authorization result stays empty; permission failures do not become empty success', async () => {
  const f = fixture(); f.list.items = []; f.list.next_cursor = ''
  assert.deepEqual(await loadReviewCandidates(f.sdk, fixed), { items: [], nextCursor: '' })
  f.handle(async () => ({ status: 403, data: null, requestId: 'denied' }))
  await assert.rejects(loadReviewCandidates(f.sdk, fixed), error => (error as { status?: number }).status === 403)
  assert.equal(f.contexts.size, 0); assert.equal(f.lifecycle.size, 0)
})

test('foreign domains and mutable version references are rejected', async () => {
  const f = fixture(); f.list.items[0].asset_type = 'character'
  await assert.rejects(loadReviewCandidates(f.sdk, fixed), /INVALID_RESPONSE/)
  f.list.items[0].asset_type = 'scene'; f.list.items[0].head_asset_version_id = 'latest'
  await assert.rejects(loadReviewCandidates(f.sdk, fixed))
})

for (const trigger of ['abort', 'hidden', 'context'] as const) test(`${trigger} cancels a pending catalog read and releases all subscriptions`, async () => {
  const f = fixture(), controller = new AbortController()
  let resolve!: (value: { status: number; data: Json; requestId: string }) => void
  f.handle(() => new Promise(done => { resolve = done }))
  const pending = loadReviewCandidates(f.sdk, fixed, { signal: controller.signal })
  if (trigger === 'abort') controller.abort(); else if (trigger === 'hidden') f.emit('hidden'); else f.change()
  assert.equal(f.requests[0].signal?.aborted, true)
  resolve({ status: 200, data: f.list, requestId: 'late' })
  await assert.rejects(pending, { name: 'AbortError' })
  assert.equal(f.contexts.size, 0); assert.equal(f.lifecycle.size, 0)
})
