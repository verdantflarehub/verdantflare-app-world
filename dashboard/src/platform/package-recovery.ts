import { DraftError } from './draft-client.ts'
import { bytesHash } from './package-upload.ts'
import { isID } from './world-model.ts'

export interface PackageRecoveryIntent {
  version: 1
  package_id: string
  package_sha256: string
  create_id: string
  commit_id: string
  /** Fixed, JSON-serializable mapping, baseline and per-file write/file IDs. */
  plan: Record<string, unknown>
}
export interface PackageRecovery {
  intent: PackageRecoveryIntent
  package_bytes: Uint8Array
  sequence: number
  progress: Record<string, unknown>
}
interface StoredRecovery extends Omit<PackageRecovery, 'package_bytes'> { principal: string; package_blob: Blob }
const fail = (): never => { throw new DraftError('STORAGE_UNAVAILABLE') }
const conflict = (): never => { throw Error('恢复记录已变化，请重新读取原操作。') }
function validIntent(v: PackageRecoveryIntent) {
  if (v.version !== 1 || ![v.package_id, v.create_id, v.commit_id].every(isID) || !/^[a-f0-9]{64}$/.test(v.package_sha256) || !v.plan || typeof v.plan !== 'object' || Array.isArray(v.plan)) fail()
}

/** Blob and immutable intent are committed atomically before any remote write.
 * Each principal owns one pending operation. IndexedDB serializes competing tabs;
 * checkpoints use CAS so a stale tab cannot overwrite a newer recovery receipt. */
export function createPackageRecovery(principal: readonly string[], factory: IDBFactory = indexedDB) {
  if (principal.length !== 5 || principal.some(v => typeof v !== 'string' || !v)) fail()
  const key = JSON.stringify(principal)
  let connection: Promise<IDBDatabase> | undefined, disposed = false
  function open(): Promise<IDBDatabase> {
    if (disposed) return Promise.reject(new DraftError('STORAGE_UNAVAILABLE'))
    if (!connection) connection = new Promise((resolve, reject) => {
      let blocked = false
      const request = factory.open('world.learning-package-recovery.v1', 1)
      request.onupgradeneeded = () => { request.result.createObjectStore('pending', { keyPath: 'principal' }) }
      request.onerror = () => reject(new DraftError('STORAGE_UNAVAILABLE'))
      request.onblocked = () => { blocked = true; reject(new DraftError('STORAGE_UNAVAILABLE')) }
      request.onsuccess = () => {
        const db = request.result
        if (disposed || blocked) { db.close(); reject(new DraftError('STORAGE_UNAVAILABLE')); return }
        db.onversionchange = () => { db.close(); connection = undefined }
        resolve(db)
      }
    })
    return connection
  }
  async function transaction<T>(mode: IDBTransactionMode, action: (store: IDBObjectStore, value: StoredRecovery | undefined) => T): Promise<T> {
    const db = await open()
    if (disposed) fail()
    return new Promise((resolve, reject) => {
      // A receipt is usable only after transaction completion, never request success.
      const tx = db.transaction('pending', mode, { durability: 'strict' })
      const store = tx.objectStore('pending'), request = store.get(key)
      let result: T, error: unknown
      tx.oncomplete = () => resolve(result)
      tx.onabort = () => reject(error || new DraftError('STORAGE_UNAVAILABLE'))
      tx.onerror = () => { /* abort is the authoritative failure event */ }
      request.onsuccess = () => {
        try { result = action(store, request.result) } catch (e) { error = e; tx.abort() }
      }
    })
  }
  async function read(): Promise<PackageRecovery | null> {
    const saved = await transaction('readonly', (_store, value) => value)
    if (!saved) return null
    validIntent(saved.intent)
    if (saved.principal !== key || !Number.isSafeInteger(saved.sequence) || saved.sequence < 0 || !(saved.package_blob instanceof Blob) || saved.package_blob.size > 256 * 1024 * 1024) fail()
    const bytes = new Uint8Array(await saved.package_blob.arrayBuffer())
    if (await bytesHash(bytes) !== saved.intent.package_sha256) fail()
    return { intent: saved.intent, package_bytes: bytes, sequence: saved.sequence, progress: saved.progress }
  }
  async function start(intent: PackageRecoveryIntent, input: Uint8Array): Promise<void> {
    const fixed = structuredClone(intent), bytes = new Uint8Array(input)
    validIntent(fixed)
    if (!bytes.byteLength || bytes.byteLength > 256 * 1024 * 1024 || await bytesHash(bytes) !== fixed.package_sha256) fail()
    const record: StoredRecovery = { principal: key, intent: fixed, package_blob: new Blob([bytes]), sequence: 0, progress: {} }
    await transaction('readwrite', (store, existing) => {
      if (existing) conflict()
      store.add(record)
    })
  }
  async function checkpoint(commitID: string, sequence: number, progress: Record<string, unknown>): Promise<number> {
    const fixed = structuredClone(progress)
    return transaction('readwrite', (store, value) => {
      if (!value || value.intent.commit_id !== commitID || value.sequence !== sequence) conflict()
      store.put({ ...value, progress: fixed, sequence: sequence + 1 })
      return sequence + 1
    })
  }
  /** Caller invokes only after fixed business readback, or confirmed terminal failure. */
  async function release(commitID: string, sequence: number): Promise<void> {
    await transaction('readwrite', (store, value) => {
      if (!value || value.intent.commit_id !== commitID || value.sequence !== sequence) conflict()
      store.delete(key)
    })
  }
  function dispose() { disposed = true; void connection?.then(db => db.close(), () => {}) }
  return { read, start, checkpoint, release, dispose }
}
