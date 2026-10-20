<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { useHost } from '../platform/host'
import { createDraftAPI, draftError } from '../platform/draft-client'
import { createCollectionSave } from '../platform/collection-save'
import { readDraft, readDraftList, readCollectionDocument, editableCollection, object, type DraftRevision, type DraftItem, type CollectionDocument, type CollectionForm } from '../platform/draft-model'
import { isID } from '../platform/world-model'
import { publicationPrincipal, publicationIdentity } from '../platform/publication-client'
import { rememberPosition, recalledPosition } from '../platform/route-memory'
const { appSDK, dashboardHost }=useHost(), route=useRoute(), router=useRouter()
const api=createDraftAPI(appSDK), saver=createCollectionSave(appSDK,{getItem:key=>localStorage.getItem(key),setItem:(key,value)=>localStorage.setItem(key,value),removeItem:key=>localStorage.removeItem(key)})
const scope=JSON.stringify(publicationPrincipal(appSDK.context.get())), identity=publicationIdentity(appSDK.context.get())
const items=ref<DraftItem[]>([]), cursor=ref<string|null>(null), selected=ref<DraftRevision|null>(null), document=ref<CollectionDocument|null>(null)
const loading=ref(false), error=ref(''), saveError=ref(''), saving=ref(false), pending=ref<ReturnType<typeof saver.read>>(null)
const dialog=ref<HTMLDialogElement|null>(null), opener=ref<HTMLElement|null>(null), editing=ref(false)
const form=ref<CollectionForm>({name:'',text:'',url:''}), search=ref(typeof route.query.q==='string'?route.query.q:'')
const detail=computed(()=>typeof route.params.draftId==='string'?route.params.draftId:'')
const editor=computed(()=>document.value?editableCollection(document.value):null)
const listPath=computed(()=>({path:'/collections',query:route.query.q?{q:route.query.q}:{}}))
let alive=true, hidden=false, generation=0
let newForm:CollectionForm={name:'',text:'',url:''}
const edits=new Map<string,CollectionForm>()
let editBase:DraftRevision|undefined
function savePosition() { rememberPosition(scope,route.fullPath,dashboardHost.getScroll()) }
onBeforeRouteUpdate(savePosition);onBeforeRouteLeave(savePosition)
async function restore() { await nextTick(); if(alive&&!hidden) dashboardHost.restoreScroll(recalledPosition(scope,route.fullPath)) }
function pendingRead() { try { pending.value=saver.read() } catch(cause) { saveError.value=draftError(cause) } }
pendingRead()
async function load(more=false) {
  if(!alive||hidden)return
  const current=++generation
  loading.value=true;error.value=''
  if(!more){selected.value=null;document.value=null;items.value=[];cursor.value=null}
  try {
    if(detail.value){
      const id=detail.value, revision=route.query.revision
      if(!isID(id)||revision!==undefined&&!isID(revision)) throw Error('收录地址或固定版本不正确。')
      const opened=readDraft(await api.request({mode:'get',draft_id:id,...(revision?{revision_id:revision}:{})}),id,typeof revision==='string'?revision:undefined,'collection')
      let body:CollectionDocument|null=null
      const entry=opened.manifest.domain_documents.find(d=>d.document_type==='reference-learning')
      const file=opened.manifest.files.find(f=>f.file_id===entry?.file_id)
      if(file){
        const data=object(await api.request({mode:'text',content_ref:file.content_ref,access:{world_draft_id:opened.draft_id,world_draft_revision_id:opened.revision_id}},'artifact.read'))
        const version=object(data.version)
        if(data.mode!=='text'||typeof data.text!=='string'||version.version_id!==file.content_ref.version_id||version.artifact_id!==file.content_ref.artifact_id||version.store_id!==file.content_ref.store_id)throw Error('正文与所选固定版本不一致。')
        body=readCollectionDocument(data.text)
      }
      if(!alive||hidden||current!==generation)return
      selected.value=opened;document.value=body
    } else {
      const previous=cursor.value
      const data=readDraftList(await api.request({mode:'list',purpose:'collection',limit:50,...(typeof route.query.q==='string'&&route.query.q?{q:route.query.q}:{}),...(more&&previous?{cursor:previous}:{})}),'collection')
      if(!alive||hidden||current!==generation)return
      if(more&&data.next_cursor===previous)throw Error('分页未继续，请重新读取。')
      items.value=[...new Map([...(more?items.value:[]),...data.items].map(x=>[x.draft_id,x])).values()];cursor.value=data.next_cursor
    }
  }catch(cause){if(alive&&current===generation)error.value=draftError(cause)}
  finally{if(alive&&current===generation){loading.value=false;void restore()}}
}
function searchList(){void router.push({path:'/collections',query:search.value.trim()?{q:search.value.trim()}: {}})}
function open(item:DraftItem){void router.push({path:'/collections/'+item.draft_id,query:{...route.query,revision:item.head_revision_id}})}
function capture(edit=false){
  editing.value=edit;editBase=edit?selected.value||undefined:undefined;saveError.value='';pendingRead()
  form.value=pending.value?{...pending.value.form}:edit&&editor.value?{...(edits.get(selected.value!.revision_id)||{...editor.value,name:selected.value!.manifest.name})}:{...newForm}
  opener.value=globalThis.document.activeElement as HTMLElement
  dialog.value?.showModal();dashboardHost.setDirty(true)
}
function close(){
  if(!pending.value){if(editing.value&&editBase)edits.set(editBase.revision_id,{...form.value});else newForm={...form.value}}
  dialog.value?.close();dashboardHost.setDirty(false);if(!hidden)opener.value?.focus()
}
async function save(recover=false){
  if(saving.value)return
  saving.value=true;saveError.value=''
  const base=editBase,originPath=route.fullPath
  try{
    if(editing.value&&!base&&!recover)throw Error('请先读取要修改的固定收录版本。')
    const result=recover?await saver.resume():await saver.start(form.value,editing.value?base:undefined)
    pendingRead()
    if(!alive||hidden||route.fullPath!==originPath)return
    close();if(base)edits.delete(base.revision_id);else newForm={name:'',text:'',url:''};await router.push({path:'/collections/'+result.draft_id,query:{...route.query,revision:result.revision_id}})
    if(selected.value?.revision_id===result.revision_id)await load()
  }catch(cause){if(alive)saveError.value=draftError(cause)}
  finally{if(alive){saving.value=false;pendingRead()}}
}
async function reviewFailure(){
  if(saving.value)return
  saving.value=true
  try{
    const previous=saver.read()
    if(previous?.base){
      await router.push({path:'/collections/'+previous.base.draft_id});await load()
      if(!alive||hidden||!selected.value||selected.value.draft_id!==previous.base.draft_id||selected.value.revision_id!==selected.value.head_revision_id)throw Error('尚未读取最新版本；原修改和恢复记录仍保留。')
      if(!editor.value)throw Error('最新内容已超出文本收录的编辑范围；原修改仍保留，请先查看最新内容。')
    }
    const input=saver.releaseFailed();pendingRead();close()
    capture(!!previous?.base);form.value=input;saveError.value='原操作已明确失败；请对照最新内容检查后重新保存。'
  }catch(cause){if(alive&&!hidden)saveError.value=draftError(cause)}finally{if(alive)saving.value=false}
}
function analyze(){if(selected.value)void router.push({path:'/learn',query:{draft:selected.value.draft_id,revision:selected.value.revision_id}})}
function sourceText(source:Record<string,unknown>){const c=source.content;return c&&typeof c==='object'&&'text'in c&&typeof c.text==='string'?c.text:''}
function safeURL(source:Record<string,unknown>){try{const u=new URL(String(source.source_url));return ['http:','https:'].includes(u.protocol)&&!u.username&&!u.password?u.href:''}catch{return ''}}
watch(()=>route.fullPath,()=>{search.value=typeof route.query.q==='string'?route.query.q:'';void load()},{immediate:true})
const stop=appSDK.lifecycle.subscribe(event=>{
  if(event==='hidden'||event==='dispose'){savePosition();hidden=true;generation++;loading.value=false;close()}
  if(event==='active'){hidden=false;pendingRead();void load()}
})
const stopContext=appSDK.context.subscribe(()=>{if(publicationIdentity(appSDK.context.get())===identity)return;generation++;items.value=[];selected.value=null;document.value=null;pending.value=null;form.value={name:'',text:'',url:''};newForm={...form.value};edits.clear();editBase=undefined;dialog.value?.close();error.value='会话已变化，请重新打开数字世界。';hidden=true})
onBeforeUnmount(()=>{savePosition();alive=false;generation++;api.dispose();saver.dispose();stop();stopContext();dashboardHost.setDirty(false)})
</script>
<template>
<main class="main"><div class="workspace">

  <RouterLink v-if="detail" class="back-link" :to="listPath">← 返回收录内容</RouterLink>
  <header class="heading"><div class="heading-left"><div class="eyebrow">WORLD · COLLECTION</div><h1>{{ detail ? selected?.manifest.name || '收录详情' : '收录内容' }}</h1><p>{{detail?'保存来源和想法，需要时再拆解或整理为资产。':'看到值得留下的内容就收录。独立于项目，也不会混入成果资产。'}}</p></div><div class="actions"><button class="btn" :disabled="loading" @click="load()">刷新</button><button class="btn primary" @click="capture()">收录内容</button></div></header>
  <section v-if="pending" class="notice"><div><strong>有一份保存等待确认</strong><p>{{pending.form.name}} · 保留原操作，不会重复新建。</p></div><button class="btn" @click="capture()">{{pending.failure?'核对未保存内容':'恢复保存'}}</button></section>
  <section v-if="error" class="state error" role="alert"><p>{{error}}</p><button class="btn" @click="load()">重试</button></section>
  <form v-if="!detail" class="searchbar" @submit.prevent="searchList"><label class="sr-only" for="collection-search">搜索收录名称或标签</label><input id="collection-search" v-model="search" maxlength="256" type="search" placeholder="搜索收录名称或标签"><button class="btn" type="submit">搜索</button><span>已载入 {{items.length}} 项</span></form>
  <div v-if="loading&&!selected&&!items.length" class="state" role="status">正在读取收录内容…</div>
  <template v-else-if="!detail">
    <section v-if="!items.length&&!error" class="state empty"><strong>把值得学习的内容留在这里</strong><p>先收录一段正文或一个链接，日后再拆解、修改和复用。</p><button class="btn primary" @click="capture()">收录第一份内容</button></section>
    <div class="collection-list"><article v-for="item in items" :key="item.draft_id" class="panel collection-row"><div><span class="badge">{{item.has_content?'已收录':'待添加内容'}}</span><h2><button @click="open(item)">{{item.name}}</button></h2><p>{{item.subjects.join(' · ')||'独立收录 · 尚未自动分析'}}</p></div><div class="actions"><button class="btn" @click="open(item)">查看内容</button><button class="btn" :disabled="!item.has_content" @click="router.push({path:'/learn',query:{draft:item.draft_id,revision:item.head_revision_id}})">用于拆解</button></div></article></div>
    <button v-if="cursor" class="btn more" :disabled="loading" @click="load(true)">加载更多</button>
  </template>
  <template v-else-if="selected">
    <div class="actions detail-actions"><button class="btn primary" :disabled="!selected.manifest.files.length" @click="analyze">用于完整拆解</button><button v-if="editor" class="btn" :disabled="selected.revision_id!==selected.head_revision_id" @click="capture(true)">修改收录</button><span class="muted">{{selected.revision_id===selected.head_revision_id?'当前版本':'历史固定版本'}}</span><button v-if="selected.revision_id!==selected.head_revision_id" class="btn" @click="router.push({path:'/collections/'+selected.draft_id,query:route.query.q?{q:route.query.q}:{}})">查看最新内容</button></div>
    <section v-for="source in document?.sources||[]" :key="String(source.key)" class="panel"><h2>{{source.title}}</h2><span class="badge">{{source.acquisition_state==='ready'?'已保存内容':'仅收录线索 · 尚未读取原件'}}</span><p v-if="safeURL(source)" class="source-url"><a :href="safeURL(source)" target="_blank" rel="noopener noreferrer">{{source.source_url}} ↗</a></p><pre v-if="sourceText(source)" class="source-text">{{sourceText(source)}}</pre><p v-else class="muted">{{source.content?'原始内容保存在所附文件中。':'链接收录不会自动抓取或完成分析。'}}</p></section>
    <section v-if="!document" class="state empty"><strong>{{selected.manifest.files.length?'已保存文件':'这份收录还没有内容'}}</strong><p>文件清单来自当前固定修订。</p></section>
    <details v-if="selected.manifest.files.length" class="panel"><summary>已保存文件 · {{selected.manifest.files.length}}</summary><ul class="files"><li v-for="file in selected.manifest.files" :key="file.file_id">{{file.path}} <span>{{file.role}}</span></li></ul></details>
  </template>
  <dialog ref="dialog" class="capture-sheet" aria-labelledby="capture-title" @cancel.prevent="close"><div class="sheet-inner"><div class="sheet-head"><h2 id="capture-title">{{editing?'修改收录':'收录内容'}}</h2><button class="btn" @click="close">关闭</button></div><p class="muted">保存到数字世界，随时找回。不需要选择项目。</p>
    <section v-if="saveError" class="state error" role="alert">{{saveError}}</section>
    <form @submit.prevent="save(!!pending)"><label>名称<input v-model="form.name" :disabled="!!pending||saving" maxlength="256" required placeholder="给这份内容起个名字"></label><label>正文<textarea v-model="form.text" :disabled="!!pending||saving" rows="13" placeholder="留下剧本片段、创作笔记或值得学习的原文…"></textarea></label><label>来源链接（可选）<input v-model="form.url" :disabled="!!pending||saving" type="url" maxlength="2048" placeholder="https://"></label><p class="muted">目前此面板保存文本和链接；视频、音频等文件上传正在接入。仅保存链接不表示已经读取原件。</p><div class="actions"><button v-if="!pending?.failure" class="btn primary" type="submit" :disabled="saving">{{saving?'正在确认保存…':pending?'恢复原保存':'保存收录'}}</button><button v-else class="btn" type="button" :disabled="saving" @click="reviewFailure">核对最新版本后重新编辑</button><button class="btn" type="button" @click="close">返回</button></div></form>
  </div></dialog>
