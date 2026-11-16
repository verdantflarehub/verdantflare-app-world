import type { AppSDK } from './types.ts'
import { createDraftAPI, DraftError } from './draft-client.ts'
import { object, contentRef, readDraft, type DraftRevision, type DraftManifest, type CollectionDocument } from './draft-model.ts'
import { operationID } from './collection-save.ts'
import { inspectLearningPackage, packageCanonical, type PackageInspection } from './learning-package.ts'
import { preparePackageImport, mapPackageImport, packageTypedObjects, type ImportMapping } from './package-import.ts'
import { validateImportedDocument } from './package-semantics.ts'
import { bytesHash, createPackageUploader, type UploadedPackageEntry } from './package-upload.ts'
import { type PackageRecovery, type PackageRecoveryIntent } from './package-recovery.ts'
import { isID } from './world-model.ts'

type Row = Record<string, unknown>
interface FileIntent { key: string; file_id: string; write_id: string }
export interface ImportChoice { source: Row; entry_key?: string; target?: Row }
interface SavePlan extends Row { name: string; subjects: string[]; choices: ImportChoice[]; files: FileIntent[] }
interface Progress extends Row { base?: DraftRevision; uploaded?: Record<string, UploadedPackageEntry>; saved?: DraftRevision; failure?: string }
export interface RecoveryStore {
  read(): Promise<PackageRecovery | null>
  start(intent: PackageRecoveryIntent, bytes: Uint8Array): Promise<void>
  checkpoint(commitID: string, sequence: number, progress: Row): Promise<number>
  release(commitID: string, sequence: number): Promise<void>
}
interface Prepared { files: { key: string; path: string; mime: string; bytes: Uint8Array; file_id: string; write_id: string }[]; source_refs: Row[]; document_file_id: string; document: CollectionDocument }
const encode = (value: unknown) => new TextEncoder().encode(JSON.stringify(value))
const equal = (a: unknown, b: unknown) => packageCanonical(a) === packageCanonical(b)
function sameManifest(a: DraftManifest, b: DraftManifest) {
  const normalize = (m: DraftManifest) => Object.fromEntries(Object.entries(m).map(([k, v]) => [k, Array.isArray(v) ? [...v].sort((x, y) => packageCanonical(x) < packageCanonical(y) ? -1 : packageCanonical(x) > packageCanonical(y) ? 1 : 0) : v]))
  return equal(normalize(a), normalize(b))
}
const fail = (): never => { throw Error('导入意图或回执不一致，原操作仍保留。') }

function prepare(inspection: PackageInspection, plan: SavePlan): Prepared {
  if (typeof plan.name !== 'string' || !plan.name.trim() || plan.name.length > 256 || !Array.isArray(plan.subjects) || plan.subjects.length > 256 || plan.subjects.some(s => typeof s !== 'string' || !s.trim() || s.length > 256)) fail()
  const entries = inspection.manifest.entries
  if (!Array.isArray(plan.files) || plan.files.length !== entries.length || !Array.isArray(plan.choices)) fail()
  const fileIDs = new Set<string>(), writeIDs = new Set<string>(), files = new Map<string, FileIntent>()
  for (const f of plan.files) {
    if (!isID(f.file_id) || !isID(f.write_id) || fileIDs.has(f.file_id) || writeIDs.has(f.write_id) || files.has(f.key) || !entries.some(e => e.key === f.key)) fail()
    fileIDs.add(f.file_id); writeIDs.add(f.write_id); files.set(f.key, f)
  }
  const preview = preparePackageImport(inspection)
  const root = entries.find(e => e.role === 'baseline')!.source_ref
  function matchesEntry(source: Row, fixed: Row) {
    if (equal(source, fixed)) return true
    if (source.kind !== 'file' || source.file_id !== fixed.file_id || fixed.kind !== root.kind) return false
    const scope = root.kind === 'world_draft' ? ['draft_id', 'revision_id'] : root.kind === 'project' ? ['project_id', 'revision_id'] : ['asset_id', 'asset_version_id']
    return scope.every(k => root[k] === fixed[k]) && !fixed.object_key
  }
  const mappings: ImportMapping[] = plan.choices.map(choice => {
    if (Boolean(choice.entry_key) === Boolean(choice.target)) return fail()
    if (choice.entry_key) {
      const entry = entries.find(e => e.key === choice.entry_key), file = files.get(choice.entry_key)
      // Only explicit reference bytes can satisfy destination file references.
      if (!entry || entry.role !== 'reference' || !file || !matchesEntry(choice.source, entry.source_ref)) return fail()
      return { source: choice.source, state: 'imported', target: { kind: 'file', file_id: file.file_id } }
    }
    // Reuse is always a fixed business source, never an arbitrary local file ID.
    if (!choice.target || !['world_draft', 'project', 'asset'].includes(String(choice.target.kind))) return fail()
    return { source: choice.source, state: 'reused', target: choice.target }
  })
  const document = mapPackageImport(preview, mappings)
  validateImportedDocument(document, [...fileIDs])
  const sourceRefs = new Map<string, Row>()
  for (const node of packageTypedObjects(document)) if (['world_draft', 'project', 'asset'].includes(String(node.kind))) sourceRefs.set(packageCanonical(node), structuredClone(node))
  if (sourceRefs.size > 1000) fail()
  const payloads = entries.map(entry => ({ ...files.get(entry.key)!, path: entry.path, mime: entry.role === 'reference' ? entry.mime : 'application/json', bytes: entry.role === 'working' ? encode(document) : inspection.files.get(entry.path)! }))
  for (const file of payloads) if (!file.bytes || file.bytes.length > 64 * 1024 * 1024 || (file.mime === 'application/json' || file.mime.endsWith('+json')) && file.bytes.length > 4 * 1024 * 1024) fail()
  return { files: payloads, source_refs: [...sourceRefs.values()], document_file_id: files.get(entries.find(e => e.role === 'working')!.key)!.file_id, document }
}

