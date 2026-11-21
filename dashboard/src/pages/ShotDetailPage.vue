<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useHost } from '../platform/host'
import { createReadClient } from '../platform/client'
import { publicationIdentity } from '../platform/publication-client'
import { isID, readAsset, readAssetList, type AssetOpen, type AssetFile } from '../platform/world-model'
import { loadAssetResults, type LearningDocument } from '../platform/learning-results'
import { producedMedia } from '../platform/produced-media'
import { extendShotList, recallShotList, rememberShotList, shotPath, type ShotList } from '../platform/shot-navigation'
import { useAssetTarget } from '../platform/route-links'
import { sampleDownload } from '../platform/prompt-samples'
import { record } from '../platform/result-reference'
import { approvedShotPrompt, shotAssetReading, shotAssetReference } from '../platform/shot-asset'
import WorldTrail from '../components/WorldTrail.vue'
import PromptSampleMedia from '../components/PromptSampleMedia.vue'
import AssetCardArt from '../components/AssetCardArt.vue'

const route=useRoute(),router=useRouter(),{appSDK,dashboardHost}=useHost(),client=createReadClient(appSDK,'world')
const identity=publicationIdentity(appSDK.context.get()),asset=ref<AssetOpen>(),document=ref<LearningDocument|null>(null)
const copyStatus=ref(''),copyLocation=ref(''),manualInput=ref<HTMLTextAreaElement|null>(null)
let copyGeneration=0
const error=ref(''),notice=ref(''),manual=ref(''),loading=ref(false),paging=ref(false),busy=ref(false)
const list=ref<ShotList|undefined>(recallShotList(identity)),downloads=ref<Record<string,string>>({})
const assetID=computed(()=>String(route.params.assetId||'')),versionID=computed(()=>String(route.query.version||''))
const shot=computed(()=>document.value?.results.find(r=>r.type_key==='shot'&&r.type_version===1))
const production=computed(()=>asset.value&&shot.value?producedMedia(shot.value,asset.value):null)
const promptText=computed(()=>document.value?approvedShotPrompt(document.value.results):'')
const animation=computed(()=>document.value?.results.find(r=>r.type_key==='camera_animation'&&r.type_version===1&&production.value?.clips.some(c=>record(c).file_id===record(r.payload.previs_ref).file_id)))
const reading=computed(()=>shot.value?shotAssetReading(shot.value):null)
const index=computed(()=>list.value?.items.findIndex(i=>i.asset_id===assetID.value)??-1)
const canNext=computed(()=>index.value>=0&&(index.value<(list.value?.items.length||0)-1||!!list.value?.cursor))
const related=computed(()=>list.value?.items.filter(i=>i.asset_id!==assetID.value).slice(Math.max(0,index.value-2),Math.max(0,index.value-2)+6)||[])
const engineering=computed(()=>{
 if(!animation.value||!asset.value)return []
 const p=animation.value.payload,refs=[p.engineering_ref,p.animation_ref,...(Array.isArray(p.dependency_refs)?p.dependency_refs:[])].map(record)
 return asset.value.manifest.files.filter(f=>refs.some(r=>r.kind==='file'&&r.file_id===f.file_id)&&(/\.blend$/i.test(f.path)||/\/reuse_blender\.py$/i.test(f.path)))
})
const duration=computed(()=>{const p=animation.value?.payload;if(!p)return '见运镜说明';const frames=Number(p.frame_end)-Number(p.frame_start)+1,num=Number(p.fps_num),den=Number(p.fps_den),seconds=frames*den/num;return frames>0&&num>0&&den>0&&Number.isFinite(seconds)?`${seconds.toFixed(1)} 秒`:'见运镜说明'})
let epoch=0,alive=true,hidden=false,revoked=false,controller=new AbortController()
const valid=(n:number)=>alive&&!hidden&&!revoked&&epoch===n
async function load(){
 if(!alive||hidden||revoked)return
 controller.abort();controller=new AbortController();const current=++epoch
 copyGeneration++;copyStatus.value='';copyLocation.value='';manual.value='';loading.value=true;asset.value=undefined;document.value=null;error.value='';notice.value='';manual.value='';downloads.value={};busy.value=false;paging.value=false
 try{
  if(!isID(assetID.value)||!isID(versionID.value))throw Error('镜头地址缺少有效的固定版本。')
  const a=readAsset(await client.call('world.get',{asset_id:assetID.value,asset_version_id:versionID.value}),assetID.value,versionID.value)
  if(a.manifest.asset_type!=='shot')throw Error('该资产不是镜头，请从对应分类打开。')
  const d=await loadAssetResults(client.call,a,controller.signal)
  if(!valid(current))return
  asset.value=a;document.value=d
  if(!list.value?.items.some(i=>i.asset_id===a.asset_id)){
   try{
    const subject=a.manifest.subjects.includes('百例运镜')?'百例运镜':undefined
    const page=readAssetList(await client.call('world.list',{domain:'shot',...(subject?{subject}:{}),limit:100}))
    if(!valid(current))return
    list.value={path:'/shot',items:page.items,cursor:page.next_cursor,scroll:0,subject};rememberShotList(identity,list.value)
   }catch{if(valid(current))notice.value='其他镜头暂时无法读取，当前镜头仍可预演和引用。'}
  }
 }catch(e){if(valid(current))error.value=e instanceof Error?e.message:'无法读取镜头。'}
 finally{if(valid(current)){loading.value=false;dashboardHost.restoreScroll(0)}}
}
async function adjacent(delta:number){
 if(paging.value||index.value<0||!list.value)return
 const current=epoch,next=index.value+delta
 if(next<0)return
 if(next>=list.value.items.length&&list.value.cursor){
  paging.value=true;notice.value=''
  try{
   const snapshot=list.value,base=new URL(snapshot.path,'https://app.invalid')
   const extended=await extendShotList(snapshot,async cursor=>{
    if(!valid(current))throw Error('读取已取消。')
    const args:Record<string,unknown>={domain:'shot',limit:50,cursor}
    if(snapshot.subject)args.subject=snapshot.subject
    for(const k of ['q','asset_type','relation'])if(base.searchParams.get(k))args[k]=base.searchParams.get(k)
    const page=readAssetList(await client.call('world.list',args))
    if(!valid(current))throw Error('读取已取消。')
    return page
   })
   if(!valid(current))return
   list.value=extended;rememberShotList(identity,extended)
  }catch{if(valid(current))notice.value='暂时无法读取下一个镜头，可重试。';return}finally{if(valid(current))paging.value=false}
 }
 const item=list.value.items[next];if(item)void router.replace(shotPath(item))
}
async function copyText(text:string,location:string){
 const current=epoch,generation=++copyGeneration
 copyLocation.value=location;copyStatus.value='正在复制…';manual.value=''
 let timer:ReturnType<typeof setTimeout>|undefined
 const active=()=>valid(current)&&generation===copyGeneration
 try{
  await Promise.race([navigator.clipboard.writeText(text),new Promise<never>((_,reject)=>{timer=setTimeout(()=>reject(Error('clipboard timeout')),1200)})])
  if(active())copyStatus.value=location==='prompt'?'已复制提示词。':'已复制，包含当前镜头的固定版本。'
 }catch{
  if(!active())return
  manual.value=text;copyStatus.value='内容已选中，可直接复制。'
  await nextTick();if(active()){manualInput.value?.focus();manualInput.value?.select()}
 }finally{if(timer)clearTimeout(timer)}
}
function reference(agent=false,location='notes'){
 if(!asset.value||!shot.value)return
 const description=(reading.value?.sections||[]).map(s=>s.title+'\n'+s.lines.join('\n')).join('\n\n')
 void copyText(shotAssetReference(asset.value,shot.value.title,promptText.value,description,agent),location)
}
async function navigate(target:{appId:string;path:string}){
 if(busy.value||!asset.value||hidden||revoked)return
 const current=epoch;busy.value=true;notice.value=''
 try{await appSDK.capabilities.call('navigation.openApp',target,{signal:controller.signal});if(valid(current))notice.value='已打开项目交接，请在项目中完成选择。'}
 catch{if(valid(current))notice.value='无法打开项目，请确认已安装 Project 且有访问权限；当前固定资产版本仍保留。'}
 finally{if(valid(current))busy.value=false}
}
function use(){if(asset.value)void navigate(useAssetTarget(asset.value.asset_id,asset.value.asset_version_id))}
async function download(file:AssetFile){
 if(!asset.value)return;const current=epoch
 try{const r=await sampleDownload(client.call,asset.value,{kind:'file',file_id:file.file_id});if(valid(current))downloads.value={...downloads.value,[file.file_id]:r.path}}
 catch{if(valid(current))notice.value='工程文件暂时无法读取，可重试。'}
}
let fullscreenGuardUntil=0
function fullscreenChanged(){fullscreenGuardUntil=Date.now()+500}
function keys(e:KeyboardEvent){
 if(e.defaultPrevented||window.document.fullscreenElement||Date.now()<fullscreenGuardUntil)return
 if(hidden||revoked||e.altKey||e.ctrlKey||e.metaKey||(e.target as HTMLElement)?.closest('input,textarea,select,video,button,.quiet-video,[contenteditable=true]'))return
 if(e.key==='ArrowLeft'&&index.value>0){e.preventDefault();void adjacent(-1)}
 if(e.key==='ArrowRight'&&canNext.value){e.preventDefault();void adjacent(1)}
 if(e.key==='Escape')void router.push(list.value?.path||'/shot')
}
const stop=appSDK.lifecycle.subscribe(e=>{if(e==='hidden'||e==='dispose'){hidden=true;epoch++;copyGeneration++;manual.value='';copyStatus.value='';controller.abort();loading.value=false;paging.value=false;busy.value=false}if(e==='active'){hidden=false;void load()}})
const stopContext=appSDK.context.subscribe(c=>{if(publicationIdentity(c)===identity)return;revoked=true;epoch++;copyGeneration++;manual.value='';copyStatus.value='';controller.abort();asset.value=undefined;document.value=null;list.value=undefined;loading.value=false;error.value='会话已变化，请重新打开镜头。'})
window.addEventListener('keydown',keys)
window.document.addEventListener('fullscreenchange',fullscreenChanged)
onBeforeUnmount(()=>{alive=false;epoch++;controller.abort();stop();stopContext();client.dispose();window.removeEventListener('keydown',keys);window.document.removeEventListener('fullscreenchange',fullscreenChanged)})
watch(()=>[assetID.value,versionID.value],()=>void load(),{immediate:true})
</script>
<template>
 <main class="shot-detail" aria-label="镜头资产详情">
 <WorldTrail :items="[{label:'数字世界',to:'/'},{label:'镜头',to:list?.path||'/shot'},{label:asset?.manifest.name||'镜头详情'}]"/>
 <div v-if="loading" class="state" role="status">正在读取本镜头的固定版本…</div>
 <div v-else-if="error" class="state" role="alert"><p>{{error}}</p><button v-if="!revoked" class="btn" @click="load">重新读取</button></div>
 <template v-else-if="asset">
 <header class="head"><div><h1>{{asset.manifest.name}}</h1><p>镜头 · 运镜与构图 · 固定版本</p></div><div class="actions"><span v-if="index>=0" class="pager"><button class="btn" :disabled="index<=0||paging" @click="adjacent(-1)">‹ 上一个</button><span>{{index+1}} / {{list?.items.length}}{{list?.cursor?'+':''}}</span><button class="btn" :disabled="!canNext||paging" @click="adjacent(1)">下一个 ›</button></span><button class="btn primary" :disabled="busy" @click="use">选择项目并引用此版本</button></div></header>
 <p v-if="notice" class="notice" role="status">{{notice}}</p>
 <div class="layout">
 <section class="stage" aria-label="本镜头实际预演">
 <template v-if="production&&document"><PromptSampleMedia v-for="(clip,i) in production.clips" :key="`${asset.asset_version_id}:${i}`" :asset="asset" :document="document" :reference="clip" :title="asset.manifest.name+' · 实际预演'" eager previs/></template>
 <div v-else class="state">此固定版本尚未提供制作预演。可参考下方说明或引用已有文件。</div>
 <div class="stage-caption"><span>实际预演</span><span>{{duration}} · 静音循环</span></div>
 </section>
 <aside class="side">
 <section class="panel overview"><h2>概览</h2><dl><dt>资产类型</dt><dd>镜头 · 运镜与构图</dd><dt>预演时长</dt><dd>{{duration}}</dd><dt>标签</dt><dd>{{asset.manifest.subjects.join('、')}}</dd><dt>固定版本</dt><dd class="mono">{{asset.asset_version_id}}</dd></dl><div class="use-block"><p>复制运镜作为参考，或引用固定版本到项目中继续创作。</p><div class="actions"><button class="btn primary" :disabled="busy" @click="use">引用到项目</button><button v-if="shot" class="btn" @click="reference(true,'overview')">复制给智能体</button></div><p v-if="copyLocation==='overview'&&copyStatus" class="copy-status" role="status">{{copyStatus}}</p><textarea v-if="copyLocation==='overview'&&manual" ref="manualInput" :value="manual" readonly rows="6" aria-label="可复制的镜头参考"/></div></section>
 <details v-if="engineering.length" class="panel engineering"><summary><h2>可编辑工程</h2><span>{{engineering.length}} 份文件</span></summary><p class="hint">在 Blender 中打开，或将运镜复用到自己的场景。</p><div v-for="file in engineering" :key="file.file_id" class="file"><span>{{file.path.endsWith('.blend')?'Blender 工程':'跨场景复用脚本'}}</span><a v-if="downloads[file.file_id]" class="btn" :href="downloads[file.file_id]" :download="file.path.split('/').pop()">下载</a><button v-else class="btn" @click="download(file)">读取文件</button></div></details>
 </aside></div>
 <section v-if="reading" class="panel notes"><div class="panel-heading"><h2>运镜说明</h2><div class="actions"><button class="btn" @click="reference()">复制参考说明</button><button class="btn" @click="reference(true)">复制给智能体</button></div></div><div class="reading-grid"><div v-for="section in reading.sections" :key="section.title" class="reading"><h3>{{section.title}}</h3><p v-for="line in section.lines" :key="line">{{line}}</p></div></div><p v-if="copyLocation==='notes'&&copyStatus" class="copy-status" role="status">{{copyStatus}}</p><textarea v-if="copyLocation==='notes'&&manual" ref="manualInput" :value="manual" readonly rows="7" aria-label="可复制的镜头参考"/></section>
 <section v-if="promptText" class="panel prompt-panel"><div class="panel-heading"><h2>运镜提示词</h2><button class="btn" @click="copyText(promptText,'prompt')">复制提示词</button></div><p class="prompt-text">{{promptText}}</p><p v-if="copyLocation==='prompt'&&copyStatus" class="copy-status" role="status">{{copyStatus}}</p><textarea v-if="copyLocation==='prompt'&&manual" ref="manualInput" :value="manual" readonly rows="6" aria-label="可复制的提示词"/></section>
 <section v-if="!document" class="panel"><h2>镜头内容暂时无法读取</h2><button class="btn" @click="load">重新读取</button></section>
 <section v-if="related.length" class="panel related-panel"><h2>其他镜头</h2><div class="related"><button v-for="item in related" :key="item.asset_id" :title="item.name" @click="router.push(shotPath(item))"><AssetCardArt :asset="item"/><span>{{item.name}}</span></button></div></section>
 </template></main>
