<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { resultRouteBase, resultRouteKey } from '../platform/route-links'
import { useHost } from '../platform/host'
import WorldTrail, { type TrailItem } from './WorldTrail.vue'
import { resultTypes, promptVariants, type LearningResult } from '../platform/learning-results'
import { useResultBrowser, resultSummary, resultPageSize } from '../platform/result-browser'
const props = defineProps<{ results: LearningResult[]; parents?: TrailItem[]; label?: string; code?: (result: LearningResult) => string }>()
const { type, query, page, pages, visible, filtered, selected, index, active } = useResultBrowser(() => props.results, r=>props.code?.(r)||'')
const root = ref<HTMLElement | null>(null), heading = ref<HTMLElement | null>(null)
const counts = computed(() => {
  const values = new Map<string, number>()
  for (const r of props.results) values.set(r.type_key, (values.get(r.type_key) || 0) + 1)
  return [...values]
})
function models(result: LearningResult) {
  return result.type_key === 'prompt' && result.type_version === 1 ? [...new Set(promptVariants(result).map(v => v.requested_label))].join(' · ') : ''
}
const route=useRoute(),router=useRouter(),{appSDK}=useHost()
const base=computed(()=>resultRouteBase(route.path))
const missing=computed(()=>!!resultRouteKey(route.path)&&!active.value)
function stopMedia(){root.value?.querySelectorAll<HTMLMediaElement>('audio,video').forEach(media=>media.pause())}
function routeFilters(){return {...route.query,result_type:type.value||undefined,result_q:query.value||undefined,result_page:page.value>1?String(page.value):undefined}}
function restoreRoute(){
  stopMedia()
  type.value=typeof route.query.result_type==='string'?route.query.result_type:''
  query.value=typeof route.query.result_q==='string'?route.query.result_q:''
  const number=Number(route.query.result_page||1)
  page.value=Number.isSafeInteger(number)&&number>0?Math.min(number,pages.value):1
  selected.value=resultRouteKey(route.path)
}
watch(()=>[route.fullPath,props.results],restoreRoute,{immediate:true,flush:'sync'})
const stopLifecycle=appSDK.lifecycle.subscribe(event=>{if(event==='hidden'||event==='dispose')stopMedia()})
onBeforeUnmount(()=>{stopMedia();stopLifecycle()})
let returnKey = ''
async function rememberFilters(){
  await nextTick()
  const target={path:base.value,query:routeFilters()}
  if(router.resolve(target).fullPath!==route.fullPath)await router.replace(target)
}
function changeType(value:string){type.value=value;void rememberFilters()}
function clearFilters(){type.value='';query.value='';void rememberFilters()}
async function open(result: LearningResult) {
  returnKey=result.key;stopMedia();await rememberFilters()
  await router.push({path:(base.value==='/'?'':base.value)+'/item/'+result.key,query:routeFilters()})
  await nextTick();heading.value?.focus({preventScroll:true});root.value?.scrollIntoView({block:'start'})
}
async function back() {
  const key=selected.value||returnKey;stopMedia()
  await router.push({path:base.value,query:routeFilters()});await nextTick()
  const card=Array.from(root.value?.querySelectorAll<HTMLButtonElement>('[data-result-key]')||[]).find(el=>el.dataset.resultKey===key)
  card?.focus({preventScroll:true});card?.scrollIntoView({block:'center'})
}
async function adjacent(delta:number){
  const result=filtered.value[index.value+delta];if(!result)return;stopMedia()
  const targetPage=Math.floor((index.value+delta)/resultPageSize)+1
  await router.push({path:(base.value==='/'?'':base.value)+'/item/'+result.key,query:{...routeFilters(),result_page:targetPage>1?String(targetPage):undefined}})
  await nextTick();heading.value?.focus({preventScroll:true});root.value?.scrollIntoView({block:'start'})
}
async function changePage(delta:number){page.value+=delta;await rememberFilters();await nextTick();root.value?.scrollIntoView({block:'start'});root.value?.querySelector<HTMLButtonElement>('[data-result-key]')?.focus({preventScroll:true})}
</script>
<template><div ref="root" class="result-browser">
<WorldTrail :items="[...(parents||[{label:'数字世界',to:'/'},{label:'拆解作品',to:'/learn'}]),{label:label||'拆解成果'},...(active?[{label:(code?.(active)||resultTypes[active.type_key]||'成果')+' · '+active.title}]:[])]" @back="back"><template v-if="active"><span>{{index+1}} / {{filtered.length}}</span><button class="btn" :disabled="index<=0" @click="adjacent(-1)">上一项</button><button class="btn" :disabled="index>=filtered.length-1" @click="adjacent(1)">下一项</button></template></WorldTrail>
<section v-if="missing" class="empty" role="status"><h3>没有找到这项成果</h3><p>成果编号不在当前固定版本或筛选范围内。返回列表后可重新查找。</p><button class="btn" @click="back">返回拆解成果</button></section>
<template v-else-if="!active">
<slot name="intro"/>
<div class="browser-tools"><label class="search">查找成果<input v-model="query" type="search" @change="rememberFilters" @keyup.enter="rememberFilters" maxlength="256" placeholder="搜索编号、名称或适用模型"></label><button v-if="type||query" class="btn" @click="clearFilters">清除筛选</button></div>
<nav class="type-filters" aria-label="成果分类"><button :aria-pressed="!type" @click="changeType('')">全部 <span>{{results.length}}</span></button><button v-for="[key,count] in counts" :key="key" :aria-pressed="type===key" @click="changeType(key)">{{resultTypes[key]||'其他成果'}} <span>{{count}}</span></button></nav>
<div class="browser-summary" role="status"><span>{{type ? resultTypes[type]||'其他成果' : '全部成果'}} <strong>{{filtered.length}}</strong> 项</span><span v-if="filtered.length">{{(page-1)*resultPageSize+1}}–{{Math.min(page*resultPageSize,filtered.length)}} / {{filtered.length}}</span></div>
<div v-if="!filtered.length" class="empty"><h3>没有找到匹配成果</h3><p>试试其他名称或模型，也可以清除筛选查看全部。</p><button class="btn" @click="clearFilters">显示全部成果</button></div>
<div v-else class="result-grid"><button v-for="r in visible" :key="r.key" class="result-card" :data-result-key="r.key" @click="open(r)"><slot name="thumbnail" :result="r"/><span class="card-kind">{{code?.(r)||resultTypes[r.type_key]||'其他成果'}}</span><strong class="card-title">{{r.title}}</strong><span class="card-summary">{{resultSummary(r)}}</span><span class="card-footer"><span>{{models(r)||'查看内容与使用说明'}}</span><span aria-hidden="true">↗</span></span></button></div>
<nav v-if="pages>1" class="pagination" aria-label="成果分页"><button class="btn" :disabled="page===1" @click="changePage(-1)">上一页</button><span>第 {{page}} / {{pages}} 页</span><button class="btn" :disabled="page===pages" @click="changePage(1)">下一页</button></nav>
</template>
<section v-else-if="active" class="result-detail">
<header class="detail-heading"><span>{{code?.(active)||resultTypes[active.type_key]||'其他成果'}}</span><h3 ref="heading" tabindex="-1">{{active.title}}</h3></header>
<div :key="active.key" class="detail-body"><slot :result="active"/></div>
</section>
</div></template>
<style scoped>
.type-filters{display:flex;flex-wrap:wrap;gap:8px;margin:0 0 20px}.type-filters button{border:1px solid var(--line);border-radius:20px;padding:8px 13px;background:var(--shell-panel);color:var(--muted);font:inherit;font-size:12px;cursor:pointer}.type-filters button[aria-pressed=true]{background:var(--paper);color:var(--button-ink);border-color:var(--paper)}.type-filters span{margin-left:5px;opacity:.7}.type-filters button:focus-visible{outline:2px solid var(--accent);outline-offset:3px}.result-browser{min-width:0;scroll-margin-top:24px}.browser-tools{display:flex;gap:16px;align-items:end;flex-wrap:wrap;margin:24px 0}.browser-tools label{display:grid;gap:8px;font-size:12px;color:var(--muted);min-width:0}.search{flex:1;max-width:480px}input,select{width:100%;box-sizing:border-box;padding:12px;border:1px solid var(--line);border-radius:9px;background:var(--shell-button);color:var(--ink);font:inherit;min-width:0}.browser-summary{display:flex;justify-content:space-between;gap:12px;color:var(--muted);font-size:12px;margin:0 0 16px}.browser-summary strong{color:var(--ink);font-weight:600;margin-left:8px}.result-grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:16px}.result-card{display:flex;flex-direction:column;gap:14px;min-width:0;text-align:left;padding:22px;border:1px solid var(--line);border-radius:14px;background:var(--shell-panel);color:var(--ink);font:inherit;cursor:pointer;transition:border-color .15s,background .15s}.result-card:hover{border-color:var(--accent);background:color-mix(in srgb,var(--accent) 4%,var(--shell-panel))}.result-card:focus-visible,.btn:focus-visible{outline:2px solid var(--accent);outline-offset:3px}.card-kind{align-self:start;padding:5px 9px;border-radius:6px;background:color-mix(in srgb,var(--accent) 8%,var(--shell-panel));color:var(--accent);font-size:11px}.card-title{font-size:16px;font-weight:600;line-height:1.6;display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden;overflow-wrap:anywhere}.card-summary{font-size:12px;line-height:1.8;color:var(--muted);display:-webkit-box;-webkit-line-clamp:3;-webkit-box-orient:vertical;overflow:hidden;overflow-wrap:anywhere}.card-footer{display:flex;justify-content:space-between;gap:10px;border-top:1px solid var(--line);padding-top:14px;margin-top:auto;color:var(--muted);font-size:11px;line-height:1.6}.card-footer>span:first-child{display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden;overflow-wrap:anywhere}.pagination{display:flex;gap:20px;align-items:center;justify-content:center;margin:26px 0;font-size:12px;color:var(--muted)}.detail-nav{max-width:none;display:flex;justify-content:space-between;gap:14px;align-items:center;flex-wrap:wrap;margin:24px auto}.detail-nav>div{display:flex;gap:10px;align-items:center;font-size:12px;color:var(--muted)}.detail-heading{max-width:none;margin:auto;padding:22px 0;border-bottom:1px solid var(--line)}.detail-heading>span{font-size:12px;color:var(--accent)}.detail-heading h3{font-size:clamp(20px,2vw,28px);line-height:1.5;margin:10px 0;overflow-wrap:anywhere;scroll-margin-top:20px}.detail-body{padding:24px 0;min-width:0;max-width:none;margin:auto}.empty{padding:48px 24px;text-align:center;border:1px dashed var(--line);border-radius:14px}.empty p{font-size:13px;color:var(--muted);line-height:1.8}.btn:disabled{opacity:.4;cursor:default}@media(max-width:1100px){.result-grid{grid-template-columns:repeat(2,minmax(0,1fr))}}@media(max-width:620px){.result-grid{grid-template-columns:1fr}.browser-tools{align-items:stretch}.browser-tools label{width:100%;max-width:none}.result-card{padding:18px}.detail-nav>div{width:100%;justify-content:space-between}.detail-body{padding-top:18px}}@media(prefers-reduced-motion:reduce){.result-card{transition:none}}
</style>
