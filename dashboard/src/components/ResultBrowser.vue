<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { resultTypes, promptVariants, type LearningResult } from '../platform/learning-results'
import { useResultBrowser, resultSummary, resultPageSize } from '../platform/result-browser'
const props = defineProps<{ results: LearningResult[] }>()
const { type, query, page, pages, visible, filtered, selected, index, active, move } = useResultBrowser(() => props.results)
const root = ref<HTMLElement | null>(null), heading = ref<HTMLElement | null>(null)
const counts = computed(() => {
  const values = new Map<string, number>()
  for (const r of props.results) values.set(r.type_key, (values.get(r.type_key) || 0) + 1)
  return [...values]
})
function models(result: LearningResult) {
  return result.type_key === 'prompt' && result.type_version === 1 ? [...new Set(promptVariants(result).map(v => v.requested_label))].join(' · ') : ''
}
let returnKey = ''
async function open(result: LearningResult) { returnKey = result.key; selected.value = result.key; await nextTick(); heading.value?.focus(); heading.value?.scrollIntoView({ block: 'start' }) }
async function back() {
  const key = selected.value === returnKey ? returnKey : selected.value
  selected.value = ''; await nextTick()
  const card = Array.from(root.value?.querySelectorAll<HTMLButtonElement>('[data-result-key]') || []).find(el => el.dataset.resultKey === key)
  card?.focus({ preventScroll: true }); card?.scrollIntoView({ block: 'center' })
}
async function adjacent(delta: number) { move(delta); await nextTick(); heading.value?.focus(); heading.value?.scrollIntoView({ block: 'start' }) }
async function changePage(delta: number) { page.value += delta; await nextTick(); root.value?.scrollIntoView({ block: 'start' }); root.value?.querySelector<HTMLButtonElement>('[data-result-key]')?.focus({ preventScroll: true }) }
</script>
<template><div ref="root" class="result-browser">
<template v-if="!active">
<div class="browser-tools"><label>成果分类<select v-model="type"><option value="">全部成果 · {{results.length}}</option><option v-for="[key,count] in counts" :key="key" :value="key">{{resultTypes[key]||'其他成果'}} · {{count}}</option></select></label><label class="search">查找成果<input v-model="query" type="search" maxlength="256" placeholder="搜索名称或适用模型"></label><button v-if="type||query" class="btn" @click="type='';query=''">清除筛选</button></div>
<div class="browser-summary" role="status"><span>{{type ? resultTypes[type]||'其他成果' : '全部成果'}} <strong>{{filtered.length}}</strong> 项</span><span v-if="filtered.length">{{(page-1)*resultPageSize+1}}–{{Math.min(page*resultPageSize,filtered.length)}} / {{filtered.length}}</span></div>
<div v-if="!filtered.length" class="empty"><h3>没有找到匹配成果</h3><p>试试其他名称或模型，也可以清除筛选查看全部。</p><button class="btn" @click="type='';query=''">显示全部成果</button></div>
<div v-else class="result-grid"><button v-for="r in visible" :key="r.key" class="result-card" :data-result-key="r.key" @click="open(r)"><span class="card-kind">{{resultTypes[r.type_key]||'其他成果'}}</span><strong class="card-title">{{r.title}}</strong><span class="card-summary">{{resultSummary(r)}}</span><span class="card-footer"><span>{{models(r)||'查看内容与使用说明'}}</span><span aria-hidden="true">↗</span></span></button></div>
<nav v-if="pages>1" class="pagination" aria-label="成果分页"><button class="btn" :disabled="page===1" @click="changePage(-1)">上一页</button><span>第 {{page}} / {{pages}} 页</span><button class="btn" :disabled="page===pages" @click="changePage(1)">下一页</button></nav>
</template>
<section v-else class="result-detail">
<div class="detail-nav"><button class="btn" @click="back">← 返回成果</button><div><span>{{index+1}} / {{filtered.length}}</span><button class="btn" :disabled="index<=0" @click="adjacent(-1)">上一项</button><button class="btn" :disabled="index>=filtered.length-1" @click="adjacent(1)">下一项</button></div></div>
<header class="detail-heading"><span>{{resultTypes[active.type_key]||'其他成果'}}</span><h3 ref="heading" tabindex="-1">{{active.title}}</h3></header>
<div :key="active.key" class="detail-body"><slot :result="active"/></div>
</section>
</div></template>
<style scoped>
.result-browser{min-width:0;scroll-margin-top:24px}.browser-tools{display:flex;gap:16px;align-items:end;flex-wrap:wrap;margin:24px 0}.browser-tools label{display:grid;gap:8px;font-size:12px;color:var(--muted);min-width:0}.search{flex:1;max-width:480px}input,select{width:100%;box-sizing:border-box;padding:12px;border:1px solid var(--line);border-radius:9px;background:var(--shell-button);color:var(--ink);font:inherit;min-width:0}.browser-summary{display:flex;justify-content:space-between;gap:12px;color:var(--muted);font-size:12px;margin:0 0 16px}.browser-summary strong{color:var(--ink);font-weight:600;margin-left:8px}.result-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px}.result-card{display:flex;flex-direction:column;gap:14px;min-width:0;text-align:left;padding:22px;border:1px solid var(--line);border-radius:14px;background:var(--shell-panel);color:var(--ink);font:inherit;cursor:pointer;transition:border-color .15s,background .15s}.result-card:hover{border-color:var(--accent);background:color-mix(in srgb,var(--accent) 4%,var(--shell-panel))}.result-card:focus-visible,.btn:focus-visible{outline:2px solid var(--accent);outline-offset:3px}.card-kind{align-self:start;padding:5px 9px;border-radius:6px;background:color-mix(in srgb,var(--accent) 8%,var(--shell-panel));color:var(--accent);font-size:11px}.card-title{font-size:16px;font-weight:600;line-height:1.6;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden;overflow-wrap:anywhere}.card-summary{font-size:12px;line-height:1.8;color:var(--muted);display:-webkit-box;-webkit-line-clamp:3;-webkit-box-orient:vertical;overflow:hidden;overflow-wrap:anywhere}.card-footer{display:flex;justify-content:space-between;gap:10px;border-top:1px solid var(--line);padding-top:14px;margin-top:auto;color:var(--muted);font-size:11px;line-height:1.6}.card-footer>span:first-child{display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden;overflow-wrap:anywhere}.pagination{display:flex;gap:20px;align-items:center;justify-content:center;margin:26px 0;font-size:12px;color:var(--muted)}.detail-nav{display:flex;justify-content:space-between;gap:14px;align-items:center;flex-wrap:wrap;margin:24px 0}.detail-nav>div{display:flex;gap:10px;align-items:center;font-size:12px;color:var(--muted)}.detail-heading{padding:22px 0;border-bottom:1px solid var(--line)}.detail-heading>span{font-size:12px;color:var(--accent)}.detail-heading h3{font-size:clamp(20px,2vw,28px);line-height:1.5;margin:10px 0;overflow-wrap:anywhere;scroll-margin-top:20px}.detail-body{padding:24px 0;min-width:0;max-width:1040px;margin:auto}.empty{padding:48px 24px;text-align:center;border:1px dashed var(--line);border-radius:14px}.empty p{font-size:13px;color:var(--muted);line-height:1.8}.btn:disabled{opacity:.4;cursor:default}@media(max-width:1100px){.result-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}@media(max-width:620px){.result-grid{grid-template-columns:1fr}.browser-tools{align-items:stretch}.browser-tools label{width:100%;max-width:none}.result-card{padding:18px}.detail-nav>div{width:100%;justify-content:space-between}.detail-body{padding-top:18px}}@media(prefers-reduced-motion:reduce){.result-card{transition:none}}
</style>
