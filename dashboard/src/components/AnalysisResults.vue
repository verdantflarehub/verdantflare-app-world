<script setup lang="ts">
import { ref } from 'vue'
import { type LearningDocument, type LearningResult } from '../platform/learning-results'
import type { DraftRevision } from '../platform/draft-model'
import ResultFields from './ResultFields.vue'
import AgentReferenceCopy from './AgentReferenceCopy.vue'
import HumanResult from './HumanResult.vue'
import ResultReferenceMedia from './ResultReferenceMedia.vue'
import { provideReferenceMedia } from '../platform/reference-media'
import { resultCode, businessOrder } from '../platform/result-locator'
import ResultBrowser from './ResultBrowser.vue'
import AnalysisPrompt from './AnalysisPrompt.vue'
import ResultContext from './ResultContext.vue'
const props=defineProps<{document:LearningDocument;revision:DraftRevision;sourceName?:string}>()
provideReferenceMedia()
const copyText=ref(''),copied=ref(false)
async function copy(text:string){copyText.value=text;copied.value=false;try{await navigator.clipboard.writeText(text);copied.value=true}catch{/* Manual selectable copy remains available. */}}
function config(r:LearningResult){return JSON.stringify({source:{world_draft_id:props.revision.draft_id,world_draft_revision_id:props.revision.revision_id,result_key:r.key},result:r},null,2)}
</script>
<template><section aria-label="已保存的学习成果" class="results">
<ResultBrowser :results="businessOrder(document.results)" :code="r=>resultCode(r,document!)">
<template #intro><div class="section-head"><div><h2>拆解成果</h2><p v-if="sourceName" class="source-name">{{sourceName}}</p><p class="muted">{{document.results.length}} 项参考内容 · 查看画面、试听与创作方法</p></div></div><p class="muted">原作品与拆解内容保持只读，复制参考后在项目中创作。</p></template>
<template #thumbnail="{result}"><ResultReferenceMedia v-if="['character','scene','prop','shot'].includes(result.type_key)" :result="result" :document="document" thumbnail/></template>
<template #default="{result}">

<AnalysisPrompt v-if="result.type_key==='prompt'&&result.type_version===1" :result="result" @copy="copy"/>
<HumanResult v-else :result="result" :document="document" :source-label="`世界学习记录 ${revision.draft_id} / 修订 ${revision.revision_id}`"/>
<AgentReferenceCopy v-if="result.type_key==='prompt'" :result="result" :document="document" :source-label="`世界学习记录 ${revision.draft_id} / 修订 ${revision.revision_id}`"/>
<details><summary>来源与技术数据</summary><ResultContext :result="result" :document="document"/><button class="btn" @click="copy(config(result))">复制原始技术数据</button></details>
</template>
</ResultBrowser>
<section v-if="copyText" aria-live="polite" class="copy"><p>{{copied?'已复制。':'可选中下方文本手动复制。'}}</p><details :open="!copied"><summary>查看复制内容</summary><textarea readonly :value="copyText" rows="5" aria-label="可复制的成果正文"/></details><button class="btn" @click="copyText=''">收起</button></section>
<details class="coverage"><summary>已分析的范围与仍需补齐的内容</summary><ResultFields :value="document.coverage"/></details>
</section></template>
<style scoped>
.source-name{font-size:15px;color:var(--ink);line-height:1.7;margin:8px 0;overflow-wrap:anywhere}.results{margin-top:28px;min-width:0}.section-head{display:flex;justify-content:space-between;align-items:center;gap:16px;flex-wrap:wrap}.section-head>.btn{flex-shrink:0}.muted{font-size:12px;color:var(--muted);line-height:1.8}.intro{font-size:13px;line-height:1.8;color:var(--muted);flex:1;min-width:180px;margin:0 0 24px;white-space:pre-wrap}details{margin-top:24px}summary{cursor:pointer;font-size:13px}.copy{margin-top:20px}.copy textarea{width:100%;box-sizing:border-box;padding:12px;border:1px solid var(--line);border-radius:8px;background:var(--shell-button);color:var(--ink);font:inherit}.coverage{padding:20px 0;border-top:1px solid var(--line)}.coverage[open]>summary{margin-bottom:24px}
</style>
