<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import ResultFields from './ResultFields.vue'
import { compatibilityLabels, promptVariants, promptBody, promptConfiguration, type LearningResult } from '../platform/learning-results'
import type { AssetOpen } from '../platform/world-model'
import PromptSampleMedia from './PromptSampleMedia.vue'
import { promptSamples } from '../platform/prompt-samples'
import type { LearningDocument } from '../platform/learning-results'
const emit=defineEmits<{'new-project':[variant:string]}>()
const props=defineProps<{result:LearningResult;asset:AssetOpen;document:LearningDocument}>()
const variants=computed(()=>promptVariants(props.result)),chosen=ref(variants.value[0].key),copyStatus=ref(''),manual=ref(''),manualInput=ref<HTMLTextAreaElement|null>(null)
const current=computed(()=>variants.value.find(v=>v.key===chosen.value)||variants.value[0])
const samplePage=ref(0),showOriginals=ref(false)
const sampleState=computed(()=>{try{return {items:promptSamples(current.value),error:''}}catch{return {items:[],error:'生成记录不完整，暂不能展示样例。'}}})
const visibleSamples=computed(()=>sampleState.value.items.slice(samplePage.value*4,samplePage.value*4+4))
watch(()=>[props.asset.asset_id,props.asset.asset_version_id,props.result.key,current.value.key,current.value.revision],()=>{samplePage.value=0;showOriginals.value=false})
const tasks:Record<string,string>={text_planning:'文字策划',text:'文本创作',image:'图像创作',video:'视频创作',audio:'音频创作',music:'音乐创作',speech:'语音创作',image_generation:'图像生成',video_generation:'视频生成'}
const inputs:Record<string,string>={messages:'对话输入','text-to-video':'文字生成视频','reference-to-video':'参考素材生成视频','image-to-video':'图片生成视频','text-to-image':'文字生成图片','reference-to-image':'参考素材生成图片',text:'文字输入',image:'图片参考',video:'视频参考',audio:'音频参考',multimodal:'多种素材参考',text_to_image:'文字生成图片',image_to_video:'图片生成视频',text_to_video:'文字生成视频'}
const roles:Record<string,string>={system:'系统规则',developer:'任务约束',user:'创作要求',assistant:'参考回复'}
const reviews:Record<string,string>={unreviewed:'尚未审核',accepted:'记录为已采纳',rejected:'记录为未采纳'}
let copyGeneration=0
onBeforeUnmount(()=>{copyGeneration++})
watch(()=>[props.asset.asset_id,props.asset.asset_version_id,props.result.key,chosen.value],()=>{copyGeneration++;copyStatus.value='';manual.value=''})
async function copy(config=false){
 const generation=++copyGeneration,value=config?promptConfiguration(props.result,current.value,props.asset):promptBody(current.value)
 try{await navigator.clipboard.writeText(value);if(generation===copyGeneration)copyStatus.value=config?'已复制完整配置，包含变量、参数和参考绑定。':'已复制正文；参数、负向提示和素材绑定请另取完整配置。'}
 catch{if(generation!==copyGeneration)return;manual.value=value;copyStatus.value='当前环境不能自动复制，已选中内容，可手动复制。';await nextTick();if(generation===copyGeneration){manualInput.value?.focus();manualInput.value?.select()}}
}
</script>
<template><div class="prompt-result">
<p class="intent">{{result.payload.creative_intent}}</p>
<div class="variant-bar"><label>适用模型版本<select v-model="chosen"><option v-for="v in variants" :key="v.key" :value="v.key">{{v.requested_label}} · {{v.model_version||'未注明模型版本'}} · 配置 r{{v.revision}}</option></select></label><span class="badge">{{compatibilityLabels[current.compatibility]}}</span></div>
<dl class="model-facts"><dt>实际模型</dt><dd>{{current.model_id||'待解析，尚无实际模型标识'}} · {{current.model_version||'版本未记录'}}</dd><dt>任务与输入</dt><dd>{{tasks[current.task_type]||'其他创作任务'}} · {{inputs[current.input_mode]||'参考输入方式待确认'}}</dd><template v-if="current.provider||current.route"><dt>渠道</dt><dd>{{current.provider||'未记录'}} · {{current.route||'路由未记录'}}</dd></template></dl>
<div class="actions"><button class="btn primary" @click="copy()">复制正文</button><button class="btn" @click="copy(true)">复制完整配置</button><button class="btn" @click="emit('new-project',current.key)">新建项目创作</button></div><p v-if="copyStatus" class="copy-status" role="status">{{copyStatus}}</p><textarea v-if="manual" ref="manualInput" :value="manual" readonly rows="9" aria-label="待手动复制的内容"/>
<p class="sample-note">样例只读。新建项目时会带入所选模型的固定配置，所有修改与创作在项目中完成。</p>
<section class="samples" aria-label="提示词真实样例">
<h4>真实样例 · {{current.requested_label}} · 配置 r{{current.revision}}</h4>
<p v-if="sampleState.error" role="alert">{{sampleState.error}}</p>
<p v-else-if="!sampleState.items.length" class="sample-empty">该版本暂无生成样例。提示词可以先保存和使用，在项目中实际产出后，再明确选择沉淀效果。</p>
<template v-else><p class="sample-note">执行记录关联，尚未核验生成关系。样例的实际效果与质量评价分开记录。</p>
<div class="sample-grid"><figure v-for="item in visibleSamples" :key="`${asset.asset_id}:${asset.asset_version_id}:${result.key}:${current.key}:${current.revision}:${item.key}`">
<PromptSampleMedia :asset="asset" :document="document" :reference="item.reference" :title="`${result.title} · ${item.model} 样例`"/>
<figcaption>{{item.model}} · {{item.route}}<span>{{item.note}}</span></figcaption>
</figure></div>
<div v-if="sampleState.items.length>4" class="actions"><button class="btn" :disabled="samplePage===0" @click="samplePage--">上一组</button><span>{{samplePage+1}} / {{Math.ceil(sampleState.items.length/4)}}</span><button class="btn" :disabled="(samplePage+1)*4>=sampleState.items.length" @click="samplePage++">下一组</button></div>
</template>
<div v-if="result.source_refs.length" class="originals"><button class="btn" :aria-expanded="showOriginals" @click="showOriginals=!showOriginals">{{showOriginals?'收起':'查看'}}原作与来源参考</button><p>来源参考不代表生成效果。</p><template v-if="showOriginals"><PromptSampleMedia v-for="(reference,i) in result.source_refs.slice(0,4)" :key="`${asset.asset_id}:${asset.asset_version_id}:${result.key}:source:${i}`" :asset="asset" :document="document" :reference="reference" :title="`${result.title} · 来源参考`"/><p v-if="result.source_refs.length>4">其余来源引用见下方“来源依据”。</p></template></div>
</section>
<section v-if="current.prompt_text" class="text-block"><h4>提示词正文</h4><pre>{{current.prompt_text}}</pre></section>
<section v-if="current.messages" class="text-block"><h4>对话提示词</h4><div v-for="(m,i) in current.messages" :key="i" class="message"><strong>{{roles[m.role]||'对话内容'}}</strong><pre>{{m.content}}</pre></div></section>
<section v-if="current.negative_prompt" class="text-block"><h4>负向提示词</h4><pre>{{current.negative_prompt}}</pre></section>
<section v-if="Array.isArray(result.payload.variables)&&result.payload.variables.length" class="configuration"><h4>可改变量</h4><ResultFields :value="result.payload.variables"/></section>
<section v-if="current.parameters&&Object.keys(current.parameters).length" class="configuration"><h4>生成参数</h4><ResultFields :value="current.parameters"/></section>
<section v-if="current.reference_bindings.length" class="configuration"><h4>所需参考素材与绑定</h4><ResultFields :value="current.reference_bindings"/></section>
<section class="validation"><h4>已有验证记录</h4><p>兼容性：{{compatibilityLabels[current.compatibility]}}。此状态不代表本次生成已执行。</p><p>质量：{{reviews[current.quality_review.state]}} · {{current.quality_review.note}}</p><p>{{current.execution_evidence.length?'文档记录 '+current.execution_evidence.length+' 条执行证据；本页未重新核验运行环境。':'尚无执行证据。'}}</p><details v-if="current.execution_evidence.length"><summary>查看执行证据</summary><ResultFields :value="current.execution_evidence"/></details></section>
</div></template>
<style scoped>
.samples{margin:24px 0;padding:20px;border:1px solid var(--line);border-radius:10px}.sample-empty,.sample-note,.originals{font-size:12px;color:var(--muted);line-height:1.8}.sample-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,280px),1fr));gap:16px}.sample-grid figure{min-width:0;margin:0}.sample-grid figcaption{font-size:12px;line-height:1.8;overflow-wrap:anywhere}.sample-grid figcaption span{display:block;color:var(--muted)}.originals{margin-top:16px}

