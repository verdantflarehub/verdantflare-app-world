<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useHost } from '../platform/host'
import { createDraftAPI, draftError } from '../platform/draft-client'
import { readDraft, readDraftList, readCollectionDocument, object, type DraftItem, type DraftRevision, type CollectionDocument } from '../platform/draft-model'
import { publicationIdentity } from '../platform/publication-client'
import { isID } from '../platform/world-model'
const { appSDK }=useHost(), api=createDraftAPI(appSDK), route=useRoute(), router=useRouter()
const identity=publicationIdentity(appSDK.context.get())
const items=ref<DraftItem[]>([]),cursor=ref<string|null>(null),source=ref<DraftRevision|null>(null),document=ref<CollectionDocument|null>(null)
const search=ref(''),loading=ref(false),loadingSource=ref(false),error=ref('')
let generation=0,selection=0,alive=true,hidden=false
async function load(more=false){
  if(hidden||!alive)return
  const current=++generation;loading.value=true;error.value=''
  if(!more){items.value=[];cursor.value=null}
  try{
    const previous=cursor.value
    const data=readDraftList(await api.request({mode:'list',purpose:'collection',limit:50,...(search.value.trim()?{q:search.value.trim()}:{}),...(more&&previous?{cursor:previous}:{})}),'collection')
    if(!alive||hidden||current!==generation)return
    if(more&&data.next_cursor===previous)throw Error('分页未继续，请重新搜索。')
    items.value=[...new Map([...(more?items.value:[]),...data.items].map(x=>[x.draft_id,x])).values()];cursor.value=data.next_cursor
  }catch(cause){if(alive&&current===generation)error.value=draftError(cause)}finally{if(alive&&current===generation)loading.value=false}
}
async function select(){
  const current=++selection;source.value=null;document.value=null;loadingSource.value=false
  if(!alive||hidden||!route.query.draft)return
  const draft=route.query.draft,revision=route.query.revision
  if(!isID(draft)||!isID(revision)){error.value='选择的来源必须有固定版本。';return}
  loadingSource.value=true;error.value=''
  try{
    const opened=readDraft(await api.request({mode:'get',draft_id:draft,revision_id:revision}),draft,revision,'collection')
    const entry=opened.manifest.domain_documents.find(d=>d.document_type==='reference-learning'),file=opened.manifest.files.find(f=>f.file_id===entry?.file_id)
    let parsed:CollectionDocument|null=null
    if(file){
      const data=object(await api.request({mode:'text',content_ref:file.content_ref,access:{world_draft_id:draft,world_draft_revision_id:revision}},'artifact.read'))
      const v=object(data.version)
      if(data.mode!=='text'||typeof data.text!=='string'||v.store_id!==file.content_ref.store_id||v.artifact_id!==file.content_ref.artifact_id||v.version_id!==file.content_ref.version_id)throw Error('来源正文与所选版本不一致。')
      parsed=readCollectionDocument(data.text)
    }
    if(!alive||hidden||current!==selection)return
    source.value=opened;document.value=parsed
  }catch(cause){if(alive&&current===selection)error.value=draftError(cause)}finally{if(alive&&current===selection)loadingSource.value=false}
}
function choose(item:DraftItem){void router.replace({path:'/learn',query:{draft:item.draft_id,revision:item.head_revision_id}})}
function textOf(raw:Record<string,unknown>){const c=raw.content;return c&&typeof c==='object'&&'text'in c&&typeof c.text==='string'?c.text:''}
watch(()=>[route.query.draft,route.query.revision],()=>void select(),{immediate:true})
void load()
const stop=appSDK.lifecycle.subscribe(event=>{if(event==='hidden'||event==='dispose'){hidden=true;generation++;selection++;loading.value=false;loadingSource.value=false}if(event==='active'){hidden=false;void load();void select()}})
const stopContext=appSDK.context.subscribe(()=>{if(publicationIdentity(appSDK.context.get())===identity)return;generation++;selection++;items.value=[];source.value=null;document.value=null;hidden=true;error.value='会话已变化，请重新打开数字世界。'})
onBeforeUnmount(()=>{alive=false;generation++;selection++;api.dispose();stop();stopContext()})
</script>
<template><main class="main"><header class="heading"><div class="heading-left"><div class="eyebrow">WORLD · LEARN</div><h1>拆解作品</h1><p>整份作品、全部适用维度。拆解成果按类型留在数字世界。</p></div><RouterLink class="btn" to="/collections">管理收录内容</RouterLink></header>
<section class="panel"><div class="section-head"><h2>从收录内容选择作品</h2><span>无需项目</span></div><form class="searchbar" @submit.prevent="load()"><input v-model="search" maxlength="256" type="search" aria-label="搜索收录内容" placeholder="搜索已收录的名称或标签"><button class="btn" type="submit" :disabled="loading">搜索</button></form><p v-if="error" class="error" role="alert">{{error}}</p><p v-if="loading&&!items.length" class="muted" role="status">正在读取收录内容…</p><p v-else-if="!items.length&&!error" class="empty">没有找到收录内容。可以先在收录内容中保存作品或参考。</p>
<div class="picker"><button v-for="item in items" :key="item.draft_id" class="option" :class="{selected:route.query.draft===item.draft_id}" :aria-pressed="route.query.draft===item.draft_id" :disabled="!item.has_content" @click="choose(item)"><strong>{{item.name}}</strong><span>{{item.has_content?'已保存来源 · 点击预览':'待添加内容'}}</span></button></div><button v-if="cursor" class="btn" :disabled="loading" @click="load(true)">加载更多</button>
<div v-if="loadingSource" class="preview" role="status">正在读取所选固定版本…</div><section v-else-if="source" class="preview"><div class="section-head"><h3>{{source.manifest.name}}</h3><button class="btn" @click="router.replace('/learn')">更换来源</button></div><p class="muted">已固定来源版本；此后的收录编辑不会悄悄替换本次选择。</p><template v-for="item in document?.sources||[]" :key="String(item.key)"><pre v-if="textOf(item)">{{textOf(item)}}</pre><p v-else class="muted">{{item.acquisition_state==='declared'?'此项仅收录链接，原件尚未读取。':'作品保存在来源文件中。'}}</p></template><RouterLink class="btn" :to="{path:'/collections/'+source.draft_id,query:{revision:source.revision_id}}">查看来源内容</RouterLink></section>
</section><section class="panel"><h2>一次拆解，会得到什么</h2><div class="dimensions"><span v-for="name in ['人物与关系','场景与道具','镜头与运镜','音乐与声音','剧作与节奏','提示词与方法']" :key="name">{{name}}</span></div><p class="muted">根据作品类型核对适用范围；实际成果包括可编辑正文、模型提示词和具备条件的工程文件。</p><div class="service-state" role="status"><strong>分析服务尚未接通</strong><p>现在可选择、读取和核对已收录作品。尚未启动分析，也没有生成拆解成果。</p><button class="btn primary" disabled>开始完整拆解</button></div></section>
</main></template>
<style scoped>.eyebrow{color:var(--accent);font-size:11px;letter-spacing:.12em}.panel{padding:26px;border:1px solid var(--line);border-radius:12px;background:var(--shell-panel);margin-bottom:20px;color:var(--ink)}h2{font-size:18px}h3{font-size:16px}.section-head{display:flex;justify-content:space-between;align-items:center;gap:16px}.section-head>span,.muted,.option span{font-size:12px;color:var(--muted)}.searchbar{display:flex;gap:12px;margin:22px 0}input{width:min(500px,100%);padding:11px 12px;border:1px solid var(--line);border-radius:8px;color:var(--ink);background:var(--shell-button)}.picker{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px;margin-bottom:18px}.option{text-align:left;padding:18px;border:1px solid var(--line);border-radius:9px;background:var(--shell-button);display:grid;gap:8px}.option.selected{border-color:var(--accent);box-shadow:inset 0 0 0 1px var(--accent)}.preview{border:1px solid var(--line);border-radius:8px;padding:22px;margin-top:24px}.preview p{margin:12px 0}.preview pre{white-space:pre-wrap;overflow-wrap:anywhere;font:inherit;line-height:1.8;margin:20px 0}.dimensions{display:flex;flex-wrap:wrap;gap:10px;margin:22px 0}.dimensions span{padding:9px 12px;background:var(--shell-button);border:1px solid var(--line);border-radius:8px;font-size:13px}.service-state{border-top:1px solid var(--line);margin-top:24px;padding-top:22px}.service-state p{margin:8px 0 18px;color:var(--muted);font-size:13px}.error{color:var(--danger)}.empty{padding:30px 0;color:var(--muted)}@media(max-width:800px){.picker{grid-template-columns:1fr}.panel{padding:20px}.section-head{align-items:start}}
</style>
