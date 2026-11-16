import { object, type CollectionDocument } from './draft-model.ts'
import { packageCanonical } from './learning-package.ts'
import { packageTypedObjects } from './package-import.ts'
import { rejectPackage, validatePackageSchema } from './package-json.ts'
import contracts from '../generated/learning-package-contracts.json' with { type: 'json' }

type Row = Record<string, unknown>
const list = (v: unknown): Row[] => (v as unknown[]).map(object)
const strings = (v: unknown): string[] => v as string[]
const fail = (reason: string): never => rejectPackage('IMPORT_SEMANTICS_' + reason)
function unique(values: string[], label: string): Set<string> {
  const result = new Set(values)
  if (result.size !== values.length) fail(label)
  return result
}
function index(value: unknown, label: string): Map<string, Row> {
  const items = list(value); unique(items.map(r => String(r.key)), label)
  return new Map(items.map(r => [String(r.key), r]))
}
function dag(keys: Iterable<string>, edges: [string, string][]) {
  const degrees = new Map([...keys].map(k => [k, 0])), children = new Map<string, string[]>()
  for (const [from, to] of new Map(edges.map(e => [packageCanonical(e), e])).values()) {
    if (!degrees.has(from) || !degrees.has(to)) fail('MISSING_DEPENDENCY')
    degrees.set(to, degrees.get(to)! + 1); children.set(from, [...children.get(from) || [], to])
  }
  const queue = [...degrees].filter(([, n]) => n === 0).map(([k]) => k)
  for (let i = 0; i < queue.length; i++) for (const child of children.get(queue[i]) || []) {
    const degree = degrees.get(child)! - 1; degrees.set(child, degree)
    if (!degree) queue.push(child)
  }
  if (queue.length !== degrees.size) fail('CYCLIC_DEPENDENCY')
}

/** Local semantics of the normalized import profile. Current source permissions,
 * external object types/rules and merge bindings still require service validation.
 * This is not a validator for arbitrary historical generated documents. */
