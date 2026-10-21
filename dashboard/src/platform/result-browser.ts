import { computed, ref, watch } from 'vue'
import { filterResults, type LearningResult } from './learning-results.ts'

export const resultPageSize = 12
export function useResultBrowser(results: () => LearningResult[]) {
  const type = ref(''), query = ref(''), page = ref(1), selected = ref('')
  const filtered = computed(() => filterResults(results(), type.value, query.value))
  const pages = computed(() => Math.max(1, Math.ceil(filtered.value.length / resultPageSize)))
  const visible = computed(() => filtered.value.slice((page.value - 1) * resultPageSize, page.value * resultPageSize))
  const index = computed(() => filtered.value.findIndex(r => r.key === selected.value))
  const active = computed(() => filtered.value[index.value])
  watch([type, query], () => { page.value = 1; selected.value = '' }, { flush: 'sync' })
  watch(filtered, () => { page.value = Math.min(page.value, pages.value); if (index.value < 0) selected.value = '' }, { flush: 'sync' })
  function move(delta: number) {
    const target = index.value + delta, result = filtered.value[target]
    if (result) { selected.value = result.key; page.value = Math.floor(target / resultPageSize) + 1 }
  }
  return { type, query, page, pages, visible, filtered, selected, index, active, move }
}
export function resultSummary(result: LearningResult): string {
  for (const key of ['creative_intent', 'description', 'premise', 'objective', 'setting', 'composition', 'text']) {
    const value = result.payload[key]
    if (typeof value === 'string' && value.trim()) return value
  }
  return result.use_conditions.map(u => u.purpose).join(' · ') || result.basis_note
}
