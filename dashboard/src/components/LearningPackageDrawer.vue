<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, shallowRef } from 'vue'
import { useRouter } from 'vue-router'
import WorldDrawer from './WorldDrawer.vue'
import PackageReferencePicker from './PackageReferencePicker.vue'
import { useHost } from '../platform/host'
import { publicationIdentity, publicationPrincipal } from '../platform/publication-client'
import { inspectLearningPackage, packageCanonical, verifyPackageBaseline, type PackageInspection } from '../platform/learning-package'
import { exportLearningPackage, type PackageExport } from '../platform/package-export'
import { preparePackageImport, mapPackageImport, type ImportPreview } from '../platform/package-import'
import { validateImportedDocument } from '../platform/package-semantics'
import { createPackageReferenceReader } from '../platform/package-reference'
import { createPackageRecovery, type PackageRecovery } from '../platform/package-recovery'
import { createPackageSave, type ImportChoice } from '../platform/package-save'
import { operationID } from '../platform/collection-save'
import type { DraftRevision } from '../platform/draft-model'

const emit=defineEmits<{saved:[revision:DraftRevision]}>()
type Row = Record<string, unknown>
interface Mapping { source: Row; choice: string; custom: string; status: 'pending'|'ready'|'reused'|'failed' }
const { appSDK } = useHost(), router = useRouter(), identity = publicationIdentity(appSDK.context.get())
const panel = ref<InstanceType<typeof WorldDrawer>|null>(null), mode = ref<'export'|'import'>('export'), stage = ref('choose'), busy = ref(false), error = ref('')
const title = ref(''), root = shallowRef<Row>({}), selection = ref<string[]>([]), total = ref(0), scope = ref<'complete'|'selected'>('complete'), media = ref(false)
const exported = shallowRef<PackageExport|null>(null), inspection = shallowRef<PackageInspection|null>(null), preview = shallowRef<ImportPreview|null>(null), rawPackage = shallowRef<Uint8Array|null>(null)
const pickerVisible = ref(false)
const mappings = ref<Mapping[]>([]), checked = ref(false), sourceVerified = ref(false), name = ref(''), recovery = shallowRef<PackageRecovery|null>(null), saved = shallowRef<DraftRevision|null>(null)
const store = createPackageRecovery(publicationPrincipal(appSDK.context.get()))
let reader = createPackageReferenceReader(appSDK), saver = createPackageSave(appSDK, store, reader.authorize)
let generation = 0, controller = new AbortController(), alive = true, hidden = false, revoked = false
const missingLabels = { not_included:'未选择附带', unavailable:'当前不可读取', unsupported:'暂不支持', over_limit:'超过容量限制' }
const mappingComplete = computed(() => checked.value && mappings.value.every(m => m.status === 'ready' || m.status === 'reused'))
const prettySize = (n:number) => n >= 1024*1024 ? `${(n/1024/1024).toFixed(1)} MiB` : `${Math.ceil(n/1024)} KiB`
const copy = <T,>(value:T):T => JSON.parse(JSON.stringify(value))
function message(e: unknown) {
  const code = e && typeof e === 'object' && 'code' in e ? String(e.code) : ''
  if (code === 'PACKAGE_LIMIT') return '文件超过本次容量上限，请保留原件并调整参考文件选择。'
  if (code === 'STORAGE_UNAVAILABLE') return '浏览器无法保存恢复记录，已停止新写入。请保留原包。'
  if (code === 'SESSION_REVOKED') return '会话已变化，请重新打开数字世界；原操作恢复记录仍保留。'
  if (code === 'DASHBOARD_HIDDEN') return '读取已暂停，返回页面后可继续原操作。'
  if (code === 'REVISION_CONFLICT' || code === 'TERMINAL_FAILURE') return '保存已被拒绝或版本发生冲突。原操作与已上传文件仍保留，可核对失败状态后返回编辑。'
  if (/^(INVALID_|IMPORT_SEMANTICS_|MISSING_|UNSUPPORTED_|BASELINE_|INCOMPLETE_|DUPLICATE_|CYCLIC_|SELECTED_|SOURCE_BASELINE)/.test(code)) return '包内容、固定原件或依赖关系不符合要求。请核对原包、编辑稿与映射后重试。'
  return '暂时无法完成核验或保存。请检查当前来源权限与文件；已有操作会沿原编号恢复。'
}
function active(epoch:number) { return alive && !hidden && !revoked && epoch === generation }
function resetIO() { controller.abort(); controller = new AbortController(); reader.dispose(); saver.dispose(); reader = createPackageReferenceReader(appSDK); saver = createPackageSave(appSDK,store,reader.authorize) }
function close() { pickerVisible.value=false;generation++; controller.abort(); reader.dispose(); saver.dispose(); busy.value=false; panel.value?.close() }
async function showExport(reference:Row, label:string, selected:string[], count:number) {
  if (hidden || revoked) return
  close(); resetIO(); mode.value='export';stage.value='choose';error.value='';title.value=label;root.value=copy(reference);selection.value=[...selected];total.value=count;scope.value=selected.length?'selected':'complete';media.value=false;exported.value=null
  await nextTick(); panel.value?.showModal()
}
async function showImport() {
  if (hidden || revoked) return
  close();resetIO();pickerVisible.value=true;mode.value='import';error.value='';const epoch=generation
  await nextTick();panel.value?.showModal();busy.value=true
  try { const record=await store.read();if(!active(epoch))return;recovery.value=record;stage.value=record?'recover':inspection.value?'review':'choose' }
  catch(e){if(active(epoch))error.value=message(e)}finally{if(active(epoch))busy.value=false}
}
async function buildExport() {
  if(busy.value)return;const epoch=generation;busy.value=true;error.value=''
  try{const result=await exportLearningPackage(reader,root.value,scope.value==='complete'?null:selection.value,media.value,controller.signal);if(active(epoch)){exported.value=result;stage.value='export-preview'}}
  catch(e){if(active(epoch))error.value=message(e)}finally{if(active(epoch))busy.value=false}
}
function download() {
  if(!exported.value || hidden || revoked)return
  const url=URL.createObjectURL(new Blob([new Uint8Array(exported.value.bytes)],{type:'application/zip'})),a=window.document.createElement('a')
  a.href=url;a.download=`world-learning-${exported.value.manifest.package_id}.zip`;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)
}
function entryOptions(source:Row) {
  const manifest=inspection.value?.manifest;if(!manifest)return []
  const base=manifest.entries.find(e=>e.role==='baseline')!.source_ref
  return manifest.entries.filter(e=>e.role==='reference' && (packageCanonical(e.source_ref)===packageCanonical(source) || source.kind==='file' && e.source_ref.file_id===source.file_id && e.source_ref.kind===base.kind && ['draft_id','revision_id','project_id','asset_id','asset_version_id'].every(k=>e.source_ref[k]===base[k]) && !e.source_ref.object_key))
}
async function chooseFile(event:Event) {
  const file=(event.target as HTMLInputElement).files?.[0];if(!file || busy.value)return
  const epoch=++generation;busy.value=true;error.value='';checked.value=false;sourceVerified.value=false;saved.value=null
  inspection.value=null;preview.value=null;rawPackage.value=null;mappings.value=[]
  try {
    if(file.size>256*1024*1024)throw {code:'PACKAGE_LIMIT'}
    const bytes=new Uint8Array(await file.arrayBuffer());if(!active(epoch))return
    const inspected=await inspectLearningPackage(bytes,controller.signal),prepared=preparePackageImport(inspected)
    if(!active(epoch))return
    inspection.value=inspected;preview.value=prepared;rawPackage.value=bytes;name.value=file.name.replace(/\.zip$/i,'').slice(0,256)||'导入的学习成果'
    mappings.value=prepared.required_refs.map(source=>({source,choice:'',custom:'',status:'pending'}));stage.value='review'
  }catch(e){if(active(epoch))error.value=message(e)}finally{if(active(epoch))busy.value=false}
}
function choices(): ImportChoice[] {
  const baseline=inspection.value!.manifest.entries.find(e=>e.role==='baseline')!.source_ref
  return mappings.value.map(m=>{
    const source=copy(m.source)
    if(m.choice.startsWith('entry:'))return {source,entry_key:m.choice.slice(6)}
    if(m.choice==='original') { const target=source.kind==='file'?{...baseline,file_id:source.file_id}:source;return {source,target} }
    if(m.choice==='custom'||m.choice==='picker') return {source,target:JSON.parse(m.custom)}
    throw Error('mapping missing')
  })
}
function changeMappingMode(mapping:Mapping){mapping.custom='';dirtyMapping()}
function dirtyMapping(){checked.value=false;for(const m of mappings.value)m.status='pending'}
async function checkMappings() {
  if(busy.value||!preview.value||!inspection.value)return
  const epoch=generation;busy.value=true;error.value='';checked.value=false
  try {
    const selected=choices(),ids=new Map(inspection.value.manifest.entries.map(e=>[e.key,operationID()]))
    const document=mapPackageImport(preview.value,selected.map(c=>({source:c.source,state:c.entry_key?'imported':'reused',target:c.entry_key?{kind:'file',file_id:ids.get(c.entry_key)}:c.target})))
    validateImportedDocument(document,[...ids.values()])
    const targets=selected.filter(c=>c.target).map(c=>c.target!)
    await reader.authorize(targets,document);if(!active(epoch))return
    checked.value=true;for(const m of mappings.value)m.status=m.choice.startsWith('entry:')?'ready':'reused'
  }catch(e){if(active(epoch)){error.value=message(e);for(const m of mappings.value)m.status='failed'}}finally{if(active(epoch))busy.value=false}
}
async function verifySource() {
  if(!inspection.value||busy.value)return;const epoch=generation;busy.value=true;error.value=''
  try{const base=inspection.value.manifest.entries.find(e=>e.role==='baseline')!;const current=await reader.read(base.source_ref,4*1024*1024);await verifyPackageBaseline(inspection.value,current.bytes);if(active(epoch))sourceVerified.value=true}
  catch(e){if(active(epoch)){sourceVerified.value=false;error.value='未能从当前授权来源核对原件。包内完整性检查不等于来源可信，导入稿仍须重新审核。'}}finally{if(active(epoch))busy.value=false}
}
async function save(resume=false) {
  if(busy.value||!resume&&(!mappingComplete.value||!rawPackage.value||!name.value.trim()))return
  const epoch=generation;busy.value=true;error.value=''
  try {const revision=resume?await saver.resume():await saver.start(rawPackage.value!,name.value,[],choices());if(active(epoch)){saved.value=revision;recovery.value=null;stage.value='saved';emit('saved',revision)}}
  catch(e){if(active(epoch)){error.value=message(e);try{recovery.value=await store.read();if(recovery.value)stage.value='recover'}catch{error.value+=' 恢复记录暂无法读取，请保留原包。'}}}finally{if(active(epoch))busy.value=false}
}
async function openSaved(){if(!saved.value)return;const revision=saved.value;close();await router.push({path:'/learn',query:{learning:revision.draft_id,revision:revision.revision_id}})}
async function editFailed() {
  if(busy.value||!recovery.value?.progress.failure)return
  const epoch=generation;busy.value=true;error.value=''
  try {
    const record=recovery.value,inspected=await inspectLearningPackage(record.package_bytes,controller.signal),prepared=preparePackageImport(inspected)
    if(!active(epoch))return
    // Restore the local editor before releasing the failed operation, so closing
    // the drawer during the status query cannot discard the user's input.
    inspection.value=inspected;preview.value=prepared;rawPackage.value=record.package_bytes
    name.value=String(record.intent.plan.name||'导入的学习成果');checked.value=false;sourceVerified.value=false
    const previous=record.intent.plan.choices as ImportChoice[]
    mappings.value=prepared.required_refs.map(source=>{
      const choice=previous.find(c=>packageCanonical(c.source)===packageCanonical(source))
      return {source,choice:choice?.entry_key?'entry:'+choice.entry_key:choice?.target?'custom':'',custom:choice?.target?JSON.stringify(choice.target):'',status:'pending'}
    })
    await saver.returnFailedToEditing()
    if(active(epoch)){recovery.value=null;stage.value='review'}
  }catch(e){if(active(epoch))error.value=message(e)}finally{if(active(epoch))busy.value=false}
}
const stopLifecycle=appSDK.lifecycle.subscribe(e=>{hidden=e==='hidden'||e==='dispose';if(hidden)close()})
const stopContext=appSDK.context.subscribe(c=>{if(publicationIdentity(c)===identity)return;revoked=true;close();inspection.value=null;preview.value=null;rawPackage.value=null;exported.value=null;recovery.value=null;saved.value=null;mappings.value=[]})
onBeforeUnmount(()=>{alive=false;close();store.dispose();stopLifecycle();stopContext()})
defineExpose({showExport,showImport})
</script>

