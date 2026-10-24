<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useHost } from '../platform/host'
import { createDraftAPI, draftError } from '../platform/draft-client'
import { readDraft, readDraftList, readCollectionDocument, object, type DraftItem, type DraftRevision, type CollectionDocument } from '../platform/draft-model'
import { publicationIdentity } from '../platform/publication-client'
import { isID } from '../platform/world-model'
import { createAnalysisSubmission, readAvailability, readTask, promptTargets, taskLabel, shouldPoll, type AnalysisTask, type AnalysisAvailability } from '../platform/analysis-task'
import { readLearningResults, type LearningDocument } from '../platform/learning-results'
import { sampleContentPath, readSampleBytes } from '../platform/prompt-samples'
import WorldTrail from '../components/WorldTrail.vue'
import AnalysisResults from '../components/AnalysisResults.vue'
const { appSDK }=useHost(),api=createDraftAPI(appSDK),route=useRoute(),router=useRouter()
const identity=publicationIdentity(appSDK.context.get())
const submission=createAnalysisSubmission(appSDK,{getItem:key=>localStorage.getItem(key),setItem:(key,value)=>localStorage.setItem(key,value),removeItem:key=>localStorage.removeItem(key)})
const items=ref<DraftItem[]>([]),cursor=ref<string|null>(null),source=ref<DraftRevision|null>(null),document=ref<CollectionDocument|null>(null)
const search=ref(''),loading=ref(false),loadingSource=ref(false),error=ref(''),serviceError=ref(''),taskError=ref('')
const availability=ref<AnalysisAvailability|null>(null),model=ref(''),fileID=ref(''),targets=ref(promptTargets.map(t=>t.key)),budget=ref(''),perCall=ref(''),submitting=ref(false),pending=ref(false)
const tasks=ref<AnalysisTask[]>([]),nextTask=ref(''),task=ref<AnalysisTask|null>(null),learning=ref<LearningDocument|null>(null),learningRevision=ref<DraftRevision|null>(null),loadingTask=ref(false)
const mediaFiles=computed(()=>source.value?.manifest.files.filter(f=>/\.mp4$/i.test(f.path))||[])
const canSubmit=computed(()=>availability.value?.enabled&&source.value&&source.value.lifecycle?.state!=='trashed'&&fileID.value&&model.value&&targets.value.length&&budget.value&&perCall.value&&!submitting.value&&!pending.value)
let generation=0,selection=0,taskGeneration=0,alive=true,hidden=false,revoked=false,timer:ReturnType<typeof setTimeout>|undefined
const live=()=>alive&&!hidden&&!revoked
function stopPolling(){if(timer)clearTimeout(timer);timer=undefined}
function refreshPending(){try{pending.value=!!submission.pending()}catch(e){error.value=draftError(e);pending.value=true}}
async function load(more=false){
 if(!live())return
 const current=++generation;loading.value=true;error.value=''
 if(!more){items.value=[];cursor.value=null}
 try{const previous=cursor.value;const data=readDraftList(await api.request({mode:'list',purpose:'collection',limit:50,...(search.value.trim()?{q:search.value.trim()}:{}),...(more&&previous?{cursor:previous}:{})}),'collection')
 if(!live()||current!==generation)return
 if(more&&data.next_cursor===previous)throw Error('分页未继续，请重新搜索。')
 items.value=[...new Map([...(more?items.value:[]),...data.items].map(x=>[x.draft_id,x])).values()];cursor.value=data.next_cursor
 }catch(e){if(live()&&current===generation)error.value=draftError(e)}finally{if(live()&&current===generation)loading.value=false}
}
async function select(){
 const current=++selection;source.value=null;document.value=null;fileID.value='';loadingSource.value=false
 if(!live()||!route.query.draft||route.query.task)return
 const draft=route.query.draft,revision=route.query.revision
 if(!isID(draft)||!isID(revision)){error.value='选择的来源必须有固定版本。';return}
 loadingSource.value=true;error.value=''
 try{const opened=readDraft(await api.request({mode:'get',draft_id:draft,revision_id:revision}),draft,revision,'collection')
 const entry=opened.manifest.domain_documents.find(d=>d.document_type==='reference-learning'),file=opened.manifest.files.find(f=>f.file_id===entry?.file_id)
 let parsed:CollectionDocument|null=null
 if(file){const data=object(await api.request({mode:'text',content_ref:file.content_ref,access:{world_draft_id:draft,world_draft_revision_id:revision}},'artifact.read'));const v=object(data.version)
 if(data.mode!=='text'||typeof data.text!=='string'||v.store_id!==file.content_ref.store_id||v.artifact_id!==file.content_ref.artifact_id||v.version_id!==file.content_ref.version_id)throw Error('来源正文与所选版本不一致。')
 parsed=readCollectionDocument(data.text)}
 if(!live()||current!==selection)return
 source.value=opened;if(opened.lifecycle?.state==='trashed')error.value='这份来源已移入回收站，请先在收录内容中恢复，再开始新拆解。';document.value=parsed;fileID.value=mediaFiles.value[0]?.file_id||''
 }catch(e){if(live()&&current===selection)error.value=draftError(e)}finally{if(live()&&current===selection)loadingSource.value=false}
}
async function capabilities(){
 if(!live())return;serviceError.value=''
 try{const c=readAvailability(await api.analyzeRequest({mode:'capabilities'}));if(!live())return;availability.value=c;if(!c.models.includes(model.value))model.value=c.models[0]||''}
 catch(e){if(live()){availability.value=null;serviceError.value=draftError(e)}}
}
async function listTasks(more=false){
 if(!live())return
 try{const previous=more?nextTask.value:'';const data=object(await api.analyzeRequest({mode:'list',limit:20,...(previous?{before_task_id:previous}:{})}));if(!live())return
 if(!Array.isArray(data.items)||data.items.length>20||data.next_before_task_id!==undefined&&!isID(data.next_before_task_id)||previous&&data.next_before_task_id===previous)throw Error('拆解列表返回异常，请重新读取。')
 const rows=data.items.map(x=>readTask(x));tasks.value=[...new Map([...(more?tasks.value:[]),...rows].map(x=>[x.task_id,x])).values()];nextTask.value=String(data.next_before_task_id||'')
 }catch(e){if(live())taskError.value=draftError(e)}
}
async function loadTask(){
 stopPolling();const id=route.query.task,current=++taskGeneration
 if(!live()||!id)return
 if(!isID(id)){taskError.value='拆解任务编号无效。';return}
 loadingTask.value=true;taskError.value=''
 try{const t=readTask(await api.analyzeRequest({mode:'get',task_id:id}),id);if(!live()||current!==taskGeneration)return
 task.value=t
 if(t.learning?.state==='saved'&&t.learning.revision&&learningRevision.value?.revision_id!==t.learning.revision.revision_id){
 const d=t.learning.revision,entry=d.manifest.domain_documents.find(x=>x.document_type==='reference-learning'),f=d.manifest.files.find(x=>x.file_id===entry?.file_id)
 if(!f)throw Error('已保存记录中缺少学习正文。')
 const raw=object(await api.request({mode:'download',content_ref:f.content_ref,access:{world_draft_id:d.draft_id,world_draft_revision_id:d.revision_id}},'artifact.read')),v=object(raw.version)
 if(raw.mode!=='download'||v.store_id!==f.content_ref.store_id||v.artifact_id!==f.content_ref.artifact_id||v.version_id!==f.content_ref.version_id||typeof v.mime!=='string'||!Number.isSafeInteger(v.size)||Number(v.size)<0||typeof v.sha256!=='string'||!/^[a-f0-9]{64}$/.test(v.sha256))throw Error('学习正文与固定保存版本不一致。')
 const ctrl=new AbortController(),stopAbort=appSDK.lifecycle.subscribe(e=>{if(e==='hidden'||e==='dispose')ctrl.abort()})
 let text=''
 try{text=await (await readSampleBytes({path:sampleContentPath(raw.content_path),mime:v.mime.split(';')[0].trim().toLowerCase(),size:Number(v.size),sha256:v.sha256},ctrl.signal)).text()}finally{stopAbort()}
 const parsed=readLearningResults(text);if(!live()||current!==taskGeneration)return
 learning.value=parsed;learningRevision.value=d
 }
 if(shouldPoll(t))timer=setTimeout(()=>void loadTask(),5000)
 }catch(e){if(live()&&current===taskGeneration)taskError.value=draftError(e)}finally{if(live()&&current===taskGeneration)loadingTask.value=false}
}
async function submit(resume=false){
 if(submitting.value)return
 submitting.value=true;error.value=''
 try{
 const t=resume?await submission.resume():await submission.start({source:{draft_id:source.value!.draft_id,revision_id:source.value!.revision_id,file_id:fileID.value},model_id:model.value,budget_limit:budget.value.trim(),budget_per_call:perCall.value.trim(),prompt_targets:promptTargets.filter(t=>targets.value.includes(t.key))})
 if(!live())return
 refreshPending();await router.push({path:'/learn',query:{task:t.task_id}});void listTasks()
 }catch(e){if(live()){error.value=draftError(e);refreshPending()}}finally{if(live())submitting.value=false}
}
function choose(item:DraftItem){void router.push({path:'/learn',query:{draft:item.draft_id,revision:item.head_revision_id}})}
function textOf(raw:Record<string,unknown>){const c=raw.content;return c&&typeof c==='object'&&'text'in c&&typeof c.text==='string'?c.text:''}
function boot(){if(!live())return;refreshPending();void load();void capabilities();void listTasks();void select();if(route.query.task)void loadTask()}
watch([()=>route.query.draft,()=>route.query.revision,()=>route.query.task],()=>{stopPolling();taskGeneration++;task.value=null;learning.value=null;learningRevision.value=null;taskError.value='';void select();if(route.query.task)void loadTask()})
const stop=appSDK.lifecycle.subscribe(event=>{if(event==='hidden'||event==='dispose'){hidden=true;generation++;selection++;taskGeneration++;stopPolling();loading.value=false;loadingSource.value=false;loadingTask.value=false}if(event==='active'){hidden=false;submitting.value=false;boot()}})
const stopContext=appSDK.context.subscribe(()=>{if(publicationIdentity(appSDK.context.get())===identity)return;revoked=true;generation++;selection++;taskGeneration++;stopPolling();items.value=[];tasks.value=[];task.value=null;source.value=null;document.value=null;learning.value=null;learningRevision.value=null;error.value='会话已变化，请重新打开数字世界。'})
onBeforeUnmount(()=>{alive=false;generation++;selection++;taskGeneration++;stopPolling();api.dispose();submission.dispose();stop();stopContext()})
boot()
</script>
<template><main class="main"><WorldTrail v-if="!learning" :items="[{label:'数字世界',to:'/'},{label:'拆解作品'}]"/><header v-if="!learning" class="heading"><div class="heading-left"><div class="eyebrow">WORLD · LEARN</div><h1>拆解作品</h1><p>完整分析一次，留下可以参考、复制和复用的创作成果。</p></div><div class="actions"><RouterLink class="btn" to="/">返回数字世界</RouterLink><RouterLink class="btn" to="/collections">收录内容</RouterLink></div></header>
<p v-if="error" class="error" role="alert">{{error}}</p>
<section v-if="pending" class="panel"><h2>上次提交尚未确认</h2><p>恢复原任务，不会另建一次拆解。</p><button class="btn primary" :disabled="submitting" @click="submit(true)">找回原提交</button></section>
<template v-if="route.query.task"><section v-if="!learning" class="panel"><div class="section-head"><h2>{{task?.name||'拆解记录'}}</h2><RouterLink class="btn" to="/learn">返回拆解入口</RouterLink></div><p v-if="loadingTask&&!task" role="status">正在读取原拆解任务…</p><p v-if="taskError" class="error" role="alert">{{taskError}} <button class="btn" :disabled="loadingTask" @click="loadTask">重新读取</button></p>
<template v-if="task"><p class="progress" role="status">{{taskLabel(task)}}</p><p class="muted">任务已留在数字世界。离开页面不会停止后台处理，返回后可继续查看。</p><div v-if="task.progress" class="dimensions"><span>画面与声音：{{task.progress.observation_ready}} 项已保存</span><span>提示词：{{task.progress.prompt_ready}} / {{task.progress.prompt_total}} 批</span><span>分类成果：{{task.progress.result_ready}} / {{task.progress.result_total}} 批</span></div><p v-if="task.error_code==='SOURCE_UNAVAILABLE'" class="error">来源文件无法处理，请回到收录内容核对原件。原任务与失败原因已保留。</p><p class="muted">分析模型：{{task.model_id}} · 预算占用 {{task.budget.reserved}} / {{task.budget.limit}}（渠道计价单位，非实际结算）</p><RouterLink class="btn" :to="{path:'/collections/'+task.source.draft_id,query:{revision:task.source.revision_id}}">查看原作品</RouterLink></template></section>
<section v-if="learning&&learningRevision"><AnalysisResults :document="learning" :revision="learningRevision" :source-name="task?.name"/></section></template>
<div v-else class="learn-layout"><section class="panel"><div class="section-head"><h2>选择已收录的作品</h2><span class="muted">无需项目</span></div><form class="searchbar" @submit.prevent="load()"><input v-model="search" maxlength="256" type="search" aria-label="搜索收录内容" placeholder="搜索已收录的名称或标签"><button class="btn" type="submit" :disabled="loading">搜索</button></form><p v-if="loading&&!items.length" class="muted" role="status">正在读取收录内容…</p><p v-else-if="!items.length&&!error" class="empty">还没有匹配内容，可先去收录作品。</p>
<div class="picker"><button v-for="item in items" :key="item.draft_id" class="option" :class="{selected:route.query.draft===item.draft_id}" :aria-pressed="route.query.draft===item.draft_id" :disabled="!item.has_content" @click="choose(item)"><strong>{{item.name}}</strong><span>{{item.has_content?'选择这份内容':'待添加内容'}}</span></button></div><button v-if="cursor" class="btn" :disabled="loading" @click="load(true)">加载更多</button>
<div v-if="loadingSource" class="preview" role="status">正在读取所选版本…</div><section v-else-if="source" class="preview"><div class="section-head"><h3>{{source.manifest.name}}</h3><button class="btn" @click="router.push('/learn')">更换来源</button></div><template v-for="item in document?.sources||[]" :key="String(item.key)"><pre v-if="textOf(item)">{{textOf(item)}}</pre><p v-else class="muted">{{item.acquisition_state==='declared'?'此项仅收录链接，原件尚未读取。':'原作品已收录。'}}</p></template><label v-if="mediaFiles.length>1">选择原片<select v-model="fileID"><option v-for="f in mediaFiles" :key="f.file_id" :value="f.file_id">{{f.path}}</option></select></label><p v-else-if="!mediaFiles.length" class="muted">这份收录中没有可分析的 MP4 原件。请先补齐原文件；链接或文字不会被当成已读取的影片。</p><RouterLink class="btn" :to="{path:'/collections/'+source.draft_id,query:{revision:source.revision_id}}">查看来源内容</RouterLink></section></section>
<section class="panel"><h2>一次拆解，自动整理所有适用成果</h2><div class="dimensions"><span v-for="name in ['人物与关系','场景与道具','镜头与运镜','音乐与声音','剧作与节奏','提示词与方法']" :key="name">{{name}}</span></div><p class="muted">结果包含来源证据、可复制的提示词与分类内容。缺少证据的部分会标明；工程文件与生成效果只展示实际产出。</p>
<p v-if="serviceError" class="error" role="alert">{{serviceError}} <button class="btn" @click="capabilities">重新检查可用模型</button></p><p v-else-if="!availability" class="muted">正在检查分析服务…</p><p v-else-if="!availability.enabled" class="muted">当前分析服务未开放新任务，已有任务仍可找回。</p>
<form v-if="availability?.enabled" @submit.prevent="submit()"><div class="settings"><label>分析模型<select v-model="model" required><option v-for="m in availability.models" :key="m" :value="m">{{m}}</option></select></label><label>整次预算上限<input v-model="budget" inputmode="decimal" placeholder="填写本次总上限" required></label><label>单次调用上限<input v-model="perCall" inputmode="decimal" placeholder="不超过总预算" required></label></div><p class="muted">金额采用渠道计价单位。整次上限约束所有分析调用，预算用尽会保留已有结果并停止新调用。</p><fieldset><legend>整理哪些模型的提示词</legend><label v-for="target in promptTargets" :key="target.key" class="check"><input v-model="targets" type="checkbox" :value="target.key">{{target.requested_label}}</label></fieldset><p class="muted">提示词目标与分析模型分开；实际目标模型版本与生成效果在成果中单独标注。</p><button class="btn primary" :disabled="!canSubmit" type="submit">{{submitting?'正在提交…':'开始完整拆解'}}</button><p class="muted">当前支持 MP4，原件最多 {{Math.floor(availability.max_media_bytes/1024/1024)}} MiB。提交后自动处理并保存到数字世界。</p></form></section></div>
<section v-if="!route.query.task" class="panel"><div class="section-head"><h2>我的拆解记录</h2><button class="btn" @click="taskError='';listTasks()">刷新记录</button></div><p v-if="taskError&&!route.query.task" class="error" role="alert">{{taskError}}</p><p v-if="!tasks.length" class="muted">提交后的任务会留在这里，随时回来查看。</p><div class="task-list"><RouterLink v-for="t in tasks" :key="t.task_id" :to="{path:'/learn',query:{task:t.task_id}}"><strong>{{t.name}}</strong><span>{{taskLabel(t)}}</span></RouterLink></div><button v-if="nextTask" class="btn" @click="listTasks(true)">更多记录</button></section>
</main></template>
<style scoped>
.learn-layout{display:grid;grid-template-columns:minmax(0,1.08fr) minmax(0,1fr);gap:24px;align-items:start}.learn-layout>.panel{min-width:0}.learn-layout .settings{grid-template-columns:1fr 1fr}.learn-layout .settings>label:first-child{grid-column:1/-1}@media(max-width:1100px){.learn-layout{grid-template-columns:1fr}}

