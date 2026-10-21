<script setup lang="ts">
import type { LearningResult } from '../platform/learning-results'
import { basisLabels } from '../platform/learning-results'
import ResultFields from './ResultFields.vue'
defineProps<{result:LearningResult}>()
const modes:Record<string,string>={direct:'直接使用',method:'方法应用',reference:'参考学习',recreate:'再创作'}
const availability:Record<string,string>={unknown:'可用性待核对',ready:'文档标记为就绪',missing:'仍需补齐',unsupported:'当前不支持'}
</script>
<template><aside class="context">
<details v-if="result.use_conditions.length"><summary>怎么使用 · {{result.use_conditions.length}} 种用途</summary><div v-for="(use,i) in result.use_conditions" :key="i" class="use"><strong>{{modes[use.mode]||use.mode}} · {{use.purpose}}</strong><p>{{availability[use.availability]||use.availability}}</p><p v-if="use.required_inputs.length">需要：{{use.required_inputs.join('、')}}</p><p v-if="use.missing_items.length">待补齐：{{use.missing_items.join('；')}}</p></div></details>
<details><summary>来源与分析依据 · {{basisLabels[result.basis]||result.basis}}</summary><p>{{result.basis_note}}</p><h4>来源引用 · {{result.source_refs.length}}</h4><ResultFields :value="result.source_refs"/><h4>定位与证据 · {{result.evidence.length}}</h4><ResultFields :value="result.evidence"/></details>
</aside></template>
<style scoped>
.context{border-top:1px solid var(--line);margin-top:28px;padding-top:4px;font-size:13px;color:var(--muted)}details{padding:18px 0;border-bottom:1px solid var(--line)}summary{cursor:pointer;color:var(--ink)}details[open]>summary{margin-bottom:20px}.use+.use{margin-top:18px}p{line-height:1.8;white-space:pre-wrap;overflow-wrap:anywhere}.use strong{color:var(--ink);font-weight:500}h4{font-size:12px;font-weight:500;margin-top:24px}
</style>