<template><WorldDrawer ref="panel" :title="mode==='export'?'导出成果包':'导入成果包'" @request-close="close">
  <p v-if="busy" class="note" role="status">正在核对内容与原操作，请稍候…</p><p v-if="error" class="error" role="alert">{{error}}</p>
  <template v-if="mode==='export'">
    <h3>{{title}}</h3><p class="note">从当前固定版本导出。原始正文、关系和用途审核一并保留。</p>
    <template v-if="stage==='choose'"><fieldset :disabled="busy"><legend>选择导出范围</legend><label class="check"><input v-model="scope" type="radio" value="complete">完整报告 · {{total}} 项</label><label class="check"><input v-model="scope" type="radio" value="selected" :disabled="!selection.length">指定成果 · {{selection.length}} 项</label></fieldset><p class="note">搜索和用途筛选不会删减完整报告；指定成果仍携带完整原件和必要关系。</p><label class="check"><input v-model="media" type="checkbox" :disabled="busy">附带可读取的参考文件</label><p class="note">逐文件核对权限。无法读取的依赖仅保留已披露的引用及缺项原因。</p></template>
    <template v-else-if="exported"><h3>包内容预览</h3><dl><dt>用途范围</dt><dd>{{exported.manifest.scope==='complete'?'完整报告':'指定成果'}} · {{exported.results}} 项</dd><dt>原始正文</dt><dd>保留完整固定原件，另有独立可编辑副本</dd><dt>参考文件</dt><dd>{{exported.reference_files}} 个</dd><dt>包大小</dt><dd>{{prettySize(exported.bytes.length)}}</dd></dl><p v-if="!exported.manifest.missing_items.length" class="note">此次选择没有记录文件缺项。实际使用仍需当前权限与业务校验。</p><ul v-else class="mapping-list"><li v-for="(item,i) in exported.manifest.missing_items" :key="i"><strong>参考 {{i+1}} · {{item.required?'导入必需':'非必需'}}</strong><span>{{missingLabels[item.reason]}}</span></li></ul><p class="note">下载不等于入库。可在本地编辑 working/document.json，再作为新工作稿导入。</p></template>
  </template>
  <template v-else-if="stage==='choose'"><h3>把学习成果带回工作区</h3><p class="note">先检查包内容、原件和编辑稿，再核对目标环境。确认保存前不创建工作稿或上传文件。</p><label class="field">选择 ZIP 文件<input type="file" accept=".zip,application/zip" :disabled="busy" @change="chooseFile"></label><p class="note">不执行包内脚本或工程。原环境标识仅用于溯源，不自动作为当前可用引用。</p></template>
  <template v-else-if="(stage==='review'||stage==='confirm')&&inspection&&preview">
    <p class="step">{{stage==='confirm'?'03 确认保存':'02 核对与映射'}}</p><h3>{{name}}</h3><p class="note">正文 v{{inspection.working.schema_version}} · 所选 {{preview.selected_keys.length}} 项 · 额外依赖成果 {{preview.dependency_keys.length}} 项</p>
    <section class="summary"><h3>原件与编辑稿</h3><p>{{inspection.changed_keys.length?'编辑稿与导出基线有修改。':'编辑稿与导出基线一致。'}}原始正文保持独立，旧运行和质量结论只作为历史证据。</p><p class="note">{{sourceVerified?'已通过当前授权来源回读核对原件字节。':'原件来源尚未核验；包内摘要一致不证明来源真实。'}}</p><button v-if="stage==='review'&&!sourceVerified" class="btn" :disabled="busy" @click="verifySource">核验原件来源</button></section>
    <section v-if="inspection.manifest.missing_items.length" class="summary"><h3>随包记录的缺项</h3><ul class="mapping-list"><li v-for="(item,i) in inspection.manifest.missing_items" :key="i"><strong>参考 {{i+1}} · {{item.required?'必需依赖，需在下方完成映射':'非必需，不阻止正文保存'}}</strong><span>{{missingLabels[item.reason]}}</span></li></ul><p class="note">这些是导出时记录的缺项。目标映射以本轮核验为准；缺少的生成效果与重建预演不会变成已验证。</p></section>
    <h3>目标环境映射</h3><p v-if="!mappings.length" class="note">没有需要映射的外部引用，仍需核对正文关系。</p>
    <ol class="mapping-list"><li v-for="(mapping,i) in mappings" :key="i"><strong>参考 {{i+1}}{{mapping.source.object_key?' · 固定对象':''}}</strong><span>{{({pending:'待核验',ready:'随包文件待导入',reused:'已核验复用',failed:'核验未通过'})[mapping.status]}}</span><template v-if="stage==='review'"><label class="field">目标来源<select v-model="mapping.choice" :aria-label="'参考 '+(i+1)+' 目标来源'" :disabled="busy" @change="changeMappingMode(mapping)"><option value="">请选择映射方式</option><option v-for="entry in entryOptions(mapping.source)" :key="entry.key" :value="'entry:'+entry.key">随包参考文件 · {{entry.path}}</option><option value="original">核验并复用原固定引用</option><option value="picker">选择当前环境有权访问的固定来源</option><option value="custom">使用已有固定引用（高级）</option></select></label><PackageReferencePicker v-if="mapping.choice==='picker'&&pickerVisible" :disabled="busy" :needs-object="!!mapping.source.object_key" :purpose="String(mapping.source.purpose||'learning_source')" @select="mapping.custom=$event?JSON.stringify($event):'';dirtyMapping()"/><p v-if="mapping.choice==='picker'&&mapping.custom" class="note">已选择固定来源，仍须核验全部映射与关系。</p><label v-if="mapping.choice==='custom'" class="field">目标固定引用<textarea v-model="mapping.custom" :disabled="busy" rows="4" placeholder="粘贴目标环境的固定引用 JSON（含固定版本与文件）" @input="dirtyMapping"/></label><details><summary>查看原引用定位</summary><pre>{{JSON.stringify(mapping.source,null,2)}}</pre></details></template></li></ol>
    <template v-if="stage==='review'"><button class="btn" :disabled="busy" @click="checkMappings">核验全部映射与关系</button><p class="note">必需关系未闭合时不能保存。关闭面板可保留当前检查，保存开始后可在刷新后恢复原操作。</p></template>
    <template v-else><label class="field">新工作稿名称<input v-model="name" maxlength="256" :disabled="busy"></label><p class="note">保存将创建新的学习工作修订，原件不覆盖、不自动登记资产。用途与生成效果需要在当前环境重新验证。</p></template>
  </template>
  <template v-else-if="stage==='recover'&&recovery"><h3>继续原导入操作</h3><p>尚未确认保存完成，请沿原操作恢复，避免重复创建。</p><dl><dt>文件阶段</dt><dd>已记录 {{Object.keys(recovery.progress.uploaded||{}).length}} 个上传回执</dd><dt>工作修订</dt><dd>{{recovery.progress.saved?'已收到回执，等待固定回读':recovery.progress.failure?'保存已被拒绝或冲突':'尚未确认最终结果'}}</dd></dl><p class="note">原包、固定映射和操作编号仍保留在本浏览器。关闭后可稍后继续。</p><p v-if="recovery.progress.failure" class="note">返回编辑前会再次查询失败状态；只有确认失败才解除原操作。已上传文件不会删除。请保留原 ZIP；修改映射后须重新核验并确认保存，才会创建新工作稿。</p></template>
  <template v-else-if="stage==='saved'&&saved"><h3>新工作稿已保存</h3><p>固定修订已回读核对，原件保持独立。尚未登记为资产。</p><p class="note">{{saved.manifest.name}}</p></template>
  <template #footer>
    <button class="btn" @click="close">{{busy||stage==='recover'?'稍后继续':'关闭'}}</button>
    <template v-if="mode==='export'"><button v-if="stage==='choose'" class="btn primary" :disabled="busy" @click="buildExport">预览包内容 →</button><template v-else><button class="btn" :disabled="busy" @click="stage='choose';exported=null">调整范围</button><button class="btn primary" :disabled="busy||!exported" @click="download">下载成果包</button></template></template>
    <template v-else-if="stage==='review'"><button class="btn" :disabled="busy" @click="stage='choose'">重新选择</button><button class="btn primary" :disabled="busy||!mappingComplete" @click="stage='confirm'">继续确认 →</button></template>
    <template v-else-if="stage==='confirm'"><button class="btn" :disabled="busy" @click="stage='review'">返回核对</button><button class="btn primary" :disabled="busy||!mappingComplete||!name.trim()" @click="save()">保存为新的学习工作稿</button></template>
    <template v-else-if="stage==='recover'"><button v-if="recovery?.progress.failure" class="btn primary" :disabled="busy" @click="editFailed">核对失败状态并返回编辑</button><button v-else class="btn primary" :disabled="busy" @click="save(true)">查询并恢复原操作</button></template>
    <button v-else-if="stage==='saved'" class="btn primary" @click="openSaved">查看新工作稿</button>
  </template>
