import test from 'node:test'
import assert from 'node:assert/strict'
import { createWorldPublication, type PublicationPointer } from '../src/platform/world-publication.ts'
import { PublicationError } from '../src/platform/publication-client.ts'
import { canonicalRoute } from '../src/platform/route-links.ts'
import type { AppSDK, Context, Json, Lifecycle } from '../src/platform/types.ts'
const id = (n: number) => `01900000-0000-7000-8000-${String(n).padStart(12, '0')}`
const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v)) as T
function fixture() {
  const pointer: PublicationPointer = { project_id: id(1), prepared_revision_id: id(3), candidate_key: 'hall', commit_id: id(4) }
  const request = { commit_id: id(4), source_project_id: id(1), source_revision_id: id(2), file_ids: [id(10)], name: 'Hall', asset_type: 'location', subjects: ['Hall'], relation: 'produced_in', depends_on: [] }
  const preview = { ...pointer, request, request_sha256: 'a'.repeat(64), versions: [{ version_key: 'v1', label: 'Version one', file_ids: [id(10)] }] }
  const ref = { store_id: id(20), artifact_id: id(21), version_id: id(22) }
  const result = { asset_id: id(5), asset_version_id: id(6), manifest_ref: ref, manifest: { schema_version: 1, kind: 'asset-version', asset_id: id(5), name: 'Hall', asset_type: 'location', subjects: ['Hall'], depends_on: [], source: { project_id: id(1), project_revision_id: id(2), relation: 'produced_in' }, files: [{ file_id: id(10), path: 'scene.json', role: 'result', content_ref: ref }] } }
  const lifecycle = new Set<(event: Lifecycle) => void>(), contexts = new Set<(value: Context) => void>()
  let context: Context = { userId: 'user', organizationId: 'org', stationId: 'station', appId: 'world', installationId: 'installed', contextRevision: 'grant1', projectId: null }
  const records = new Map<string, string>(), calls: { operation: string; body: any; signal?: AbortSignal }[] = []
  const storage = { getItem: (key: string) => records.get(key) || null, setItem: (key: string, value: string) => { records.set(key, value) }, removeItem: (key: string) => { records.delete(key) } }
  let lose = false, registrations = 0, statusHash = preview.request_sha256, sourceOffline = false
  let register: (() => Promise<unknown>) | undefined
  const committed = new Set<string>()
  const sdk: AppSDK = {
    version: '1.1.0', context: { get: () => context, subscribe: fn => { contexts.add(fn); return () => contexts.delete(fn) } },
    lifecycle: { subscribe: fn => { lifecycle.add(fn); return () => lifecycle.delete(fn) } }, theme: { get: () => 'light', subscribe: () => () => {} },
    navigation: { get: () => ({ path: '/', revision: 1 }), subscribe: () => () => {}, request: async () => {} },
    capabilities: { call: async () => { throw Error('Unexpected host capability') } },
    app: { request: async (input, options) => {
      const operation = input.path.split('/').at(-1)!, body = clone(input.body) as any; calls.push({ operation, body, signal: options?.signal })
      let data: unknown
      if (operation === 'publication_source') { if (sourceOffline) throw Error('Project offline'); assert.deepEqual(body, pointer); data = preview }
      else if (operation === 'register') {
        assert.ok(records.size, 'persist recovery before any write'); assert.deepEqual(body, request)
        if (!committed.has(body.commit_id)) { registrations++; committed.add(body.commit_id) }
        if (register) data = await register(); else data = result
        if (lose) { lose = false; throw Error('lost response') }
      } else if (operation === 'commit_status') { assert.deepEqual(body, { asset_id: id(5), commit_id: id(4) }); data = { state: 'committed', request_sha256: statusHash, result } }
      else throw Error('Unexpected route ' + operation)
      return { status: 200, data: clone(data) as Json, requestId: 'request' }
    } },
  }
  return { pointer, request, preview, result, sdk, storage, records, calls, count: () => registrations, lose: () => { lose = true }, wrongHash: () => { statusHash = 'b'.repeat(64) }, defer: (fn: () => Promise<unknown>) => { register = fn },
    sourceOffline: () => { sourceOffline = true },
    emit: (event: Lifecycle) => { for (const fn of lifecycle) fn(event) }, change: () => { context = { ...context, organizationId: 'different' }; for (const fn of contexts) fn(context) },
  }
}
test('source preview is read-only and navigation preserves the two fixed revisions', async () => {
  const f = fixture(), c = createWorldPublication(f.sdk, f.storage, f.pointer)
  const preview = await c.load()
  assert.equal(preview.prepared_revision_id, id(3)); assert.equal(preview.request.source_revision_id, id(2)); assert.equal(f.count(), 0); assert.equal(f.records.size, 0)
  const status = await c.submit(); assert.equal(status.state, 'committed'); assert.equal(f.count(), 1)
  const route = new URL(c.returnPath(), 'https://app.invalid')
  assert.equal(route.searchParams.get('prepared'), id(3)); assert.equal(route.searchParams.get('version'), id(6)); assert.equal(route.searchParams.has('result'), false)
  assert.equal(canonicalRoute('world', '/publish?project=' + id(1)), '/publish?project=' + id(1)); c.dispose()
})
test('lost register response survives reconstruction and retries the identical request once', async () => {
  const f = fixture(); let c = createWorldPublication(f.sdk, f.storage, f.pointer); await c.load(); f.lose()
  await assert.rejects(c.submit(), /lost response/); assert.equal(c.pending()?.state, 'unknown'); c.dispose()
  c = createWorldPublication(f.sdk, f.storage, f.pointer); await c.submit()
  assert.equal(f.count(), 1); const writes = f.calls.filter(c => c.operation === 'register'); assert.equal(writes.length, 2); assert.deepEqual(writes[0].body, writes[1].body); c.dispose()
})
test('wrong original hash and changed fixed source never become a confirmed result', async () => {
  const f = fixture(), c = createWorldPublication(f.sdk, f.storage, f.pointer); f.wrongHash()
  await assert.rejects(c.submit(), e => e instanceof PublicationError && e.code === 'REQUEST_MISMATCH'); assert.equal(c.pending()?.state, 'unknown'); assert.throws(() => c.returnPath())
  f.preview.request.source_revision_id = id(90)
  await assert.rejects(c.submit(), e => e instanceof PublicationError && e.code === 'REQUEST_MISMATCH'); assert.equal(f.count(), 1); c.dispose()
})
test('unavailable recovery storage prevents register', async () => {
  const f = fixture(), c = createWorldPublication(f.sdk, { ...f.storage, setItem: () => { throw Error('quota') } }, f.pointer)
  await assert.rejects(c.submit(), e => e instanceof PublicationError && e.code === 'STORAGE_UNAVAILABLE'); assert.equal(f.count(), 0); c.dispose()
})
test('hiding preserves accepted register and later confirmation resumes from its saved result', async () => {
  const f = fixture(), c = createWorldPublication(f.sdk, f.storage, f.pointer)
  let resolve!: (value: unknown) => void, entered!: () => void
  const ready = new Promise<void>(done => { entered = done })
  f.defer(() => new Promise(done => { resolve = done; entered() }))
  const pending = c.submit(); await ready; f.emit('hidden')
  assert.equal(f.calls.at(-1)?.signal?.aborted, false); resolve(f.result)
  await assert.rejects(pending, e => e instanceof PublicationError && e.code === 'DASHBOARD_HIDDEN')
  assert.equal(c.pending()?.result?.asset_id, id(5)); f.emit('active'); await c.status(); assert.equal(c.pending()?.state, 'committed'); c.dispose()
})
test('known asset recovery queries World while Project is unavailable and never registers again', async () => {
  const f = fixture(); let c = createWorldPublication(f.sdk, f.storage, f.pointer); await c.submit(); c.dispose()
  f.sourceOffline(); c = createWorldPublication(f.sdk, f.storage, f.pointer)
  await c.submit(); assert.equal(f.calls.filter(c => c.operation === 'register').length, 1); assert.equal(c.pending()?.state, 'committed'); c.dispose()
})
test('authority changes abort writes and reject late results while keeping original journal', async () => {
  const f = fixture(), c = createWorldPublication(f.sdk, f.storage, f.pointer)
  let resolve!: (value: unknown) => void, entered!: () => void; const ready = new Promise<void>(done => { entered = done })
  f.defer(() => new Promise(done => { resolve = done; entered() }))
  const pending = c.submit(); await ready; f.change(); assert.equal(f.calls.at(-1)?.signal?.aborted, true); resolve(f.result)
  await assert.rejects(pending, e => e instanceof PublicationError && e.code === 'SESSION_REVOKED'); assert.equal(c.pending()?.state, 'unknown'); assert.equal(f.records.size, 1)
  const other = createWorldPublication(f.sdk, f.storage, f.pointer); assert.equal(other.pending(), undefined); other.dispose()
})
