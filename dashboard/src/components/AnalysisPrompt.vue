<script setup lang="ts">
import { computed, ref } from 'vue'
import { promptVariants, promptBody, compatibilityLabels, type LearningResult } from '../platform/learning-results'
import ResultFields from './ResultFields.vue'
const props=defineProps<{result:LearningResult}>()
defineEmits<{copy:[text:string]}>()
const variants=computed(()=>promptVariants(props.result)),chosen=ref(variants.value[0].key)
const current=computed(()=>variants.value.find(v=>v.key===chosen.value)||variants.value[0])
</script>
<template><section class="prompt">
<p class="intent">{{result.payload.creative_intent}}</p>
<div class="bar"><label>适用模型<select v-model="chosen"><option v-for="variant in variants" :key="variant.key" :value="variant.key">{{variant.requested_label}} · {{variant.model_version||'版本未注明'}}</option></select></label><button class="btn" @click="$emit('copy',promptBody(current))">复制提示词</button></div>
<p class="muted">{{current.model_id||'实际模型待确认'}} · {{compatibilityLabels[current.compatibility]}}</p>
<pre>{{promptBody(current)}}</pre>
<p v-if="current.negative_prompt" class="negative">负向提示：{{current.negative_prompt}}</p>
<p class="muted">{{current.quality_review.note}}</p><p v-if="!current.execution_evidence.length" class="muted">暂无该目标模型的真实生成样例。</p>
<details><summary>变量、参数与参考素材</summary><ResultFields :value="{variables:result.payload.variables,parameters:current.parameters,reference_bindings:current.reference_bindings}"/></details>
<details v-if="current.execution_evidence.length"><summary>已记录的执行证据 · {{current.execution_evidence.length}} 条</summary><ResultFields :value="current.execution_evidence"/></details>
</section></template>
<style scoped>
.intent,.negative{font-size:14px;line-height:1.8;white-space:pre-wrap;overflow-wrap:anywhere}.bar{display:flex;align-items:end;flex-wrap:wrap;gap:16px;margin:20px 0}label{display:grid;gap:8px;font-size:12px;max-width:100%}select{padding:12px;border:1px solid var(--line);border-radius:8px;background:var(--shell-button);color:var(--ink);font:inherit;max-width:100%}pre{padding:24px;border:1px solid var(--line);border-radius:12px;background:var(--shell-button);white-space:pre-wrap;overflow-wrap:anywhere;font:inherit;line-height:1.9}.muted{font-size:12px;line-height:1.8;color:var(--muted)}details{margin-top:20px}summary{cursor:pointer;font-size:12px;margin-bottom:18px}@media(max-width:620px){pre{padding:16px}}
</style>
