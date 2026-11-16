<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useHost } from '../platform/host'
import { createDraftAPI, draftError } from '../platform/draft-client'
import { readDraft, readDraftList, type DraftItem, type DraftRevision } from '../platform/draft-model'
import { createPackageReferenceReader } from '../platform/package-reference'
import { packageJSON, validatePackageSchema } from '../platform/package-json'
type Row = Record<string, unknown>
const props = defineProps<{ disabled: boolean; needsObject: boolean; purpose: string }>()
const emit = defineEmits<{ select: [reference: Row | null] }>()
const { appSDK } = useHost(), api = createDraftAPI(appSDK), reader = createPackageReferenceReader(appSDK)
const purpose = ref<'collection'|'learning'|'creation'>('learning'), search = ref(''), items = ref<DraftItem[]>([]), cursor = ref<string|null>(null)
const selected = ref(''), opened = ref<DraftRevision|null>(null), file = ref(''), objectKey = ref(''), objects = ref<{key:string;title:string}[]>([])
const busy = ref(false), searched = ref(false), error = ref('')
let epoch = 0, alive = true
const current = (n:number) => alive && epoch === n
function clearSelection() { selected.value='';opened.value=null;file.value='';objectKey.value='';objects.value=[];emit('select',null) }
async function list(more=false) {
  if(props.disabled||busy.value)return
  const n=++epoch;busy.value=true;error.value=''
  if(!more){items.value=[];cursor.value=null;searched.value=false;clearSelection()}
  try {
    const previous=more?cursor.value:null
    const result=readDraftList(await api.request({mode:'list',purpose:purpose.value,limit:30,...(search.value.trim()?{q:search.value.trim()}:{}),...(previous?{cursor:previous}:{})}),purpose.value)
    if(!current(n))return
    if(previous&&result.next_cursor===previous)throw Error('分页未继续，请重新搜索。')
    items.value=[...new Map([...(more?items.value:[]),...result.items].map(item=>[item.draft_id,item])).values()];cursor.value=result.next_cursor;searched.value=true
  }catch(e){if(current(n))error.value=draftError(e)}finally{if(current(n))busy.value=false}
}
async function selectDraft() {
  const item=items.value.find(row=>row.draft_id===selected.value),n=++epoch
  opened.value=null;file.value='';objectKey.value='';objects.value=[];emit('select',null);error.value=''
  if(!item)return
  busy.value=true
  try {
    const fixed=readDraft(await api.request({mode:'get',draft_id:item.draft_id,revision_id:item.head_revision_id}),item.draft_id,item.head_revision_id,purpose.value)
    if(current(n))opened.value=fixed
  }catch(e){if(current(n))error.value=draftError(e)}finally{if(current(n))busy.value=false}
}
function reference(): Row | null {
  const draft=opened.value
  if(!draft||!draft.manifest.files.some(f=>f.file_id===file.value))return null
  return {kind:'world_draft',draft_id:draft.draft_id,revision_id:draft.revision_id,file_id:file.value,purpose:props.purpose||'learning_source'}
}
function selectObject() {
  const target=reference()
  emit('select',target&&objects.value.some(o=>o.key===objectKey.value)?{...target,object_key:objectKey.value}:null)
}
async function selectFile() {
  const target=reference(),n=++epoch
  objectKey.value='';objects.value=[];error.value='';emit('select',null)
  if(!target)return
  if(!props.needsObject){emit('select',target);return}
  busy.value=true
  try {
    const result=await reader.read(target,4*1024*1024),doc=packageJSON(result.bytes) as Row
    validatePackageSchema(doc.schema_version===2?'reference-learning-v2':'reference-learning',doc)
    if(!current(n))return
    objects.value=[...(doc.sources as Row[]),...(doc.results as Row[])].map(item=>({key:String(item.key),title:String(item.title||item.name||item.key)}))
    if(!objects.value.length)error.value='此文件没有可选择的固定成果或来源，请更换文件。'
  }catch(e){if(current(n))error.value=draftError(e)}finally{if(current(n))busy.value=false}
}
onBeforeUnmount(()=>{alive=false;epoch++;api.dispose();reader.dispose()})
</script>
<template><section class="reference-picker" aria-label="选择固定来源">
  <p>从当前有权访问的工作稿中选择。读取所选固定版本后，再选择实际文件与成果；不会按名称自动匹配。</p>
  <fieldset :disabled="disabled||busy"><label>工作稿类型<select aria-label="工作稿类型" v-model="purpose" @change="list()"><option value="learning">学习成果</option><option value="collection">收录内容</option><option value="creation">创作工作稿</option></select></label><label>查找工作稿<input v-model="search" type="search" maxlength="256" placeholder="输入名称或标签" @keydown.enter.prevent="list()"></label><button class="btn" @click="list()">查找固定来源</button>
  <label v-if="items.length">有权访问的工作稿<select aria-label="有权访问的工作稿" v-model="selected" @change="selectDraft"><option value="">请选择工作稿</option><option v-for="item in items" :key="item.draft_id" :value="item.draft_id" :disabled="!item.has_content">{{item.name}}{{item.has_content?'':' · 尚无文件'}}</option></select></label><button v-if="cursor" class="btn" @click="list(true)">更多来源</button>
  <template v-if="opened"><p class="fixed">已固定版本：{{opened.revision_id}}</p><label>参考文件<select aria-label="参考文件" v-model="file" @change="selectFile"><option value="">请选择文件</option><option v-for="entry in opened.manifest.files" :key="entry.file_id" :value="entry.file_id">{{entry.path}}</option></select></label><label v-if="needsObject&&objects.length">固定成果或来源<select aria-label="固定成果或来源" v-model="objectKey" @change="selectObject"><option value="">请选择成果或来源</option><option v-for="entry in objects" :key="entry.key" :value="entry.key">{{entry.title}} · {{entry.key}}</option></select></label></template>
  </fieldset><p v-if="searched&&!items.length&&!busy">没有匹配的可访问工作稿，请调整类型或搜索条件。</p><p v-if="busy" role="status">正在读取当前授权来源…</p><p v-if="error" role="alert" class="error">{{error}}</p>
</section></template>
<style scoped>
.reference-picker{margin-top:14px;padding:14px;border:1px solid var(--line);border-radius:8px;font-size:12px;line-height:1.8}.reference-picker p{overflow-wrap:anywhere}fieldset{border:0;padding:0;min-width:0;display:grid;gap:12px}label{display:grid;gap:6px}input,select{box-sizing:border-box;width:100%;min-width:0;padding:10px;border:1px solid var(--line);border-radius:8px;background:var(--shell-button);color:var(--ink);font:inherit}.fixed{color:var(--muted)}.error{color:var(--danger)}
</style>
