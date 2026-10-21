<script setup lang="ts">
import { ref } from 'vue'
import type { LearningResult, LearningDocument } from '../platform/learning-results'
import { basisLabels } from '../platform/learning-results'
import ResultFields from './ResultFields.vue'
const props=defineProps<{result:LearningResult;document:LearningDocument}>()
const evidenceLimit=ref(6)
function object(value:unknown):Record<string,unknown>{return value&&typeof value==='object'&&!Array.isArray(value)?value as Record<string,unknown>:{}}
function sourceTitle(value:unknown){const ref=object(value),source=props.document.sources.find(s=>s.key===ref.key);return typeof source?.title==='string'?source.title:'来源名称未记录'}
function location(value:unknown){const loc=object(object(value).locator);if(loc.kind==='lines'&&typeof loc.start_line==='number'&&typeof loc.end_line==='number')return `第 ${loc.start_line}–${loc.end_line} 行`;return '具体定位见原始证据'}

const modes:Record<string,string>={direct:'直接使用',method:'方法应用',reference:'参考学习',recreate:'再创作'}
const availability:Record<string,string>={unknown:'可用性待核对',ready:'文档标记为就绪',missing:'仍需补齐',unsupported:'当前不支持'}
</script>
<template><aside class="context">
<details v-if="result.use_conditions.length"><summary>怎么使用 · {{result.use_conditions.length}} 种用途</summary><div v-for="(use,i) in result.use_conditions" :key="i" class="use"><strong>{{modes[use.mode]||use.mode}} · {{use.purpose}}</strong><p>{{availability[use.availability]||use.availability}}</p><p v-if="use.required_inputs.length">需要：{{use.required_inputs.join('、')}}</p><p v-if="use.missing_items.length">待补齐：{{use.missing_items.join('；')}}</p></div></details>
<details><summary>来源与分析依据 · {{basisLabels[result.basis]||result.basis}}</summary><p>{{result.basis_note}}</p><h4>来源引用 · {{result.source_refs.length}}</h4><ul><li v-for="(source,i) in result.source_refs.slice(0,evidenceLimit)" :key="i">{{sourceTitle(source)}}</li></ul><h4>定位与证据 · {{result.evidence.length}}</h4><div v-for="(evidence,i) in result.evidence.slice(0,evidenceLimit)" :key="i" class="use"><strong>{{sourceTitle(object(evidence).source_ref)}} · {{location(evidence)}}</strong><p v-if="typeof object(evidence).note==='string'">{{object(evidence).note}}</p></div><button v-if="Math.max(result.evidence.length,result.source_refs.length)>evidenceLimit" class="btn" @click="evidenceLimit+=6">再显示 6 项来源与证据</button><details><summary>查阅原始引用字段</summary><ResultFields :value="{source_refs:result.source_refs,evidence:result.evidence}"/></details></details>
</aside></template>
<style scoped>
.context{border-top:1px solid var(--line);margin-top:28px;padding-top:4px;font-size:13px;color:var(--muted)}details{padding:18px 0;border-bottom:1px solid var(--line)}summary{cursor:pointer;color:var(--ink)}details[open]>summary{margin-bottom:20px}.use+.use{margin-top:18px}p{line-height:1.8;white-space:pre-wrap;overflow-wrap:anywhere}.use strong{color:var(--ink);font-weight:500}h4{font-size:12px;font-weight:500;margin-top:24px}
</style>
