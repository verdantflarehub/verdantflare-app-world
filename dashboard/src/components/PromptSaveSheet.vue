<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useHost } from '../platform/host'
import { createPromptSave } from '../platform/prompt-save'
import { promptEdit, preparePromptCopy, revisePromptCopy, type PromptEdit } from '../platform/prompt-copy'
import { promptVariants, type LearningResult } from '../platform/learning-results'
import { draftError } from '../platform/draft-client'
import { publicationIdentity } from '../platform/publication-client'
import type { AssetOpen } from '../platform/world-model'
const emit=defineEmits<{saved:[asset:AssetOpen]}>(),{appSDK,dashboardHost}=useHost()
const saver=createPromptSave(appSDK,{getItem:k=>localStorage.getItem(k),setItem:(k,v)=>localStorage.setItem(k,v),removeItem:k=>localStorage.removeItem(k)})
const pending=ref<ReturnType<typeof saver.read>>(null),dialog=ref<HTMLDialogElement|null>(null),form=ref<PromptEdit|null>(null),error=ref(''),saving=ref(false),reediting=ref(false)
let source:{asset:AssetOpen;result:LearningResult;variant:string}|undefined,opener:HTMLElement|null=null,alive=true,hidden=false,revoked=false
const edits=new Map<string,PromptEdit>()
const editKey=()=>source?JSON.stringify([source.asset.asset_id,source.asset.asset_version_id,source.result.key,source.variant]):''
const identity=publicationIdentity(appSDK.context.get())
function read(){try{pending.value=saver.read()}catch(e){error.value=draftError(e)}}
read()
function show(){opener=document.activeElement as HTMLElement;dialog.value?.showModal();dashboardHost.setDirty(true)}
function edit(asset:AssetOpen,result:LearningResult,variant:string){
 if(revoked||hidden)return
 read();reediting.value=false;error.value=''
 if(!pending.value){source=JSON.parse(JSON.stringify({asset,result,variant}));const v=promptVariants(source!.result).find(v=>v.key===variant);if(!v)return;form.value=JSON.parse(JSON.stringify(edits.get(editKey())||promptEdit(source!.result,v)))}
 show()
}
function recover(){read();reediting.value=false;show()}
function editFailed(){if(!pending.value)return;const r=pending.value.plan.document.results[0];form.value=promptEdit(r,promptVariants(r)[0]);reediting.value=true;error.value=''}
function close(){if(!pending.value&&!reediting.value&&source&&form.value)edits.set(editKey(),JSON.parse(JSON.stringify(form.value)));dialog.value?.close();dashboardHost.setDirty(false);if(!hidden)opener?.focus()}
async function save(retry=false){
 if(saving.value||revoked)return
 saving.value=true;error.value=''
 try{
  let operation:Promise<AssetOpen>
  if(retry)operation=saver.retryRegistration()
  else if(pending.value&&reediting.value&&form.value)operation=saver.retryCommit(revisePromptCopy(pending.value.plan,form.value))
  else if(pending.value)operation=saver.resume()
  else{if(!source||!form.value)throw Error('请先选择要修改的固定提示词。');operation=saver.start(preparePromptCopy(source.asset,source.result,source.variant,form.value))}
  read()
  const asset=await operation
  if(!alive||hidden)return
  read();close();edits.delete(editKey());form.value=null;source=undefined;emit('saved',asset)
 }catch(e){if(alive&&!hidden)error.value=draftError(e)}finally{if(alive){saving.value=false;read()}}
}
const stop=appSDK.lifecycle.subscribe(e=>{if(e==='hidden'||e==='dispose'){hidden=true;close()}if(e==='active'){hidden=false;read()}})
const stopContext=appSDK.context.subscribe(c=>{if(publicationIdentity(c)===identity)return;revoked=true;edits.clear();source=undefined;form.value=null;pending.value=null;error.value='会话已变化，请重新打开数字世界。';dialog.value?.close();dashboardHost.setDirty(false)})
onBeforeUnmount(()=>{alive=false;saver.dispose();stop();stopContext();dashboardHost.setDirty(false)})
defineExpose({edit})
</script>
<template>
<p v-if="error&&!pending" class="error" role="alert">{{error}}</p>
<section v-if="pending" class="pending"><div><strong>有一份提示词另存等待确认</strong><p>{{pending.plan.name}} · {{pending.saved?'工作副本已保存':'原操作可恢复'}}</p></div><button class="btn" @click="recover">查看并恢复</button></section>
<dialog ref="dialog" class="sheet" aria-labelledby="prompt-save-title" @cancel.prevent="close"><div class="sheet-inner"><header><h2 id="prompt-save-title">修改并另存提示词</h2><button class="btn" @click="close">返回</button></header><p class="hint">为选中的模型变体创建新资产，保留原版本；不需要项目。</p>
<p v-if="error" class="error" role="alert">{{error}}</p>
<template v-if="pending&&!reediting"><div class="recovery"><h3>{{pending.plan.name}}</h3><p>已固定原操作的正文与编号，恢复不会另建第二份工作副本。</p><p v-if="pending.saved">已保存工作副本：{{pending.saved.draft_id}}</p><p v-if="pending.failure">原操作已明确失败：{{pending.failure}}。已保存内容和恢复记录仍保留。</p></div><div class="actions"><button v-if="!pending.failure" class="btn primary" :disabled="saving" @click="save()">{{saving?'正在确认…':'恢复原另存操作'}}</button><button v-else-if="pending.failure_stage==='register'" class="btn primary" :disabled="saving" @click="save(true)">保留工作副本，重试登记</button><button v-else class="btn" :disabled="saving" @click="editFailed">核对修改后重试保存</button></div></template>
<form v-else-if="form" @submit.prevent="save()"><label>新资产名称<input v-model="form.name" maxlength="256" required :disabled="saving"></label><label>创作目标<textarea v-model="form.intent" rows="3" maxlength="4096" required :disabled="saving"/></label>
<div class="columns"><label>模型名称或说明<input v-model="form.requested_label" maxlength="256" required :disabled="saving"></label><label>实际模型标识（可留空）<input v-model="form.model_id" maxlength="256" :disabled="saving"></label><label>模型版本<input v-model="form.model_version" maxlength="256" :disabled="saving"></label><label>服务渠道<input v-model="form.provider" maxlength="256" :disabled="saving"></label><label>渠道路由<input v-model="form.route" maxlength="256" :disabled="saving"></label></div>
<label>提示词正文<textarea v-model="form.prompt_text" rows="10" maxlength="65536" :disabled="saving"/></label>
<fieldset v-if="form.messages.length" :disabled="saving"><legend>对话提示词（保留消息顺序）</legend><label v-for="(m,i) in form.messages" :key="i">{{m.role}}<textarea v-model="m.content" rows="5" maxlength="65536" required/></label></fieldset>
<label>负向提示词<textarea v-model="form.negative_prompt" rows="3" maxlength="65536" :disabled="saving"/></label><details><summary>生成参数 JSON</summary><textarea v-model="form.parameters" rows="8" :disabled="saving" aria-label="生成参数 JSON"/></details>
<p class="hint">参考素材绑定与变量继续保留。修改后的适配及质量需重新验证，保存不会调用生成模型。</p><div class="actions"><button class="btn primary" type="submit" :disabled="saving">{{saving?'正在保存…':'另存为提示词资产'}}</button><button class="btn" type="button" @click="close">返回</button></div></form>
</div></dialog></template>
<style scoped>
.pending{display:flex;justify-content:space-between;gap:16px;align-items:center;border:1px solid var(--accent);padding:18px;border-radius:10px;margin-bottom:20px;color:var(--ink)}.pending p,.hint{color:var(--muted);font-size:12px;line-height:1.8}.sheet{position:fixed;inset:0 0 0 auto;width:min(820px,96vw);height:100dvh;max-width:none;max-height:none;margin:0;border:0;border-left:1px solid var(--line);background:var(--shell-panel);color:var(--ink);padding:0}.sheet::backdrop{background:color-mix(in srgb,var(--ink) 30%,transparent)}.sheet-inner{padding:30px}.sheet header{display:flex;justify-content:space-between;align-items:center;gap:16px;margin-bottom:12px}h2{font-size:24px}.error{color:var(--danger);line-height:1.8;margin:18px 0}form{display:grid;gap:20px;margin-top:24px}label{display:grid;gap:8px;font-size:12px}input,textarea{width:100%;border:1px solid var(--line);border-radius:7px;padding:11px;background:var(--shell-button);color:var(--ink);font:inherit}textarea{line-height:1.8;resize:vertical}.columns{display:grid;grid-template-columns:1fr 1fr;gap:16px}.actions{display:flex;gap:12px;flex-wrap:wrap;margin-top:18px}fieldset{display:grid;gap:16px;border:1px solid var(--line);padding:18px;border-radius:8px}legend,summary{color:var(--muted);font-size:12px}summary{cursor:pointer;margin-bottom:12px}.recovery{padding:24px 0;overflow-wrap:anywhere}.recovery p{font-size:13px;line-height:1.8;color:var(--muted)}@media(max-width:700px){.sheet-inner{padding:24px 18px}.columns{grid-template-columns:1fr}.pending{align-items:start;flex-direction:column}}
</style>