export function validateImportedDocument(doc: CollectionDocument, fileIDs: string[]) {
  validatePackageSchema(doc.schema_version === 2 ? 'reference-learning-v2' : 'reference-learning', doc)
  if (doc.origin !== 'imported' || doc.provenance || doc.processing_snapshot || doc.uses.length) fail('EXECUTION_HISTORY')
  const sources = index(doc.sources, 'SOURCE_KEYS'), results = index(doc.results, 'RESULT_KEYS'), files = unique(fileIDs, 'FILE_IDS')
  index(doc.relations, 'RELATION_KEYS')
  if ([...sources.keys()].some(k => results.has(k))) fail('OVERLAPPING_KEYS')
  for (const node of packageTypedObjects(doc)) {
    if (node.kind === 'source' && !sources.has(String(node.key)) || node.kind === 'result' && !results.has(String(node.key)) || node.kind === 'file' && !files.has(String(node.file_id))) fail('REFERENCE')
    // Avoid silently accepting rounded JSON integer identities/time coordinates.
    if (node.kind === 'time') {
      if (!['start_tick', 'end_tick', 'timebase_num', 'timebase_den'].every(k => Number.isSafeInteger(node[k])) || Number(node.end_tick) <= Number(node.start_tick)) fail('TIME_RANGE')
    }
    if (node.kind === 'lines' && (!Number.isSafeInteger(node.start_line) || !Number.isSafeInteger(node.end_line) || Number(node.end_line) < Number(node.start_line))) fail('LINE_RANGE')
    if ('service_id' in node && 'run_id' in node) fail('EXECUTION_HISTORY')
  }
  const parents: [string, string][] = []
  const schema = object(object(contracts)['station://contracts/v2/reference-learning'])
  const resultProperties = object(object(object(schema.$defs).result).properties)
  const outputTypes = new Set(strings(object(resultProperties.type_key).enum))
  for (const [key, result] of results) {
    if (result.parent_key) parents.push([String(result.parent_key), key])
    index(result.evidence, 'EVIDENCE_KEYS')
    unique(list(result.use_conditions).map(c => packageCanonical([c.mode, c.purpose])), 'USE_CONDITIONS')
    if (list(result.use_conditions).some(c => c.availability !== 'unknown')) fail('USE_NOT_RESET')
    const p = object(result.payload)
    if (result.type_key === 'prompt') {
      index(p.variables, 'VARIABLE_KEYS'); index(p.variants, 'VARIANT_KEYS')
      for (const variant of list(p.variants)) {
        index(variant.reference_bindings, 'BINDING_KEYS')
        if (variant.compatibility !== 'unresolved' || list(variant.execution_evidence).length || object(variant.quality_review).state !== 'unreviewed' || variant.capability_ref || variant.capability_sha256) fail('PROMPT_NOT_RESET')
      }
    }
    if (p.verification && list(p.verification).some(v => v.status !== 'unverified' || list(v.evidence_refs).length)) fail('VERIFICATION_NOT_RESET')
    if (result.type_key === 'comfyui_workflow' && p.execution_snapshot_ref) fail('EXECUTION_HISTORY')
    if (result.type_key === 'method' || result.type_key === 'skill_candidate') {
      const inputs = index(p.inputs, 'INPUT_KEYS'), outputs = index(p.expected_outputs ?? p.outputs, 'OUTPUT_KEYS')
      index(p.steps, 'STEP_KEYS')
      const declared = new Set(inputs.keys()), produced = new Set<string>()
      for (const step of list(p.steps)) {
        if (strings(step.input_keys).some(k => !declared.has(k))) fail('FUTURE_INPUT')
        for (const k of strings(step.output_keys)) {
          if (inputs.has(k) || produced.has(k)) fail('OUTPUT_OVERWRITE')
          produced.add(k); declared.add(k)
        }
      }
      for (const [k, value] of outputs) if (!produced.has(k) || !outputTypes.has(String(value.type_key))) fail('OUTPUT')
    }
    if (result.type_key === 'creative_system') {
      if ((p.research_subjects as unknown[]).length && !(p.corpus_refs as unknown[]).length) fail('CORPUS')
      const modules = index(p.modules, 'MODULE_KEYS'), rules = index(p.coordination_rules, 'RULE_KEYS'), edges: [string, string][] = []
      for (const [k, m] of modules) {
        for (const dep of strings(m.depends_on_keys)) edges.push([dep, k])
        const ref = object(m.method_ref)
        if (ref.kind === 'source' || ref.kind === 'file' || ref.kind === 'result' && !['method', 'style'].includes(String(results.get(String(ref.key))?.type_key))) fail('MODULE_TYPE')
      }
      dag(modules.keys(), edges)
      for (const r of rules.values()) if (strings(r.target_module_keys).some(k => !modules.has(k))) fail('TARGET_MODULE')
      if (result.type_version === 2) for (const e of index(p.exceptions, 'EXCEPTION_KEYS').values()) if (strings(e.target_rule_keys).some(k => !rules.has(k))) fail('EXCEPTION_RULE')
    }
    if (result.type_key === 'style') index(p.rules, 'STYLE_RULE_KEYS')
    if (['scene', 'blender_scene'].includes(String(result.type_key)) && p.coordinate_system) {
      const c = object(p.coordinate_system)
      if (String(c.up_axis).replace('-', '') === String(c.forward_axis).replace('-', '')) fail('COORDINATE_AXES')
    }
    if (result.type_key === 'camera_animation' && (!Number.isSafeInteger(p.frame_start) || !Number.isSafeInteger(p.frame_end) || Number(p.frame_end) <= Number(p.frame_start))) fail('FRAME_RANGE')
  }
  dag(results.keys(), parents)
  for (const basis of ['presentation', 'story_time', 'media_time']) {
    const edges: [string, string][] = []
    for (const relation of list(doc.relations)) {
      const a = object(relation.from_ref), b = object(relation.to_ref), attributes = object(relation.attributes)
      if (relation.relation_type === 'precedes' && attributes.order_basis === basis && a.kind === 'result' && b.kind === 'result') edges.push([String(a.key), String(b.key)])
      if (relation.relation_type === 'constrains' && a.kind === 'result') {
        const r = results.get(String(a.key))!, field = ({ method: 'constraints', style: 'rules', creative_system: 'coordination_rules' } as Record<string, string>)[String(r.type_key)]
        if (!field || !list(object(r.payload)[field]).some(rule => rule.key === attributes.rule_key)) fail('RULE_REFERENCE')
      }
    }
    dag(results.keys(), edges)
  }
  const coverage = list(doc.coverage)
  unique(coverage.map(c => packageCanonical([c.source_key, c.dimension, c.requested_scope])), 'COVERAGE_KEYS')
  if (coverage.some(c => !sources.has(String(c.source_key)) || c.state !== 'pending' || list(c.covered_scopes).length)) fail('COVERAGE_NOT_RESET')
  if (doc.schema_version === 2) {
    const c = object(doc.curation), packages = index(c.packages, 'PACKAGE_KEYS'), seen = new Set<string>(), merged = new Set<string>()
    for (const pkg of packages.values()) if ([...unique(strings(pkg.result_keys), 'MEMBERS')].some(k => !results.has(k))) fail('PACKAGE_RESULT')
    for (const a of list(c.assessments)) {
      const key = String(a.result_key)
      if (!results.has(key) || seen.has(key) || a.selection !== 'unassessed') fail('ASSESSMENT')
      seen.add(key); unique(strings(a.reason_codes), 'REASON_CODES')
      const actual = unique(strings(a.package_keys), 'PACKAGE_MEMBERSHIP'), expected = [...packages].filter(([, p]) => strings(p.result_keys).includes(key)).map(([k]) => k)
      if (expected.length !== actual.size || expected.some(k => !actual.has(k))) fail('PACKAGE_MEMBERSHIP')
      for (const field of ['evidence_refs', 'merged_from_refs']) {
        const refs = list(a[field]).map(ref => { const copy = { ...ref }; delete copy.purpose; return packageCanonical(copy) })
        unique(refs, 'CURATION_REFERENCES')
        if (field === 'merged_from_refs') for (let i = 0; i < refs.length; i++) {
          if (!list(a[field])[i].object_key || merged.has(refs[i])) fail('MERGED_IDENTITY')
          merged.add(refs[i])
        }
      }
      index(a.previews, 'PREVIEW_KEYS')
      if (list(a.previews).some(p => p.state !== 'missing')) fail('PREVIEW_NOT_RESET')
    }
    if (seen.size !== results.size) fail('MISSING_ASSESSMENT')
  }
}

