<script setup lang="ts">
import { computed, ref } from 'vue'
import { type LearningDocument, type LearningResult, basisLabels } from '../platform/learning-results'
import { agentReference } from '../platform/result-locator'
import { resultReading, readingText } from '../platform/result-reading'
import { resultReference, timeLabel } from '../platform/result-reference'
import ResultReferenceMedia from './ResultReferenceMedia.vue'
const props=defineProps<{result:LearningResult;document:LearningDocument;sourceLabel:string;canUse?:boolean}>()
const emit=defineEmits<{'use-project':[]}>()
const reading=computed(()=>resultReading(props.result)),manual=ref(''),status=ref('')
const hasMedia=computed(()=>['character','scene','prop','shot','sound_cue','music_structure'].includes(props.result.type_key))
async function copy(forAgent=false){
 const ranges=resultReference(props.result,props.document)?.ranges||[]
 const text=forAgent?agentReference(props.result,props.document,props.sourceLabel):[readingText(props.result),ranges.length?'原片参考时段\n'+ranges.map(r=>`${timeLabel(r.start)}–${timeLabel(r.end)}${r.note?'：'+r.note:''}`).join('\n'):'',`固定来源\n${props.sourceLabel}\n成果：${props.result.key}`].filter(Boolean).join('\n\n')
 try{await navigator.clipboard.writeText(text);status.value=forAgent?'已复制指令，含固定版本与精确成果定位。':'已复制参考说明，可交给项目中的智能体使用。';manual.value=''}catch{manual.value=text;status.value='请选择下方正文复制。'}
}
</script>
<template><article class="human-result">
<p class="basis">{{basisLabels[result.basis]||'分析记录'}} · 自动分析，待核对</p>
<div class="content" :class="{'picture-layout':['character','scene','prop'].includes(result.type_key)}"><ResultReferenceMedia v-if="hasMedia" :result="result" :document="document"/>
<div class="explanation"><div class="reading" :class="{'reading-grid':hasMedia}"><section v-for="(section,i) in reading.sections" :key="i" :class="{'long-section':section.lines.join('').length>180}"><h4>{{section.title}}</h4><p v-if="section.lines.length===1">{{section.lines[0]}}</p><ol v-else-if="section.title==='操作步骤'"><li v-for="(line,j) in section.lines" :key="j">{{line}}</li></ol><ul v-else><li v-for="(line,j) in section.lines" :key="j">{{line}}</li></ul></section></div>
<section class="usage"><h4>这项可以怎么用</h4><p>{{reading.purpose}}</p><p class="limitation">{{reading.limitation}}</p><div class="actions"><button class="btn primary" @click="copy()">复制参考说明</button><button class="btn" @click="copy(true)">复制给智能体</button><button v-if="canUse" class="btn" @click="emit('use-project')">选择项目引用</button></div><p class="footnote">编号在本次拆解内有效，“复制给智能体”包含固定版本和精确定位。{{canUse?'引用将打开项目选择，携带这份资产的固定版本；具体制作由项目中的智能体执行。':'复制的是可读说明。需要改写或制作时，在项目中将说明与固定来源交给智能体。'}}</p><p v-if="status" role="status">{{status}}</p><textarea v-if="manual" :value="manual" readonly rows="8" aria-label="可复制的参考说明"/></section></div></div>
</article></template>
<style scoped>
.human-result{min-width:0}.content{min-width:0}.picture-layout{display:grid;grid-template-columns:minmax(0,.9fr) minmax(0,1.1fr);gap:24px;align-items:start}.explanation{min-width:0}.picture-layout .reading{margin-top:0;grid-template-columns:1fr}.picture-layout .usage{padding:20px}.basis{font-size:12px;color:var(--muted);margin:0 0 18px;line-height:1.8}.reading{display:grid;gap:24px;margin:28px 0}.reading-grid{grid-template-columns:repeat(2,minmax(0,1fr))}.reading section{min-width:0}.long-section{grid-column:1/-1}h4{font-size:13px;color:var(--muted);font-weight:500;margin:0 0 10px}p,li{font-size:14px;line-height:1.95;overflow-wrap:anywhere;white-space:pre-wrap}p{margin:0}ul,ol{padding-left:21px;margin:0}li+li{margin-top:10px}.usage{padding:24px;border:1px solid var(--line);border-radius:12px;background:var(--shell-button)}.usage h4{font-size:16px;color:var(--ink)}.limitation{color:var(--muted);font-size:12px;margin:10px 0 18px}.actions{display:flex;gap:10px;flex-wrap:wrap}.footnote{font-size:12px;color:var(--muted);margin-top:14px}p[role=status]{margin-top:12px;color:var(--accent);font-size:12px}textarea{width:100%;box-sizing:border-box;background:var(--shell-panel);color:var(--ink);border:1px solid var(--line);border-radius:8px;padding:12px;margin-top:12px;font:inherit}@media(max-width:800px){.picture-layout{grid-template-columns:1fr}}@media(max-width:620px){.reading-grid{grid-template-columns:1fr}.usage{padding:18px}}
</style>