.intent{font-size:15px;line-height:1.8;white-space:pre-wrap}.variant-bar{display:flex;align-items:end;gap:14px;flex-wrap:wrap;margin:22px 0}.variant-bar label{display:grid;gap:8px;min-width:0;max-width:100%;font-size:12px;color:var(--muted)}select,textarea{max-width:100%;box-sizing:border-box;border:1px solid var(--line);border-radius:8px;background:var(--shell-button);color:var(--ink);padding:10px;font:inherit}textarea{width:100%;margin:12px 0}.badge{padding:8px 10px;border:1px solid var(--line);border-radius:6px;font-size:12px;color:var(--muted)}.model-facts{display:grid;grid-template-columns:100px minmax(0,1fr);gap:8px;margin:18px 0;font-size:12px}.model-facts dt{color:var(--muted)}.model-facts dd{margin:0;overflow-wrap:anywhere}.actions{display:flex;gap:10px;flex-wrap:wrap}.copy-status{font-size:12px;color:var(--accent);margin-top:12px}h4{font-size:12px;color:var(--muted);margin:0 0 12px}.text-block,.configuration,.validation{margin-top:24px}.text-block{padding:20px;border:1px solid var(--line);background:var(--shell-button);border-radius:10px}pre{font:inherit;line-height:1.9;white-space:pre-wrap;overflow-wrap:anywhere;margin:0}.message+.message{border-top:1px solid var(--line);margin-top:16px;padding-top:16px}.message strong{font-size:11px;color:var(--accent)}.validation{border-top:1px solid var(--line);padding-top:20px;font-size:12px;color:var(--muted)}.validation p{line-height:1.8;margin:8px 0}summary{cursor:pointer;margin:12px 0}
</style>
