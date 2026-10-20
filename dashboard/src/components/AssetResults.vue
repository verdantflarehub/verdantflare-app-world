<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useHost } from '../platform/host'
import { createReadClient } from '../platform/client'
import { publicationIdentity } from '../platform/publication-client'
import { loadAssetResults, resultTypes, basisLabels, filterResults, type LearningResult, type LearningDocument } from '../platform/learning-results'
import type { AssetOpen } from '../platform/world-model'
import PromptResult from './PromptResult.vue'
import ResultFields from './ResultFields.vue'
const emit=defineEmits<{'new-project':[result:LearningResult,variant:string]}>()
const props=defineProps<{asset:AssetOpen}>(),{appSDK}=useHost(),client=createReadClient(appSDK,'world')
const document=ref<LearningDocument|null>(null),loading=ref(false),error=ref(''),type=ref(''),query=ref(''),selected=ref('')
const identity=publicationIdentity(appSDK.context.get())
let alive=true,hidden=false,generation=0,revoked=false
const counts=computed(()=>{const m=new Map<string,number>();for(const r of document.value?.results||[])m.set(r.type_key,(m.get(r.type_key)||0)+1);return [...m]})
const filtered=computed(()=>filterResults(document.value?.results||[],type.value,query.value))
const active=computed(()=>filtered.value.find(r=>r.key===selected.value)||filtered.value[0])
const modes:Record<string,string>={direct:'直接使用',method:'方法应用',reference:'参考学习',recreate:'再创作'}
const availability:Record<string,string>={unknown:'可用性待核对',ready:'文档标记为就绪',missing:'仍需补齐',unsupported:'当前不支持'}
async function load(){
 if(!alive||hidden||revoked)return
 const current=++generation;loading.value=true;error.value='';document.value=null
 try{const d=await loadAssetResults(client.call,props.asset);if(alive&&!hidden&&current===generation)document.value=d}
 catch(e){if(alive&&current===generation)error.value=e instanceof Error?e.message:'无法读取资产正文。'}
 finally{if(alive&&current===generation)loading.value=false}
}
const stop=appSDK.lifecycle.subscribe(e=>{if(e==='hidden'||e==='dispose'){hidden=true;generation++;document.value=null;loading.value=false}if(e==='active'){hidden=false;void load()}})
const stopContext=appSDK.context.subscribe(c=>{if(publicationIdentity(c)===identity)return;revoked=true;generation++;document.value=null;error.value='会话已变化，请重新打开数字世界。';loading.value=false})
onBeforeUnmount(()=>{alive=false;generation++;client.dispose();stop();stopContext()})
void load()
</script>
<template><section class="asset-results" aria-label="资产成果正文">
<div v-if="loading" class="state" role="status">正在读取固定版本的成果正文…</div>
<div v-else-if="error" class="state error" role="alert"><p>{{error}}</p><button class="btn" @click="load">重新读取正文</button></div>
<div v-else-if="!document" class="state">这份资产没有类型化成果正文，已保存文件见下方清单。</div>
<template v-else><header><div><h2>可以复用的成果</h2><p>共 {{document.results.length}} 项 · 当前资产的固定版本</p></div></header>
<div v-if="!document.results.length" class="state">正文中尚未包含拆解或创作成果。</div>
<template v-else><div class="filters"><label>成果类型<select v-model="type"><option value="">全部成果</option><option v-for="[key,count] in counts" :key="key" :value="key">{{resultTypes[key]||key}} · {{count}}</option></select></label><label>搜索<input v-model="query" type="search" maxlength="256" placeholder="名称、模型或版本"></label></div>
<div v-if="!filtered.length" class="state">没有匹配的成果。<button class="btn" @click="type='';query=''">显示全部成果</button></div>
<div v-else class="results-layout"><nav aria-label="选择成果"><button v-for="r in filtered" :key="r.key" :class="{selected:active?.key===r.key}" :aria-current="active?.key===r.key?'true':undefined" @click="selected=r.key"><span>{{resultTypes[r.type_key]||r.type_key}}</span><strong>{{r.title}}</strong></button></nav>
<article v-if="active" :key="active.key"><div class="result-heading"><span>{{resultTypes[active.type_key]||active.type_key}}</span><h3>{{active.title}}</h3></div>
<div v-if="active.use_conditions.length" class="uses"><div v-for="(use,i) in active.use_conditions" :key="i"><strong>{{modes[use.mode]||use.mode}} · {{use.purpose}}</strong><p>{{availability[use.availability]||use.availability}}</p><p v-if="use.required_inputs.length">需要：{{use.required_inputs.join('、')}}</p><p v-if="use.missing_items.length" class="missing">待补齐：{{use.missing_items.join('；')}}</p></div></div>
<PromptResult v-if="active.type_key==='prompt'&&active.type_version===1" :result="active" :asset="asset" :document="document" @new-project="variant=>emit('new-project',active,variant)"/>
<template v-else><p v-if="active.type_version!==1||!resultTypes[active.type_key]" class="notice">当前页面尚未适配此成果类型或版本，以下保留原始字段供查阅。</p><ResultFields :value="active.payload"/></template>
<details class="basis"><summary>来源依据 · {{basisLabels[active.basis]||active.basis}}</summary><p>{{active.basis_note}}</p><h4>来源引用</h4><ResultFields :value="active.source_refs"/><h4>定位与证据</h4><ResultFields :value="active.evidence"/></details>
</article></div></template></template></section></template>
<style scoped>
.asset-results{margin:24px 0;color:var(--ink)}header{margin-bottom:18px}h2{font-size:21px;margin:0 0 8px}header p{font-size:12px;color:var(--muted)}.filters{display:flex;gap:16px;flex-wrap:wrap;margin-bottom:22px}.filters label{display:grid;gap:7px;font-size:12px;color:var(--muted)}select,input{padding:10px 12px;border:1px solid var(--line);border-radius:8px;background:var(--shell-button);color:var(--ink);font:inherit;min-width:220px}.results-layout{display:grid;grid-template-columns:240px minmax(0,1fr);gap:24px;align-items:start}nav{display:grid;gap:8px}nav button{display:grid;gap:7px;text-align:left;padding:16px;border:1px solid var(--line);border-radius:9px;background:var(--shell-panel);color:var(--ink)}nav button.selected{border-color:var(--accent);background:color-mix(in srgb,var(--accent) 8%,var(--shell-panel))}nav button span,.result-heading>span{color:var(--muted);font-size:11px}nav strong{font-size:13px;line-height:1.6;overflow-wrap:anywhere}article{min-width:0;border:1px solid var(--line);border-radius:12px;padding:26px;background:var(--shell-panel)}h3{font-size:24px;margin:10px 0 22px}.uses{display:grid;gap:12px;border-bottom:1px solid var(--line);padding-bottom:20px;margin-bottom:22px;font-size:12px}.uses strong{font-weight:600}.uses p{color:var(--muted);margin:8px 0;line-height:1.6}.uses .missing{color:var(--danger)}.basis{margin-top:24px;border-top:1px solid var(--line);padding-top:18px;color:var(--muted);font-size:12px}.basis p{white-space:pre-wrap;line-height:1.8}.basis h4{margin:20px 0 12px}summary{cursor:pointer}.state{padding:26px;border:1px dashed var(--line);border-radius:10px;color:var(--muted)}.error{color:var(--danger)}.notice{font-size:12px;color:var(--muted);margin-bottom:16px}@media(max-width:900px){.results-layout{grid-template-columns:1fr}article{padding:20px}nav{grid-template-columns:repeat(2,minmax(0,1fr))}}
</style>