/** Mirror external method/rule/merge checks using freshly authorized fixed
 * objects. No historical map or package-supplied permission is accepted. */
export function validateImportedBindings(doc: CollectionDocument, objects: Map<string, Row>) {
  const results = index(doc.results, 'RESULT_KEYS')
  const resolve = (ref: Row): Row => {
    if (ref.kind === 'result') return results.get(String(ref.key)) ?? fail('MISSING_RESULT')
    if (!ref.object_key) return fail('MISSING_FIXED_OBJECT')
    return objects.get(packageCanonical(ref)) ?? fail('UNRESOLVED_FIXED_OBJECT')
  }
  for (const result of results.values()) if (result.type_key === 'creative_system') {
    for (const module of list(object(result.payload).modules)) if (!['method', 'style'].includes(String(resolve(object(module.method_ref)).type_key))) fail('MODULE_TYPE')
  }
  for (const relation of list(doc.relations)) if (relation.relation_type === 'constrains') {
    const result = resolve(object(relation.from_ref)), field = ({ method: 'constraints', style: 'rules', creative_system: 'coordination_rules' } as Record<string, string>)[String(result.type_key)]
    if (!field || !list(object(result.payload)[field]).some(r => r.key === object(relation.attributes).rule_key)) fail('RULE_REFERENCE')
  }
  if (doc.schema_version !== 2) return
  const identity = (ref: Row) => { const copy = { ...ref }; delete copy.purpose; return packageCanonical(copy) }
  for (const assessment of list(object(doc.curation).assessments)) {
    const result = results.get(String(assessment.result_key))!, current = new Set(list(result.source_refs).filter(r => ['world_draft', 'project', 'asset'].includes(String(r.kind))).map(identity))
    for (const ref of list(assessment.merged_from_refs)) {
      const original = resolve(ref)
      if (original.type_key !== result.type_key || original.domain !== result.domain || original.type_version !== result.type_version) fail('MERGE_TYPE')
      for (const source of list(original.source_refs)) {
        let fixed = source
        if (source.kind === 'source' || source.kind === 'result') fixed = { ...ref, object_key: source.key }
        else if (source.kind === 'file') { fixed = { ...ref, file_id: source.file_id }; delete fixed.object_key }
        if (!current.has(identity(fixed))) fail('MERGE_PROVENANCE_DROPPED')
      }
    }
  }
}