/** authorize must perform current read-only permission/object binding checks for
 * all fixed targets. It runs before create/upload on every unfinished resume.
 * No asset registration is part of importing a working revision. */
export function createPackageSave(sdk: AppSDK, storage: RecoveryStore, authorize: (refs: Row[], document: CollectionDocument) => Promise<void>, writer = createPackageUploader(sdk)) {
  const api = createDraftAPI(sdk)
  let busy = false
  async function resume(): Promise<DraftRevision> {
    if (busy) throw Error('正在恢复原导入操作。')
    busy = true
    try {
      api.live()
      const recovery = await storage.read()
      if (!recovery) throw Error('没有待恢复的导入。')
      const { intent } = recovery
      if (![intent.create_id, intent.commit_id, intent.package_id].every(isID) || intent.create_id === intent.commit_id) fail()
      const inspection = await inspectLearningPackage(recovery.package_bytes)
      if (inspection.package_sha256 !== intent.package_sha256 || inspection.manifest.package_id !== intent.package_id) fail()
      const plan = intent.plan as SavePlan, prepared = prepare(inspection, plan)
      const progress = structuredClone(recovery.progress) as Progress
      let sequence = recovery.sequence
      async function checkpoint() { api.live(); sequence = await storage.checkpoint(intent.commit_id, sequence, progress) }
      if (progress.failure) throw new DraftError(progress.failure)
      if (progress.base) readDraft(progress.base, undefined, undefined, 'learning')
      if (progress.saved) readDraft(progress.saved, progress.base?.draft_id, undefined, 'learning')
      if (!progress.saved) {
        await authorize(prepared.source_refs, prepared.document); api.live()
        if (!progress.base) {
          const base = readDraft(await api.request({ mode: 'create', commit_id: intent.create_id, purpose: 'learning', name: plan.name, subjects: plan.subjects }), undefined, undefined, 'learning')
          if (base.manifest.name !== plan.name || !equal(base.manifest.subjects, plan.subjects) || base.manifest.files.length || base.manifest.domain_documents.length || base.manifest.source_refs.length || base.manifest.asset_refs.length || base.manifest.run_refs.length) fail()
          progress.base = base; await checkpoint()
        }
        progress.uploaded ??= {}
        for (const file of prepared.files) {
          const sha256 = await bytesHash(file.bytes)
          let receipt = progress.uploaded[file.key]
          if (!receipt) {
            receipt = await writer.write({ mode: 'prepare', write_id: file.write_id, source: { kind: 'user_import', world_draft_id: progress.base.draft_id }, mime: file.mime, size: file.bytes.length, sha256 }, file.bytes)
            progress.uploaded[file.key] = receipt
            // Validation precedes saving each receipt, including injected writers.
            contentRef(receipt.content_ref)
            if (receipt.sha256 !== sha256 || receipt.size !== file.bytes.length || receipt.mime !== file.mime) fail()
            await checkpoint()
          }
          contentRef(receipt.content_ref)
          if (receipt.sha256 !== sha256 || receipt.size !== file.bytes.length || receipt.mime !== file.mime) fail()
        }
      }
      const base = progress.base
      if (!base || !progress.uploaded || prepared.files.some(f => !progress.uploaded![f.key])) fail()
      for (const file of prepared.files) {
        const receipt = progress.uploaded![file.key]
        if (receipt.size !== file.bytes.length || receipt.mime !== file.mime || receipt.sha256 !== await bytesHash(file.bytes)) fail()
      }
      const manifest: DraftManifest = { ...structuredClone(base!.manifest), name: plan.name, subjects: plan.subjects,
        files: prepared.files.map(f => ({ file_id: f.file_id, path: f.path, role: 'reference', content_ref: contentRef(progress.uploaded![f.key].content_ref) })),
        domain_documents: [{ document_type: 'reference-learning', file_id: prepared.document_file_id }], source_refs: prepared.source_refs, asset_refs: [], run_refs: [] }
      if (!progress.saved) {
        try {
          const saved = readDraft(await api.request({ mode: 'commit', commit_id: intent.commit_id, draft_id: base!.draft_id, expected_revision_id: base!.revision_id, manifest }), base!.draft_id, undefined, 'learning')
          if (!sameManifest(saved.manifest, manifest)) fail()
          progress.saved = saved; await checkpoint()
        } catch (error) {
          try {
            const status = object(await api.request({ mode: 'status', commit_id: intent.commit_id }))
            if (status.kind === 'status' && ['failed', 'conflict'].includes(String(status.state))) { progress.failure = String(status.error_code || 'TERMINAL_FAILURE'); await checkpoint() }
          } catch { /* Unknown status retains all original IDs. */ }
          throw error
        }
      }
      if (!sameManifest(progress.saved!.manifest, manifest)) fail()
      const saved = readDraft(await api.request({ mode: 'get', draft_id: progress.saved!.draft_id, revision_id: progress.saved!.revision_id }), progress.saved!.draft_id, progress.saved!.revision_id, 'learning')
      if (!sameManifest(saved.manifest, manifest)) fail()
      api.live(); await storage.release(intent.commit_id, sequence)
      return saved
    } finally { busy = false }
  }
  async function start(bytes: Uint8Array, name: string, subjects: string[], choices: ImportChoice[]): Promise<DraftRevision> {
    if (busy) throw Error('正在处理原导入操作。')
    busy = true
    try {
      api.live()
      if (await storage.read()) throw Error('请先恢复已有导入。')
      const raw = new Uint8Array(bytes), inspection = await inspectLearningPackage(raw)
      const plan: SavePlan = { name: name.trim(), subjects: structuredClone(subjects).sort(), choices: structuredClone(choices), files: inspection.manifest.entries.map(e => ({ key: e.key, file_id: operationID(), write_id: operationID() })) }
      prepare(inspection, plan)
      api.live()
      await storage.start({ version: 1, package_id: inspection.manifest.package_id, package_sha256: inspection.package_sha256, create_id: operationID(), commit_id: operationID(), plan }, raw)
    } finally { busy = false }
    return resume()
  }
  /** Explicit user action only: unknown or successful operations cannot be replaced.
   * Recheck the authoritative terminal status before releasing the local CAS slot. */
  async function returnFailedToEditing(): Promise<PackageRecovery> {
    if (busy) throw Error('正在处理原导入操作。')
    busy = true
    try {
      api.live()
      const recovery = await storage.read()
      if (!recovery?.progress.failure || recovery.progress.saved) throw new DraftError('IMPORT_NOT_TERMINAL')
      const status = object(await api.request({ mode: 'status', commit_id: recovery.intent.commit_id }))
      if (status.kind !== 'status' || !['failed', 'conflict'].includes(String(status.state))) throw new DraftError('IMPORT_NOT_TERMINAL')
      const inspection = await inspectLearningPackage(recovery.package_bytes)
      if (inspection.package_sha256 !== recovery.intent.package_sha256 || inspection.manifest.package_id !== recovery.intent.package_id) fail()
      prepare(inspection, recovery.intent.plan as SavePlan)
      api.live()
      await storage.release(recovery.intent.commit_id, recovery.sequence)
      return recovery
    } finally { busy = false }
  }
  return { start, resume, returnFailedToEditing, dispose() { api.dispose(); writer.dispose() } }
}
