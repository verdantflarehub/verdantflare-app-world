import { computed, ref, watch } from 'vue'
import { filterResults, type LearningResult } from './learning-results.ts'

export const resultPageSize = 12
export function useResultBrowser(results: () => LearningResult[], code: (result: LearningResult) => string = () => '') {
  const type = ref(''), query = ref(''), page = ref(1), selected = ref('')
  const filtered = computed(() => { const q=query.value.trim().toLocaleLowerCase();const matches=new Set(filterResults(results(),type.value,query.value).map(r=>r.key));return results().filter(r=>(!type.value||r.type_key===type.value)&&(matches.has(r.key)||!!q&&code(r).toLocaleLowerCase().includes(q))) })
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
  for (const key of ['creative_intent', 'description', 'identity_notes', 'appearance', 'premise', 'objective', 'setting', 'composition', 'function', 'transcript', 'delivery', 'text']) {
    const value = result.payload[key]
    if (typeof value === 'string' && value.trim()) return value
  }
  return result.basis_note || result.use_conditions.map(u => u.purpose).join(' · ')
}