</template>
<style scoped>
.shot-detail{color:var(--ink);min-width:0}.head{display:flex;justify-content:space-between;align-items:end;gap:12px;flex-wrap:wrap;margin-bottom:16px}.head h1{font-size:21px;line-height:1.4;margin:0}.head p,.hint{font-size:12px;color:var(--muted);line-height:1.7}.head p{margin:3px 0 0}.actions,.pager{display:flex;align-items:center;flex-wrap:wrap;gap:8px}.pager{font-size:12px;color:var(--muted)}
.layout{display:grid;grid-template-columns:minmax(0,1.55fr) minmax(280px,.85fr);gap:20px;align-items:start;margin-bottom:16px}.side{min-width:0;display:grid;gap:12px}.stage{min-width:0;overflow:hidden}.stage-caption{display:flex;justify-content:space-between;gap:12px;padding:9px 2px 0;font-size:12px;line-height:1.6;color:var(--muted)}
.panel{border:1px solid var(--line);border-radius:12px;background:var(--shell-panel);padding:16px;min-width:0}.shot-detail>.panel{margin-top:12px}h2{font-size:14px;margin:0 0 12px}h3{font-size:12px;font-weight:500;color:var(--muted);margin:0 0 5px}p{font-size:13px;line-height:1.8;white-space:pre-wrap;overflow-wrap:anywhere}.panel-heading{display:flex;align-items:center;justify-content:space-between;gap:12px;flex-wrap:wrap;margin-bottom:14px}.panel-heading h2{margin:0}.reading-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:16px 28px}.reading p{margin:0}.use-block{margin-top:14px;padding-top:12px;border-top:1px solid var(--line)}.use-block p{font-size:12px;color:var(--muted);margin:0 0 10px}
dl{display:grid;grid-template-columns:66px minmax(0,1fr);gap:7px 12px;margin:0;font-size:12px;line-height:1.7}dt{color:var(--muted)}dd{margin:0;overflow-wrap:anywhere}.mono{font:11px/1.7 ui-monospace,monospace}.notice,.copy-status{font-size:13px;color:var(--accent)}.file{display:flex;justify-content:space-between;align-items:center;gap:8px;padding:9px 0;border-bottom:1px solid var(--line);font-size:12px}.file>span{overflow-wrap:anywhere;min-width:0}.file .btn{flex-shrink:0}
summary{cursor:pointer;font-size:13px;line-height:1.6}summary h2{display:inline;font-size:13px;margin:0}summary>span{font-size:11px;color:var(--muted);margin-left:8px}.related{display:grid;grid-template-columns:repeat(6,minmax(0,1fr));gap:10px}.related button{padding:0;border:1px solid var(--line);border-radius:8px;overflow:hidden;color:var(--ink);background:var(--shell-button);font:inherit;cursor:pointer}.related button>span:last-child{display:block;font-size:11px;padding:7px;line-height:1.5}.state{display:grid;justify-items:start;gap:10px;padding:28px;border:1px dashed var(--line);border-radius:10px;font-size:13px;color:var(--muted)}textarea{width:100%;box-sizing:border-box;margin-top:15px;background:var(--shell-button);color:var(--ink);border:1px solid var(--line);border-radius:8px;padding:12px;font:inherit}
@media(max-width:1000px){.layout{grid-template-columns:1fr}.related{grid-template-columns:repeat(3,minmax(0,1fr))}}@media(max-width:620px){.panel{padding:14px}.head h1{font-size:20px}.pager{width:100%}.reading-grid{grid-template-columns:1fr}.related{grid-template-columns:repeat(2,minmax(0,1fr))}.stage-caption{font-size:11px}}
</style>