</WorldDrawer></template>
<style scoped>
h3{font-size:17px;font-weight:550;margin:18px 0 10px;overflow-wrap:anywhere}.note,.step{font-size:12px;color:var(--muted);line-height:1.8}.step{letter-spacing:.08em}.error{color:var(--danger);font-size:13px}fieldset{border:1px solid var(--line);border-radius:10px;padding:16px;margin:20px 0}.check{display:flex;align-items:center;gap:10px;margin:12px 0}.check input{accent-color:var(--accent)}.field{display:grid;gap:8px;margin:14px 0;font-size:13px}.field input,.field select,.field textarea{box-sizing:border-box;width:100%;min-width:0;padding:11px;border:1px solid var(--line);border-radius:8px;background:var(--shell-button);color:var(--ink);font:inherit}.summary{padding:16px 20px;border:1px solid var(--line);border-radius:12px;background:var(--shell-panel)}.summary h3{margin-top:0}.mapping-list{list-style:none;padding:0;margin:18px 0;display:grid;gap:12px}.mapping-list li{border:1px solid var(--line);border-radius:10px;padding:16px;overflow-wrap:anywhere}.mapping-list strong,.mapping-list span{display:block}.mapping-list span{font-size:12px;color:var(--muted)}details{font-size:12px;margin-top:10px}summary{cursor:pointer}pre{white-space:pre-wrap;overflow-wrap:anywhere}dl{display:grid;grid-template-columns:110px minmax(0,1fr);gap:12px;font-size:13px}dt{color:var(--muted)}dd{margin:0;overflow-wrap:anywhere}
</style>
