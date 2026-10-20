<script setup lang="ts">
import { computed, ref } from 'vue'
import { filterResults, resultTypes, promptVariants, promptBody, compatibilityLabels, type LearningDocument, type LearningResult } from '../platform/learning-results'
import type { DraftRevision } from '../platform/draft-model'
import ResultFields from './ResultFields.vue'
const props=defineProps<{document:LearningDocument;revision:DraftRevision}>()
const type=ref(''),query=ref(''),selected=ref(''),copyText=ref(''),copied=ref(false)
const filtered=computed(()=>filterResults(props.document.results,type.value,query.value))
const active=computed(()=>filtered.value.find(r=>r.key===selected.value)||filtered.value[0])
const kinds=computed(()=>[...new Set(props.document.results.map(r=>r.type_key))])
async function copy(text:string){copyText.value=text;copied.value=false;try{await navigator.clipboard.writeText(text);copied.value=true}catch{/* Manual selectable copy remains available. */}}
function config(r:LearningResult){return JSON.stringify({source:{world_draft_id:props.revision.draft_id,world_draft_revision_id:props.revision.revision_id,result_key:r.key},result:r},null,2)}
</script>
<template><section aria-label="已保存的学习成果" class="results">
<div class="section-head"><div><h2>这次沉淀了什么</h2><p class="muted">{{document.results.length}} 项分类成果 · 已固定保存到数字世界</p></div><button class="btn" @click="copy(JSON.stringify(document,null,2))">复制完整学习文档</button></div>
<p class="muted">可以直接阅读和复制；修改或生成新内容时，在项目中通过智能体使用。</p>
<div class="filters"><label>成果类型<select v-model="type"><option value="">全部成果</option><option v-for="kind in kinds" :key="kind" :value="kind">{{resultTypes[kind]||kind}}</option></select></label><label>查找成果<input v-model="query" type="search" placeholder="名称、提示词或模型"></label></div>
<p v-if="!filtered.length" class="muted">没有匹配的成果。</p>
<div v-else class="result-layout"><nav aria-label="拆解成果列表"><button v-for="r in filtered" :key="r.key" :class="{selected:r.key===active?.key}" @click="selected=r.key"><small>{{resultTypes[r.type_key]||r.type_key}}</small><strong>{{r.title}}</strong></button></nav><article v-if="active" :key="active.key"><h3>{{active.title}}</h3><p>{{active.basis_note}}</p><button class="btn" @click="copy(config(active))">复制成果与来源</button>
<template v-if="active.type_key==='prompt'&&active.type_version===1"><section v-for="variant in promptVariants(active)" :key="variant.key" class="prompt"><div class="section-head"><strong>{{variant.requested_label}}</strong><button class="btn" @click="copy(promptBody(variant))">复制提示词</button></div><p class="muted">{{variant.model_id||'实际模型待确认'}} · {{compatibilityLabels[variant.compatibility]}}</p><pre>{{promptBody(variant)}}</pre><p v-if="variant.negative_prompt">负向提示：{{variant.negative_prompt}}</p><p class="muted">{{variant.quality_review.note}}</p><p v-if="!variant.execution_evidence.length" class="muted">暂无该目标模型的真实生成样例。</p></section></template>
<ResultFields v-else :value="active.payload"/>
<details><summary>使用条件与来源依据</summary><ResultFields :value="active.use_conditions"/><ResultFields :value="active.source_refs"/><ResultFields :value="active.evidence"/></details>
</article></div>
<section v-if="copyText" aria-live="polite" class="copy"><p>{{copied?'已复制。':'可选中下方文本手动复制。'}}</p><textarea readonly :value="copyText" rows="5" aria-label="可复制的成果正文"/><button class="btn" @click="copyText=''">收起</button></section>
<details class="coverage"><summary>已分析的范围与仍需补齐的内容</summary><ResultFields :value="document.coverage"/></details>
</section></template>
<style scoped>
.results{margin-top:28px}.section-head{display:flex;justify-content:space-between;align-items:center;gap:16px}.muted{font-size:12px;color:var(--muted);line-height:1.8}.filters{display:flex;flex-wrap:wrap;gap:16px;margin:24px 0}label{display:grid;gap:8px;font-size:12px}input,select,textarea{padding:12px;border:1px solid var(--line);border-radius:8px;background:var(--shell-button);color:var(--ink);font:inherit}.result-layout{display:grid;grid-template-columns:240px minmax(0,1fr);gap:24px;align-items:start}nav{display:grid;gap:8px}nav button{display:grid;gap:8px;text-align:left;padding:16px;border:1px solid var(--line);border-radius:8px;background:var(--shell-panel);color:var(--ink)}nav button.selected{border-color:var(--accent)}small{color:var(--muted)}article{min-width:0;padding:24px;border:1px solid var(--line);border-radius:10px}h3{font-size:22px}article>p{line-height:1.8;white-space:pre-wrap}pre{white-space:pre-wrap;overflow-wrap:anywhere;font:inherit;line-height:1.8}.prompt{margin-top:20px;border-top:1px solid var(--line);padding-top:20px}details{margin-top:24px}summary{cursor:pointer;font-size:13px}.copy{margin-top:20px}.copy textarea{width:100%;box-sizing:border-box}.coverage{padding:20px 0}@media(max-width:800px){.result-layout{grid-template-columns:1fr}.section-head{align-items:start;flex-wrap:wrap}article{padding:18px}}
</style>