</div></main>
</template>
<style scoped>
.eyebrow{color:var(--accent);font-size:11px;letter-spacing:.12em;font-weight:700}.actions{display:flex;align-items:center;gap:10px;flex-wrap:wrap}.back-link{display:inline-block;margin:0 0 18px;color:var(--muted);font-size:12px}.panel{border:1px solid var(--line);border-radius:12px;padding:24px;background:var(--shell-panel);margin:16px 0}.panel h2{font-size:18px;color:var(--ink);margin:10px 0}.panel p{color:var(--muted);font-size:13px}.collection-row{display:flex;justify-content:space-between;align-items:center;gap:24px}.collection-row h2 button{text-align:left}.badge{display:inline-block;border:1px solid var(--line);border-radius:6px;padding:3px 8px;font-size:11px;color:var(--muted)}.state{display:flex;align-items:center;gap:14px;padding:24px;border:1px dashed var(--line);border-radius:12px;color:var(--muted);margin:16px 0}.empty{min-height:220px;flex-direction:column;justify-content:center;text-align:center}.empty strong{font-size:20px;color:var(--ink)}.error{color:var(--danger);border-color:var(--danger)}.notice{display:flex;align-items:center;justify-content:space-between;gap:16px;border:1px solid var(--accent);padding:18px 22px;border-radius:12px;color:var(--ink);margin-bottom:20px}.notice p{color:var(--muted);font-size:12px}.searchbar{display:flex;align-items:center;gap:12px;margin-bottom:24px}.searchbar input{max-width:480px}.searchbar span,.muted{font-size:12px;color:var(--muted)}input,textarea{width:100%;border:1px solid var(--line);border-radius:8px;background:var(--shell-button);color:var(--ink);padding:11px 12px}textarea{resize:vertical;line-height:1.8}.source-text{white-space:pre-wrap;overflow-wrap:anywhere;font:inherit;line-height:1.9;color:var(--ink);margin-top:20px}.source-url{overflow-wrap:anywhere;margin-top:12px}.source-url a{color:var(--accent)}.files{list-style:none;margin-top:14px}.files li{display:flex;justify-content:space-between;border-bottom:1px solid var(--line);padding:12px 0;overflow-wrap:anywhere;color:var(--ink)}.files span{color:var(--muted);font-size:12px}summary{color:var(--muted);cursor:pointer}.more{margin-top:16px}.detail-actions{margin-bottom:22px}.capture-sheet{position:fixed;inset:0 0 0 auto;width:min(720px,96vw);height:100dvh;max-width:none;max-height:none;margin:0;padding:0;border:0;border-left:1px solid var(--line);background:var(--shell-panel);color:var(--ink)}.capture-sheet::backdrop{background:color-mix(in srgb,var(--ink) 30%,transparent)}.sheet-inner{padding:32px}.sheet-head{display:flex;align-items:center;justify-content:space-between;gap:16px;margin-bottom:12px}.sheet-head h2{font-size:24px}.capture-sheet form{display:grid;gap:22px;margin-top:28px}.capture-sheet label{display:grid;gap:8px;font-size:13px}.sr-only{position:absolute;width:1px;height:1px;overflow:hidden;clip:rect(0,0,0,0)}@media(max-width:800px){.collection-row,.notice{align-items:flex-start;flex-direction:column}.sheet-inner{padding:24px 18px}.searchbar{flex-wrap:wrap}}
</style>
