<script setup lang="ts">
import { ref } from 'vue'
import type { LearningDocument, LearningResult } from '../platform/learning-results'
import { agentReference } from '../platform/result-locator'
const props=defineProps<{result:LearningResult;document:LearningDocument;sourceLabel:string}>()
const status=ref(''),manual=ref('')
async function copy(){const text=agentReference(props.result,props.document,props.sourceLabel);try{await navigator.clipboard.writeText(text);status.value='已复制，包含固定版本与精确成果定位。';manual.value=''}catch{manual.value=text;status.value='请选择下方指令复制。'}}
</script>
<template><div class="agent-copy"><button class="btn" @click="copy">复制给智能体</button><p>编号在本次拆解内有效；复制的指令包含完整定位，可交给智能体通过 MCP 查找。</p><p v-if="status" role="status">{{status}}</p><textarea v-if="manual" readonly :value="manual" rows="6" aria-label="给智能体的定位指令"/></div></template>
<style scoped>.agent-copy{margin:20px 0}.agent-copy p{font-size:12px;color:var(--muted);line-height:1.8}textarea{width:100%;box-sizing:border-box;padding:12px;border:1px solid var(--line);border-radius:8px;background:var(--shell-panel);color:var(--ink);font:inherit}</style>
