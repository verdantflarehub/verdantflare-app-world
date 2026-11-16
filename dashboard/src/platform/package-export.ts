import { object, type CollectionDocument } from './draft-model.ts'
import { operationID } from './collection-save.ts'
import { packageJSON, validatePackageSchema } from './package-json.ts'
import { packageCanonical, encodeLearningPackage, type LearningPackageManifest, type PackageEntry, type PackageInspection } from './learning-package.ts'
import { packageTypedObjects, preparePackageImport } from './package-import.ts'
import { bytesHash } from './package-upload.ts'
import type { createPackageReferenceReader } from './package-reference.ts'

type Row = Record<string, unknown>
type Reader = Pick<ReturnType<typeof createPackageReferenceReader>, 'read'>
export interface PackageExport { manifest: LearningPackageManifest; bytes: Uint8Array; results: number; reference_files: number }
const encode = (v: unknown) => new TextEncoder().encode(JSON.stringify(v, null, 2))
function fixedReference(ref: Row, root: Row): Row {
  if (ref.kind !== 'file') return structuredClone(ref)
  const fixed: Row = { ...root, file_id: ref.file_id }; delete fixed.object_key
  return fixed
}

/** One current authorized root read; only references disclosed by that document
 * and the explicit selected dependency closure may cause additional reads. */
export async function exportLearningPackage(reader: Reader, root: Row, selected: string[] | null, includeReferences: boolean, signal?: AbortSignal): Promise<PackageExport> {
  signal?.throwIfAborted()
  if (root.object_key) throw Error('完整原件必须指向学习正文文件；成果范围单独选择。')
  const source = await reader.read(root, 4 * 1024 * 1024)
  const document = object(packageJSON(source.bytes)) as CollectionDocument
  validatePackageSchema(document.schema_version === 2 ? 'reference-learning-v2' : 'reference-learning', document)
  const keys = document.results.map(r => String(object(r).key)), wanted = selected === null ? keys : [...selected]
  if (new Set(wanted).size !== wanted.length || wanted.some(k => !keys.includes(k)) || selected !== null && !wanted.length) throw Error('请选择原件中存在的成果。')
  const manifest: LearningPackageManifest = { schema_version: 1, kind: 'learning-package', package_id: operationID(), created_at: new Date().toISOString(), root_refs: [structuredClone(root)], entries: [], relation_file_path: 'relations/document.json', scope: selected === null ? 'complete' : 'selected', selected_result_keys: wanted, missing_items: [] }
  const preview = preparePackageImport({ manifest, working: document } as PackageInspection)
  const required = new Set(preview.required_refs.map(r => packageCanonical(fixedReference(r, root))))
  const keepResults = new Set(preview.document.results.map(r => String(object(r).key))), keepSources = new Set(preview.document.sources.map(r => String(r.key)))
  const selectedDocument = selected === null ? document : { ...document, results: document.results.filter(r => keepResults.has(String(object(r).key))), sources: document.sources.filter(r => keepSources.has(String(r.key))), relations: preview.document.relations, coverage: preview.document.coverage, uses: [], ...(document.schema_version === 2 ? { curation: { packages: object(document.curation).packages, assessments: (object(document.curation).assessments as Row[]).filter(a => keepResults.has(String(a.result_key))) } } : {}) }
  const references = new Map<string, Row>()
  for (const ref of packageTypedObjects(selectedDocument)) if (['file', 'world_draft', 'project', 'asset'].includes(String(ref.kind))) {
    const fixed = fixedReference(ref, root); references.set(packageCanonical(fixed), fixed)
  }
  if (references.size > 995) throw Error('参考文件超过包条目上限，未生成截断包。')
  const payloads = new Map<string, Uint8Array>(); let total = source.bytes.length * 2
  async function entry(key: string, role: PackageEntry['role'], path: string, sourceRef: Row, bytes: Uint8Array, mime: string) {
    manifest.entries.push({ key, role, path, source_ref: structuredClone(sourceRef), mime, editable: role === 'working', size_bytes: bytes.length, sha256: await bytesHash(bytes) })
    payloads.set(key, bytes)
  }
  await entry('baseline', 'baseline', 'baseline/document.json', root, source.bytes, 'application/json')
  await entry('working', 'working', 'working/document.json', root, new Uint8Array(source.bytes), 'application/json')
  let count = 0
  for (const [key, reference] of references) {
    signal?.throwIfAborted()
    if (!includeReferences) { manifest.missing_items.push({ source_ref: reference, required: required.has(key), reason: 'not_included' }); continue }
    try {
      const read = await reader.read(reference, Math.min(64 * 1024 * 1024, 256 * 1024 * 1024 - total))
      signal?.throwIfAborted()
      if ((read.fixed.mime === 'application/json' || read.fixed.mime.endsWith('+json')) && read.bytes.length > 4 * 1024 * 1024) throw Error('JSON_LIMIT')
      total += read.bytes.length
      const entryKey = 'reference-' + String(++count).padStart(4, '0')
      await entry(entryKey, 'reference', 'references/' + entryKey + '.bin', reference, read.bytes, read.fixed.mime)
    } catch (error) {
      signal?.throwIfAborted()
      // Caller cancellation or identity changes must stop the whole export.
      if (error instanceof Error && (error.name === 'AbortError' || 'code' in error && ['SESSION_REVOKED', 'DASHBOARD_HIDDEN'].includes(String(error.code)))) throw error
      manifest.missing_items.push({ source_ref: reference, required: required.has(key), reason: error instanceof Error && (error.message === 'JSON_LIMIT' || 'code' in error && error.code === 'PACKAGE_LIMIT') ? 'over_limit' : 'unavailable' })
    }
  }
  // The relation index includes its own entry, as required by the central format.
  const relationRef = structuredClone(root)
  const links = { schema_version: 1, document_relations: document.relations, entry_refs: { ...Object.fromEntries(manifest.entries.map(e => [e.key, e.source_ref])), relations: relationRef } }
  await entry('relations', 'relations', manifest.relation_file_path, relationRef, encode(links), 'application/json')
  const readme = ['World learning package / 数字世界学习成果包', '', 'baseline/document.json：完整固定原件，请勿修改。', 'working/document.json：可编辑副本；只编辑此文件，不修改清单摘要。', 'relations/document.json：固定原件关系与文件来源索引，请勿修改。', 'manifest.json：范围、摘要及缺失依赖。参考字节在 references/。', '', `用途范围：${manifest.scope}；所选成果 ${wanted.length} 项。原件始终保持完整。`, '离线摘要一致只证明包内完整性，不证明来源或执行成功。', '导入将创建新的 imported 工作修订，旧运行和质量结论仅作历史证据。', '未包含的必需依赖须在目标环境显式映射；包不携带权限或下载令牌。', '不执行包内内容或工程。'].join('\n')
  signal?.throwIfAborted()
  const bytes = await encodeLearningPackage(manifest, payloads, readme)
  signal?.throwIfAborted()
  return { manifest, bytes, results: wanted.length, reference_files: count }
}
