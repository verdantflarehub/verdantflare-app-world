import { object, type CollectionDocument } from './draft-model.ts'
import { packageCanonical, type PackageInspection } from './learning-package.ts'
import { rejectPackage, validatePackageSchema } from './package-json.ts'

type Row = Record<string, unknown>
const rows = (v: unknown): Row[] => Array.isArray(v) ? v.map(object) : []
const note = '导入编辑稿，尚未在当前环境重新验证；历史记录见固定原件。'

/** Match the central validator's typed walk; provider parameters are opaque. */
export function* packageTypedObjects(value: unknown): Generator<Row> {
  if (Array.isArray(value)) { for (const item of value) yield* packageTypedObjects(item) }
  else if (value && typeof value === 'object') {
    const row = object(value)
    yield row
    for (const [key, child] of Object.entries(row)) {
      if (!['parameters', 'parameter_overrides', 'default'].includes(key) || key === 'default' && row.value_type === 'ref') yield* packageTypedObjects(child)
    }
  }
}

export interface ImportPreview {
  document: CollectionDocument
  selected_keys: string[]
  dependency_keys: string[]
  /** Every external identity starts pending, even in the original environment. */
  required_refs: Row[]
}

/** Pure preparation: no source reads, permission claims, uploads or draft writes.
 * The caller must resolve required_refs and run service semantic validation before save. */
export function preparePackageImport(inspection: PackageInspection): ImportPreview {
  const doc = structuredClone(inspection.working)
  validatePackageSchema(doc.schema_version === 2 ? 'reference-learning-v2' : 'reference-learning', doc)
  doc.origin = 'imported'
  delete doc.provenance
  delete doc.processing_snapshot
  doc.uses = []
  for (const coverage of rows(doc.coverage)) Object.assign(coverage, { state: 'pending', covered_scopes: [], note })
  for (const result of rows(doc.results)) {
    const payload = object(result.payload)
    for (const condition of rows(result.use_conditions)) condition.availability = 'unknown'
    if (result.type_key === 'comfyui_workflow') delete payload.execution_snapshot_ref
    if (result.type_key === 'prompt') for (const variant of rows(payload.variants)) {
      Object.assign(variant, { compatibility: 'unresolved', execution_evidence: [], quality_review: { state: 'unreviewed', note } })
      delete variant.capability_ref
      delete variant.capability_sha256
    }
    for (const verification of rows(payload.verification)) Object.assign(verification, { status: 'unverified', evidence_refs: [], note })
  }
  if (doc.schema_version === 2) for (const assessment of rows(object(doc.curation).assessments)) {
    Object.assign(assessment, { selection: 'unassessed', reason_codes: ['pending_review'], note })
    // Even original-reference bytes need an authorized destination mapping.
    // Merged lineage stays explicit; it must never disappear during normalization.
    assessment.previews = rows(assessment.previews).map(p => ({ key: p.key, state: 'missing', media_kind: p.media_kind, role: p.role, note, reason_code: 'pending_review' }))
  }
  const results = rows(doc.results), sources = rows(doc.sources)
  const index = new Map<string, Row>()
  for (const row of [...results, ...sources]) {
    const key = String(row.key)
    if (index.has(key)) rejectPackage('DUPLICATE_LOCAL_KEY')
    index.set(key, row)
  }
  const selected = [...inspection.manifest.selected_result_keys]
  const resultKeys = new Set(results.map(r => String(r.key)))
  if (new Set(selected).size !== selected.length || selected.some(k => !resultKeys.has(k))) rejectPackage('SELECTED_RESULT_MISSING')
  // Complete means the edited document, including new results, not an old count.
  const keep = new Set(inspection.manifest.scope === 'complete' ? [...index.keys()] : selected)
  const queue = [...keep]
  for (let i = 0; i < queue.length; i++) {
    const row = index.get(queue[i])!
    const refs = [...packageTypedObjects(row)].filter(r => r.kind === 'source' || r.kind === 'result')
    if (row.parent_key) refs.push({ kind: 'result', key: row.parent_key })
    for (const ref of refs) {
      const key = String(ref.key)
      if (!index.has(key) || (ref.kind === 'result') !== resultKeys.has(key)) rejectPackage('MISSING_LOCAL_DEPENDENCY')
      if (!keep.has(key)) { keep.add(key); queue.push(key) }
    }
  }
  // Parent cycles are invalid, unlike general relationship cycles.
  for (const key of keep) {
    const chain = new Set<string>()
    let current: string | undefined = key
    while (current) {
      if (chain.has(current)) rejectPackage('CYCLIC_PARENT')
      chain.add(current)
      current = index.get(current)?.parent_key as string | undefined
    }
  }
  doc.results = results.filter(r => keep.has(String(r.key)))
  doc.sources = sources.filter(r => keep.has(String(r.key)))
  doc.coverage = rows(doc.coverage).filter(r => keep.has(String(r.source_key)))
  doc.relations = rows(doc.relations).filter(r => [...packageTypedObjects(r)].every(ref => !['result', 'source'].includes(String(ref.kind)) || keep.has(String(ref.key))))
  if (doc.schema_version === 2) {
    const curation = object(doc.curation)
    curation.packages = rows(curation.packages).map(p => ({ ...p, result_keys: (p.result_keys as string[]).filter(k => keep.has(k)) })).filter(p => p.result_keys.length)
    const packages = new Set(rows(curation.packages).map(p => p.key))
    curation.assessments = rows(curation.assessments).filter(a => keep.has(String(a.result_key))).map(a => ({ ...a, package_keys: (a.package_keys as string[]).filter(k => packages.has(k)) }))
  }
  const external = new Map<string, Row>()
  for (const ref of packageTypedObjects(doc)) if (['file', 'project', 'world_draft', 'asset'].includes(String(ref.kind))) external.set(packageCanonical(ref), structuredClone(ref))
  validatePackageSchema(doc.schema_version === 2 ? 'reference-learning-v2' : 'reference-learning', doc)
  return { document: doc, selected_keys: selected, dependency_keys: results.map(r => String(r.key)).filter(k => keep.has(k) && !selected.includes(k)), required_refs: [...external.values()] }
}

