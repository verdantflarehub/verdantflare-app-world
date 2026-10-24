import type { LearningDocument, LearningResult } from './learning-results.ts'
export const record = (v: unknown): Record<string, unknown> => v && typeof v === 'object' && !Array.isArray(v) ? v as Record<string, unknown> : {}
const strings = (v: unknown): string[] => Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []
export interface ReferenceRange { start: number; end: number; note: string }
export interface ResultReference { source: Record<string, unknown>; sha256?: string; ranges: ReferenceRange[] }
interface Entry { kind: string; value: Record<string, unknown>; source: string; line: number }
const cache = new WeakMap<LearningDocument, Entry[]>()
function entries(document: LearningDocument): Entry[] {
  const cached = cache.get(document); if (cached) return cached
  const out: Entry[] = []
  for (const source of document.sources) {
    const content = record(source.content)
    if (content.kind !== 'inline' || typeof content.text !== 'string') continue
    content.text.split('\n').forEach((line, i) => {
      try { const r = record(JSON.parse(line)); if (['analysis_context','observation','result_candidate','prompt_candidate'].includes(String(r.kind))) out.push({ kind: String(r.kind), value: record(r.value), source: String(source.key), line: i + 1 }) } catch { /* Ordinary prose is not an evidence log. */ }
    })
  }
  cache.set(document, out); return out
}
function range(raw: unknown, duration: number, note = ''): ReferenceRange | null {
  const v = record(raw), start = Number(v.start_ms), end = Number(v.end_ms)
  if (typeof v.start_ms !== 'number' || typeof v.end_ms !== 'number' || !Number.isFinite(start) || !Number.isFinite(end) || start < 0 || end <= start || end > duration) return null
  return { start: start / 1000, end: end / 1000, note }
}
/** Compatibility projection of explicit analysis evidence; no title or proximity matching. */
export function resultReference(result: LearningResult, document: LearningDocument): ResultReference | null {
  if (result.type_version !== 1) return null
  const sourceKeys = result.source_refs.map(record).filter(r => r.kind === 'source').map(r => r.key)
  const originals = document.sources.filter(s => sourceKeys.includes(s.key) && s.role === 'original' && s.acquisition_state === 'ready' && ['video','audio'].includes(String(s.media_kind)))
  if (originals.length !== 1) return null
  const source = originals[0], content = record(source.content)
  const all = entries(document)
  const cited = all.filter(e => sourceKeys.includes(e.source))
  // Source identity is document metadata; audio observations may be in a
  // different evidence file. Content evidence below remains explicitly cited.
  const contexts = all.filter(e => e.kind === 'analysis_context' && content.kind === 'world_draft' && e.value.draft_id === content.draft_id && e.value.revision_id === content.revision_id && e.value.file_id === content.file_id)
  if (contexts.length !== 1) return null
  const info = record(contexts[0].value.source), duration = Number(info.duration_ms)
  if (!Number.isFinite(duration) || duration <= 0 || typeof info.sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(info.sha256)) return null
  const candidates = cited.filter(e => ['result_candidate','prompt_candidate'].includes(e.kind) && e.value.key === result.key && (e.kind === 'prompt_candidate' || e.value.type_key === result.type_key) && result.evidence.some(raw => {
    const evidence = record(raw), locator = record(evidence.locator)
    return record(evidence.source_ref).key === e.source && locator.kind === 'lines' && Number(locator.start_line) <= e.line && Number(locator.end_line) >= e.line
  }))
  if (candidates.length !== 1 || cited.filter(e => ['result_candidate','prompt_candidate'].includes(e.kind) && e.value.key === result.key).length !== 1) return null
  const observations = cited.filter(e => e.kind === 'observation'), byKey = new Map<string, Entry>()
  const duplicate = new Set<string>()
  for (const e of observations) { const key = String(e.value.key); if (byKey.has(key)) duplicate.add(key); else byKey.set(key, e) }
  const keys = new Set(strings(candidates[0].value.evidence_keys))
  // Follow explicit observation links in either direction, but reverse only entity/voice membership.
  // Never walk from an entity to unrelated entities sharing its shot.
  const selected = [...keys].flatMap(k => !duplicate.has(k) && byKey.has(k) ? [byKey.get(k)!] : [])
  const linked = selected.flatMap(e => Object.values(record(e.value.links)).flatMap(strings))
  for (const key of linked) if (!duplicate.has(key) && byKey.has(key)) selected.push(byKey.get(key)!)
  for (const e of observations) {
    const links = record(e.value.links)
    if (['shots','speech'].includes(String(e.value.kind)) && [...strings(links.entities), ...strings(links.speakers)].some(k => keys.has(k)) && !duplicate.has(String(e.value.key))) selected.push(e)
  }
  const audio = ['sound_cue','music_structure'].includes(result.type_key)
  const valid = selected.filter(e => audio ? ['speech','music','sound_effects','intervals'].includes(String(e.value.kind)) : ['shots','beats'].includes(String(e.value.kind)))
  const ranges = new Map<string, ReferenceRange>()
  for (const e of valid) {
    const value = record(e.value.value)
    const note = typeof value.description === 'string' ? value.description : typeof value.text === 'string' ? `原语对白（未翻译）：${value.text}` : typeof value.subject_motion === 'string' ? value.subject_motion : ''
    const r = range(e.value.scope, duration, note)
    if (r) { const key = `${r.start}:${r.end}`; if (!ranges.has(key) || !ranges.get(key)!.note) ranges.set(key, r) }
  }
  return ranges.size ? { source, sha256: info.sha256, ranges: [...ranges.values()].sort((a,b) => a.start-b.start || a.end-b.end) } : null
}
export function timeLabel(seconds: number): string {
  const minutes = Math.floor(seconds / 60), rest = (seconds % 60).toFixed(1).padStart(4,'0')
  return `${String(minutes).padStart(2,'0')}:${rest}`
}
