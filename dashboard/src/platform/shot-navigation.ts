import { isID, readAssetList, type AssetItem } from './world-model.ts'
import { canonicalRoute } from './route-links.ts'
export interface ShotList { path: string; items: AssetItem[]; cursor: string; scroll: number; subject?: string }
const storageKey = 'vf.world.shot-list.v1'
type SnapshotStorage = Pick<Storage, 'getItem' | 'setItem'>
export function createShotListMemory(storage?: SnapshotStorage) {
  const lists = new Map<string, ShotList>()
  function hydrate() {
    if (!storage) return
    try {
      const raw = storage.getItem(storageKey)
      if (!raw || raw.length > 2 * 1024 * 1024) return
      const entries: unknown = JSON.parse(raw)
      if (!Array.isArray(entries) || entries.length > 8) return
      for (const entry of entries) {
        if (!Array.isArray(entry) || entry.length !== 2 || typeof entry[0] !== 'string' || entry[0].length > 4096) continue
        const value = entry[1] as ShotList
        if (!value || typeof value.path !== 'string' || canonicalRoute('world', value.path) !== value.path || !['/', '/character', '/scene', '/sound', '/music', '/shot', '/script', '/knowledge'].includes(new URL(value.path, 'https://app.invalid').pathname)
          || !Array.isArray(value.items) || value.items.length > 1000 || !Number.isFinite(value.scroll) || value.scroll < 0 || (value.cursor !== '' && !isID(value.cursor)) || (value.subject !== undefined && typeof value.subject !== 'string')) continue
        const seen = new Set<string>()
        for (let i = 0; i < value.items.length; i += 100) readAssetList({items: value.items.slice(i, i + 100), next_cursor: ''})
        if (value.items.some(item => seen.has(item.asset_id) || !seen.add(item.asset_id))) continue
        lists.set(entry[0], value)
      }
    } catch { /* Invalid or unavailable browser storage cannot break navigation. */ }
  }
  return {
    remember(identity: string, list: ShotList) {
      const snapshot = JSON.parse(JSON.stringify(list)) as ShotList
      lists.delete(identity); lists.set(identity, snapshot)
      while (lists.size > 8) lists.delete(lists.keys().next().value!)
      try { const raw = JSON.stringify([...lists]); if (raw.length <= 2 * 1024 * 1024) storage?.setItem(storageKey, raw) } catch { /* Memory remains available. */ }
    },
    recall(identity: string, path?: string): ShotList | undefined {
      if (!lists.has(identity)) hydrate()
      const value = lists.get(identity)
      return value && (!path || path === value.path) ? JSON.parse(JSON.stringify(value)) as ShotList : undefined
    },
  }
}
function browserStorage(): SnapshotStorage | undefined { try { return globalThis.sessionStorage } catch { return undefined } }
const memory = createShotListMemory(browserStorage())
export const rememberShotList = memory.remember
export const recallShotList = memory.recall
export function shotPath(item: Pick<AssetItem, 'asset_id' | 'head_asset_version_id'>): string {
  return `/shot/asset/${item.asset_id}?version=${item.head_asset_version_id}`
}

/** Skip overlapping pages without losing the fixed versions already offered to the user. */
export async function extendShotList(list: ShotList, read: (cursor: string) => Promise<{items: AssetItem[]; next_cursor: string}>): Promise<ShotList> {
  let current = list
  const visited = new Set<string>()
  for (let count = 0; current.cursor && count < 25; count++) {
    if (visited.has(current.cursor)) throw Error('分页没有继续，请重试。')
    visited.add(current.cursor)
    const page = await read(current.cursor)
    if (page.next_cursor && visited.has(page.next_cursor)) throw Error('分页没有继续，请重试。')
    const seen = new Set(current.items.map(item => item.asset_id))
    const items = [...current.items]
    for (const item of page.items) if (!seen.has(item.asset_id)) { seen.add(item.asset_id); items.push(item) }
    current = {...current, items, cursor: page.next_cursor}
    if (items.length > list.items.length || !current.cursor) return current
  }
  throw Error('分页没有继续，请重试。')
}