export interface ImportMapping { source: Row; state: 'pending' | 'imported' | 'reused' | 'missing' | 'failed'; target?: Row }

/** A destination is never inferred from a source UUID. Authorization of supplied
 * targets is still the service's responsibility; this only checks mapping closure. */
export function mapPackageImport(preview: ImportPreview, mappings: ImportMapping[]): CollectionDocument {
  const required = new Set(preview.required_refs.map(packageCanonical)), targets = new Map<string, Row>()
  for (const mapping of mappings) {
    const key = packageCanonical(mapping.source)
    if (!required.has(key) || targets.has(key) || !['imported', 'reused'].includes(mapping.state) || !mapping.target) rejectPackage('INCOMPLETE_REFERENCE_MAPPING')
    const target = object(mapping.target)
    if (!['file', 'project', 'world_draft', 'asset'].includes(String(target.kind))) rejectPackage('INVALID_TARGET_REFERENCE')
    targets.set(key, structuredClone(target))
  }
  if (targets.size !== required.size) rejectPackage('INCOMPLETE_REFERENCE_MAPPING')
  const document = structuredClone(preview.document)
  // Materialize the walk before replacement, so new targets cannot be remapped.
  for (const node of [...packageTypedObjects(document)]) {
    const target = targets.get(packageCanonical(node))
    if (!target) continue
    for (const key of Object.keys(node)) delete node[key]
    Object.assign(node, target)
  }
  validatePackageSchema(document.schema_version === 2 ? 'reference-learning-v2' : 'reference-learning', document)
  return document
}
