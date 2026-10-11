import test from 'node:test'
import assert from 'node:assert/strict'
import { createReadClient, MCPRequestError } from '../src/platform/client.ts'
import type { AppSDK, Context, Json, Lifecycle } from '../src/platform/types.ts'

const domain = 'world' as const
function fixture() {
  const lifecycle = new Set<(event: Lifecycle) => void>(), contexts = new Set<(context: Context) => void>()
  let context: Context = { userId: 'user-a', organizationId: 'org-a', stationId: 'station-a', projectId: null, appId: domain, installationId: 'installed-a', contextRevision: 'grant-a' }
  const requests: Array<{ path: string; body: Json; signal?: AbortSignal }> = []
  let handler = async () => ({ status: 200, data: { items: [] } as Json, requestId: 'request-a' })
  const sdk: AppSDK = {
    version: '1.1.0',
    context: { get: () => context, subscribe: fn => { contexts.add(fn); return () => contexts.delete(fn) } },
    lifecycle: { subscribe: fn => { lifecycle.add(fn); return () => lifecycle.delete(fn) } },
    theme: { get: () => 'light', subscribe: () => () => {} },
    navigation: { get: () => ({ path: '/', revision: 1 }), subscribe: () => () => {}, request: async () => {} },
    capabilities: { call: async () => { throw new Error('Capability unavailable') } },
    app: { request: async (input, options) => { requests.push({ path: input.path, body: input.body, signal: options?.signal }); return handler() } },
  }
  return { sdk, requests, lifecycle, contexts, emit: (event: Lifecycle) => { for (const fn of lifecycle) fn(event) },
    handler: (next: typeof handler) => { handler = next },
    changeScope: () => { context = { ...context, organizationId: 'org-b' }; for (const fn of contexts) fn(context) },
  }
}

test('reads only the current application API and rejects a foreign domain or write', async () => {
  const f = fixture(), client = createReadClient(f.sdk, domain)
  await client.call(domain + '.list', { limit: 50 })
  assert.equal(f.requests[0].path, '/api/v1/' + domain + '/list')
  await assert.rejects(client.call('network.delete', {}))
  await assert.rejects(client.call(domain + '.commit', {}))
  await assert.rejects(client.call('project.list', { organization_id: 'spoofed' }))
  assert.equal(f.requests.length, 1)
  client.dispose()
  assert.equal(f.lifecycle.size, 0)
  assert.equal(f.contexts.size, 0)
})

test('hidden pages abort reads and reject even an upstream that ignores cancellation', async () => {
  const f = fixture(), client = createReadClient(f.sdk, domain)
  let resolve!: (value: { status: number; data: Json; requestId: string }) => void
  f.handler(() => new Promise(done => { resolve = done }))
  const pending = client.call(domain + '.list', {})
  f.emit('hidden')
  assert.equal(f.requests[0].signal?.aborted, true)
  resolve({ status: 200, data: { items: [] }, requestId: 'late' })
  await assert.rejects(pending, { name: 'AbortError' })
  await assert.rejects(client.call(domain + '.list', {}), { name: 'AbortError' })
  f.emit('active')
  f.handler(async () => ({ status: 200, data: { items: [] }, requestId: 'new' }))
  assert.deepEqual(await client.call(domain + '.list', {}), { items: [] })
  client.dispose()
})

test('authority scope changes revoke the consumer and stale results cannot cross it', async () => {
  const f = fixture(), client = createReadClient(f.sdk, domain)
  let resolve!: (value: { status: number; data: Json; requestId: string }) => void
  f.handler(() => new Promise(done => { resolve = done }))
  const pending = client.call(domain + '.list', {})
  f.changeScope()
  resolve({ status: 200, data: { items: [] }, requestId: 'old-org' })
  await assert.rejects(pending, { name: 'AbortError' })
  await assert.rejects(client.call(domain + '.list', {}), { name: 'AbortError' })
  assert.equal(f.lifecycle.size, 0)
})

test('permission and service failures remain failures, not empty successful data', async () => {
  const f = fixture(), client = createReadClient(f.sdk, domain)
  for (const status of [401, 403, 503]) {
    f.handler(async () => ({ status, data: null, requestId: 'failure' }))
    await assert.rejects(client.call(domain + '.list', {}), error => error instanceof MCPRequestError && error.status === status)
  }
  client.dispose()
})

test('a different installation app cannot bind this consumer', () => {
  const f = fixture()
  // Keep the alternate identity independent of the fixture getter.
  f.sdk.context.get = () => ({ userId: 'user', organizationId: 'org', stationId: 'station', projectId: null, appId: 'foreign', installationId: 'foreign', contextRevision: 'v1' })
  assert.throws(() => createReadClient(f.sdk, domain), /身份/)
})
