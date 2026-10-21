<script setup lang="ts">
import { ref } from 'vue'
import { type LearningDocument, type LearningResult } from '../platform/learning-results'
import type { DraftRevision } from '../platform/draft-model'
import ResultFields from './ResultFields.vue'
import ResultBrowser from './ResultBrowser.vue'
import AnalysisPrompt from './AnalysisPrompt.vue'
import ResultContext from './ResultContext.vue'
const props=defineProps<{document:LearningDocument;revision:DraftRevision}>()
const copyText=ref(''),copied=ref(false)
async function copy(text:string){copyText.value=text;copied.value=false;try{await navigator.clipboard.writeText(text);copied.value=true}catch{/* Manual selectable copy remains available. */}}
function config(r:LearningResult){return JSON.stringify({source:{world_draft_id:props.revision.draft_id,world_draft_revision_id:props.revision.revision_id,result_key:r.key},result:r},null,2)}
</script>
<template><section aria-label="已保存的学习成果" class="results">
<div class="section-head"><div><h2>这次沉淀了什么</h2><p class="muted">{{document.results.length}} 项分类成果 · 已固定保存到数字世界</p></div><button class="btn" @click="copy(JSON.stringify(document,null,2))">复制完整学习文档</button></div>
<p class="muted">按类型查找，打开一项查看完整内容。修改与创作在项目中完成。</p>
<ResultBrowser :results="document.results" v-slot="{result}">
<div class="section-head"><p class="intro">{{result.basis_note}}</p><button class="btn" @click="copy(config(result))">复制成果与来源</button></div>
<AnalysisPrompt v-if="result.type_key==='prompt'&&result.type_version===1" :result="result" @copy="copy"/>
<ResultFields v-else :value="result.payload"/>
<ResultContext :result="result"/>
</ResultBrowser>
<section v-if="copyText" aria-live="polite" class="copy"><p>{{copied?'已复制。':'可选中下方文本手动复制。'}}</p><details :open="!copied"><summary>查看复制内容</summary><textarea readonly :value="copyText" rows="5" aria-label="可复制的成果正文"/></details><button class="btn" @click="copyText=''">收起</button></section>
<details class="coverage"><summary>已分析的范围与仍需补齐的内容</summary><ResultFields :value="document.coverage"/></details>
</section></template>
<style scoped>
.results{margin-top:28px;min-width:0}.section-head{display:flex;justify-content:space-between;align-items:center;gap:16px;flex-wrap:wrap}.section-head>.btn{flex-shrink:0}.muted{font-size:12px;color:var(--muted);line-height:1.8}.intro{font-size:13px;line-height:1.8;color:var(--muted);flex:1;min-width:180px;margin:0 0 24px;white-space:pre-wrap}details{margin-top:24px}summary{cursor:pointer;font-size:13px}.copy{margin-top:20px}.copy textarea{width:100%;box-sizing:border-box;padding:12px;border:1px solid var(--line);border-radius:8px;background:var(--shell-button);color:var(--ink);font:inherit}.coverage{padding:20px 0;border-top:1px solid var(--line)}.coverage[open]>summary{margin-bottom:24px}
</style>
