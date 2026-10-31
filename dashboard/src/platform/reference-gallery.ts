import type { AssetItem } from './world-model'

// Pose and costume galleries: route query -> world.list arguments, facet
// requests and a per-query memory so returning from a detail page restores
// the loaded items and scroll position.
export type GalleryKind = 'pose' | 'costume'
export const ANGLES: Array<[string, string]> = [['front', '正面'], ['diag', '斜侧'], ['side', '侧面'], ['high', '俯视'], ['low', '仰视']]
export const PAGE_SIZE = 60

export interface GalleryFilters {
  sys: string; cat: string; people: string; view: string
  tab: 'outfit' | 'item'; kind: string; style: string; q: string
}
const one = (v: unknown) => typeof v === 'string' ? v.trim().slice(0, 128) : ''

export function readFilters(kind: GalleryKind, query: Record<string, unknown>): GalleryFilters {
  const view = one(query.v)
  return {
    sys: one(query.sys), cat: one(query.cat), people: ['单人', '双人'].includes(one(query.people)) ? one(query.people) : '',
    view: kind === 'pose' && ANGLES.some(a => a[0] === view) ? view : 'front',
    tab: one(query.tab) === 'item' ? 'item' : 'outfit', kind: one(query.kind), style: one(query.style), q: one(query.q),
  }
}

/** Exact "dimension:value" subjects for the current selection (see World / Pose §9, Costume §8). */
export function gallerySubjects(kind: GalleryKind, f: GalleryFilters): string[] {
  if (kind === 'pose') return [f.sys && `体系:${f.sys}`, f.cat && `动作:${f.cat}`, f.people && `人数:${f.people}`].filter(Boolean) as string[]
  if (f.tab === 'outfit') return ['范围:穿搭', f.cat && `分类:${f.cat}`].filter(Boolean) as string[]
  return ['范围:单品', f.kind && `类型:${f.kind}`, f.style && `画风:${f.style}`].filter(Boolean) as string[]
}

export function listArgs(kind: GalleryKind, f: GalleryFilters, cursor = ''): Record<string, unknown> {
  const subjects = gallerySubjects(kind, f)
  return { domain: kind, limit: PAGE_SIZE, ...(subjects.length ? { subjects } : {}), ...(f.q ? { q: f.q } : {}), ...(cursor ? { cursor } : {}), ...(kind === 'pose' && f.view !== 'front' ? { preview_hint: f.view } : {}) }
}

/**
 * Facet requests for the chip rows. Outer dimensions count over the whole
 * domain (or tab); inner dimensions count within the outer selection, so a
 * chip's number is what selecting it would show.
 */
export function facetRequests(kind: GalleryKind, f: GalleryFilters): Array<Record<string, unknown>> {
  if (kind === 'pose') return [
    { domain: kind, limit: 1, facets: ['体系', '人数'] },
    { domain: kind, limit: 1, facets: ['动作'], ...(f.sys ? { subjects: [`体系:${f.sys}`] } : {}) },
  ]
  return [
    { domain: kind, limit: 1, facets: ['范围'] },
    { domain: kind, limit: 1, subjects: [f.tab === 'item' ? '范围:单品' : '范围:穿搭'], facets: f.tab === 'item' ? ['类型', '画风'] : ['分类'] },
  ]
}

export function readFacets(value: unknown): { total: number; facets: Record<string, Record<string, number>> } {
  const data = (value && typeof value === 'object' ? value : {}) as Record<string, unknown>
  const facets: Record<string, Record<string, number>> = {}
  const raw = data.facets && typeof data.facets === 'object' ? data.facets as Record<string, unknown> : {}
  for (const [dimension, values] of Object.entries(raw)) {
    if (!values || typeof values !== 'object') continue
    facets[dimension] = {}
    for (const [k, n] of Object.entries(values as Record<string, unknown>)) if (typeof n === 'number' && n > 0) facets[dimension][k] = n
  }
  return { total: typeof data.total === 'number' && data.total >= 0 ? data.total : 0, facets }
}

/** Sorted chip values: most assets first, then by name for stable order. */
export function chipValues(counts: Record<string, number> | undefined): string[] {
  return Object.entries(counts || {}).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0], 'zh')).map(e => e[0])
}

export function subjectValue(item: AssetItem, dimension: string): string {
  const tag = item.subjects.find(s => s.startsWith(dimension + ':'))
  return tag ? tag.slice(dimension.length + 1) : ''
}

/** Distribute items into columns, each to the currently shortest one, keeping reading order. */
export function masonry<T>(items: T[], columns: number, ratio: (item: T) => number): T[][] {
  const cols: T[][] = Array.from({ length: Math.max(1, columns) }, () => [])
  const heights = cols.map(() => 0)
  for (const item of items) {
    const i = heights.indexOf(Math.min(...heights))
    cols[i].push(item); heights[i] += ratio(item) + 0.08
  }
  return cols
}

export interface GalleryMemory { items: AssetItem[]; cursor: string; total: number; scroll: number }
const memory = new Map<string, GalleryMemory>()
export const memoryKey = (kind: GalleryKind, f: GalleryFilters) => kind + ':' + JSON.stringify(listArgs(kind, f))
export function remember(key: string, value: GalleryMemory) { memory.delete(key); memory.set(key, value); while (memory.size > 20) memory.delete(memory.keys().next().value!) }
export function recall(key: string) { return memory.get(key) }
/** The most recent list a detail page was opened from, for previous/next. */
let lastList: { kind: GalleryKind; key: string; query: Record<string, string> } | null = null
export function setLastList(kind: GalleryKind, key: string, query: Record<string, string>) { lastList = { kind, key, query } }
export function getLastList(kind: GalleryKind) { return lastList && lastList.kind === kind ? lastList : null }