.eyebrow{color:var(--accent);font-size:11px;letter-spacing:.12em}.actions{display:flex;flex-wrap:wrap;gap:10px}.panel{padding:26px;border:1px solid var(--line);border-radius:12px;background:var(--shell-panel);margin-bottom:20px;color:var(--ink)}h2{font-size:18px}h3{font-size:16px}.section-head{display:flex;justify-content:space-between;align-items:center;gap:16px}.muted,.option span{font-size:12px;color:var(--muted);line-height:1.8}.searchbar{display:flex;gap:12px;margin:22px 0}input,select{padding:11px 12px;border:1px solid var(--line);border-radius:8px;color:var(--ink);background:var(--shell-button);font:inherit;max-width:100%;box-sizing:border-box}.searchbar input{width:min(500px,100%)}.picker{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:12px;margin-bottom:18px}.option{text-align:left;padding:18px;border:1px solid var(--line);border-radius:9px;background:var(--shell-button);display:grid;gap:8px}.option.selected{border-color:var(--accent);box-shadow:inset 0 0 0 1px var(--accent)}.preview{border:1px solid var(--line);border-radius:8px;padding:22px;margin-top:24px}.preview p{margin:12px 0}.preview pre{white-space:pre-wrap;overflow-wrap:anywhere;font:inherit;line-height:1.8;margin:20px 0}.dimensions{display:flex;flex-wrap:wrap;gap:10px;margin:22px 0}.dimensions span{padding:9px 12px;background:var(--shell-button);border:1px solid var(--line);border-radius:8px;font-size:13px}.error{color:var(--danger);line-height:1.8}.empty{padding:30px 0;color:var(--muted)}.settings{display:grid;grid-template-columns:2fr 1fr 1fr;gap:18px;margin:26px 0 12px}label{display:grid;gap:8px;font-size:12px}fieldset{border:0;padding:0;margin:22px 0;display:flex;gap:20px;flex-wrap:wrap}legend{font-size:13px;margin-bottom:14px}.check{display:flex;align-items:center;gap:8px}.task-list{display:grid;gap:10px;margin:18px 0}.task-list a{display:flex;justify-content:space-between;gap:16px;padding:16px;border:1px solid var(--line);border-radius:8px;color:var(--ink);text-decoration:none}.task-list span{color:var(--muted);font-size:12px}.progress{color:var(--accent);font-size:20px;margin:24px 0}.btn:disabled{opacity:.5;cursor:not-allowed}@media(max-width:800px){.picker,.settings{grid-template-columns:1fr}.panel{padding:20px}.section-head,.task-list a{align-items:start;flex-direction:column}}
</style>
