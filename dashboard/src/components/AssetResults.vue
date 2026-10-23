<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useHost } from '../platform/host'
import { createReadClient } from '../platform/client'
import { publicationIdentity } from '../platform/publication-client'
import { loadAssetResults, type LearningResult, type LearningDocument } from '../platform/learning-results'
import type { AssetOpen } from '../platform/world-model'
import PromptResult from './PromptResult.vue'
import AgentReferenceCopy from './AgentReferenceCopy.vue'
import HumanResult from './HumanResult.vue'
import ResultReferenceMedia from './ResultReferenceMedia.vue'
import { provideReferenceMedia } from '../platform/reference-media'
import { resultCode, businessOrder } from '../platform/result-locator'
import ResultBrowser from './ResultBrowser.vue'
import WorldTrail, {type TrailItem} from './WorldTrail.vue'
import ResultContext from './ResultContext.vue'
const emit=defineEmits<{'new-project':[result:LearningResult,variant:string];'use-project':[]}>()
const props=defineProps<{asset:AssetOpen;parents:TrailItem[]}>(),{appSDK}=useHost(),client=createReadClient(appSDK,'world')
provideReferenceMedia()
const document=ref<LearningDocument|null>(null),loading=ref(false),error=ref('')
const identity=publicationIdentity(appSDK.context.get())
let alive=true,hidden=false,generation=0,revoked=false,controller=new AbortController()
async function load(){
 if(!alive||hidden||revoked)return
 controller.abort();controller=new AbortController();const current=++generation;loading.value=true;error.value='';document.value=null
 try{const d=await loadAssetResults(client.call,props.asset,controller.signal);if(alive&&!hidden&&current===generation)document.value=d}
 catch(e){if(alive&&current===generation)error.value=e instanceof Error?e.message:'无法读取资产正文。'}
 finally{if(alive&&current===generation)loading.value=false}
}
const stop=appSDK.lifecycle.subscribe(e=>{if(e==='hidden'||e==='dispose'){hidden=true;controller.abort();generation++;document.value=null;loading.value=false}if(e==='active'){hidden=false;void load()}})
const stopContext=appSDK.context.subscribe(c=>{if(publicationIdentity(c)===identity)return;revoked=true;controller.abort();generation++;document.value=null;error.value='会话已变化，请重新打开数字世界。';loading.value=false})
onBeforeUnmount(()=>{alive=false;controller.abort();generation++;client.dispose();stop();stopContext()})
void load()
</script>
<template><section class="asset-results" aria-label="资产成果正文">
<WorldTrail v-if="!document" :items="[...parents,{label:asset.manifest.name}]"/><slot v-if="!document"/><div v-if="loading" class="state" role="status">正在读取固定版本的成果正文…</div>
<div v-else-if="error" class="state error" role="alert"><p>{{error}}</p><button class="btn" @click="load">重新读取正文</button></div>
<div v-else-if="!document" class="state">这份资产没有类型化成果正文，已保存文件见下方清单。</div>
<template v-else><ResultBrowser :results="businessOrder(document.results)" :code="r=>resultCode(r,document!)" :parents="parents" :label="asset.manifest.name">
<template #intro><header><h2>{{asset.manifest.name}}</h2><p>共 {{document.results.length}} 项 · 当前资产的固定版本</p></header><slot/></template>
<template #thumbnail="{result}"><ResultReferenceMedia v-if="['character','scene','prop','shot'].includes(result.type_key)" :result="result" :document="document!" thumbnail/></template>
<template #default="{result}">
<PromptResult v-if="result.type_key==='prompt'&&result.type_version===1" :result="result" :asset="asset" :document="document" @new-project="variant=>emit('new-project',result,variant)"/>
<HumanResult v-else :result="result" :document="document" :source-label="`世界资产 ${asset.asset_id} / 版本 ${asset.asset_version_id}`" can-use @use-project="emit('use-project')"/>
<AgentReferenceCopy v-if="result.type_key==='prompt'" :result="result" :document="document" :source-label="`世界资产 ${asset.asset_id} / 版本 ${asset.asset_version_id}`"/>
<details><summary>查看来源</summary><ResultContext :result="result" :document="document"/></details></template>
</ResultBrowser></template></section></template>
<style scoped>
.asset-results{margin:24px 0;color:var(--ink);min-width:0}header{margin-bottom:18px}h2{font-size:21px;margin:0 0 8px}header p{font-size:12px;color:var(--muted)}.state{padding:26px;border:1px dashed var(--line);border-radius:10px;color:var(--muted)}.error{color:var(--danger)}.notice{font-size:12px;color:var(--muted);margin-bottom:16px}
</style>
