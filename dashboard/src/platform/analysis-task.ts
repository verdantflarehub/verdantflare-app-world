import { object, readDraft, type DraftRevision } from './draft-model.ts'
import { isID } from './world-model.ts'
import { createDraftAPI, DraftError } from './draft-client.ts'
import { operationID } from './collection-save.ts'
import { publicationPrincipal } from './publication-client.ts'
import { canonical, type PublicationStorage } from './world-publication.ts'
import type { AppSDK } from './types.ts'

export interface AnalysisSource { draft_id:string; revision_id:string; file_id:string }
export interface PromptTarget { key:string; requested_label:string; task_type:string; input_mode:string; model_id?:string; adaptation_guidance:string }
export interface AnalysisSubmit extends Record<string,unknown> { mode:'submit'; task_id:string; source:AnalysisSource; model_id:string; budget_limit:string; budget_per_call:string; prompt_targets:PromptTarget[] }
export interface AnalysisAvailability { enabled:boolean; models:string[]; input_mime_types:string[]; max_media_bytes:number }
export interface AnalysisTask {
 task_id:string; name:string; request_state:'pending'|'preparing'|'queued'|'failed'; error_code?:string
 source:AnalysisSource; model_id:string; prompt_targets:PromptTarget[]; created_at:string
 budget:{limit:string;per_call:string;reserved:string}
 progress?:Record<string,unknown> & { stage:string }
 learning?:{task_id:string;state:string;revision?:DraftRevision;error_code?:string}
}
export const promptTargets:PromptTarget[]=[
 {key:'h3',requested_label:'H3',task_type:'video',input_mode:'text-to-video',adaptation_guidance:'整理主体、场景、动作和连续镜头时序。缺少模型版本或参数说明时保留待适配，不虚构参数与运行效果。'},
 {key:'chatgpt',requested_label:'ChatGPT',task_type:'text',input_mode:'messages',adaptation_guidance:'用明确的任务、原作证据、输出结构与改写变量组织消息，保留事实和推断差异。未指定实际模型版本。'},
 {key:'sd25',requested_label:'SD2.5',task_type:'video',input_mode:'reference-to-video',adaptation_guidance:'保留用户模型标签 SD2.5，整理参考素材、主体、动作和时序；实际模型标识与输入兼容性待解析，不猜测参数。'},
]
export const stageLabels:Record<string,string>={
 planned:'等待分析',observing:'分析画面与声音',observations_ready:'画面与声音分析已保存',associating:'整理人物、场景与镜头关系',associations_ready:'关联整理已保存',preparing_prompts:'准备模型提示词',producing_prompts:'整理模型提示词',prompts_ready:'模型提示词已保存',preparing_results:'整理分类成果',producing_results:'整理分类成果',results_ready:'分类成果已保存',results_need_evidence:'成果仍有证据待补齐',saving_learning:'保存学习成果',learning_saved:'学习成果已保存',learning_saved_need_evidence:'已保存，部分内容缺少证据',learning_save_failed:'成果保存需要处理',needs_attention:'需要处理',
}
function fail():never{throw Error('拆解记录不完整或与原请求不一致。')}
const amount=/^(0|[1-9]\d{0,8})(\.\d{1,8})?$/
const model=/^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,199}$/
function units(v:unknown):bigint{if(typeof v!=='string'||!amount.test(v))fail();const [whole,part='']=v.split('.');return BigInt(whole)*100000000n+BigInt(part.padEnd(8,'0'))}
function source(v:unknown):AnalysisSource{const s=object(v);if(Object.keys(s).length!==3||!isID(s.draft_id)||!isID(s.revision_id)||!isID(s.file_id))fail();return s as unknown as AnalysisSource}
export function readSubmit(v:unknown):AnalysisSubmit{
 const r=object(v);if(Object.keys(r).some(k=>!['mode','task_id','source','model_id','budget_limit','budget_per_call','prompt_targets'].includes(k))||r.mode!=='submit'||!isID(r.task_id)||typeof r.model_id!=='string'||!model.test(r.model_id))fail()
 source(r.source);if(units(r.budget_per_call)<=0n||units(r.budget_limit)<units(r.budget_per_call))throw Error('总预算须不小于单次上限，金额须大于零且最多八位小数。')
 if(!Array.isArray(r.prompt_targets)||!r.prompt_targets.length||r.prompt_targets.length>16)fail()
 const seen=new Set<string>()
 for(const raw of r.prompt_targets){const t=object(raw);if(Object.keys(t).some(k=>!['key','requested_label','task_type','input_mode','model_id','adaptation_guidance'].includes(k)))fail();for(const k of ['key','requested_label','task_type','input_mode','adaptation_guidance'])if(typeof t[k]!=='string'||!String(t[k]).trim()||String(t[k]).length>4096)fail();if(seen.has(String(t.key)))fail();seen.add(String(t.key));if(t.model_id!==undefined&&(typeof t.model_id!=='string'||!model.test(t.model_id)))fail()}
 return r as unknown as AnalysisSubmit
}
export function readAvailability(v:unknown):AnalysisAvailability{
 const c=object(v);if(typeof c.enabled!=='boolean'||!Array.isArray(c.models)||c.models.length>128||c.models.some(m=>typeof m!=='string'||!model.test(m))||new Set(c.models).size!==c.models.length||!Array.isArray(c.input_mime_types)||c.input_mime_types.some(m=>typeof m!=='string')||!Number.isSafeInteger(c.max_media_bytes)||Number(c.max_media_bytes)<1||c.enabled&&!c.models.length)fail()
 return c as unknown as AnalysisAvailability
}
export function readTask(v:unknown,id?:string,request?:AnalysisSubmit):AnalysisTask{
 const t=object(v),b=object(t.budget)
 if(!isID(t.task_id)||id&&t.task_id!==id||typeof t.name!=='string'||!t.name||!['pending','preparing','queued','failed'].includes(String(t.request_state))||typeof t.created_at!=='string'||!Number.isFinite(Date.parse(t.created_at)))fail()
 readSubmit({mode:'submit',task_id:t.task_id,source:t.source,model_id:t.model_id,budget_limit:b.limit,budget_per_call:b.per_call,prompt_targets:t.prompt_targets});if(units(b.reserved)>units(b.limit))fail()
 if(request&&canonical({source:t.source,model_id:t.model_id,budget_limit:b.limit,budget_per_call:b.per_call,prompt_targets:t.prompt_targets})!==canonical({source:request.source,model_id:request.model_id,budget_limit:request.budget_limit,budget_per_call:request.budget_per_call,prompt_targets:request.prompt_targets}))fail()
 if(t.progress){const p=object(t.progress);if(p.task_id!==t.task_id||!Object.hasOwn(stageLabels,String(p.stage)))fail();for(const k of ['planned','running','acceptance_unknown','observation_ready','failed','prompt_planned','prompt_running','prompt_acceptance_unknown','prompt_ready','prompt_failed','prompt_total','result_planned','result_running','result_acceptance_unknown','result_ready','result_failed','result_total','result_insufficient_evidence'])if(!Number.isSafeInteger(p[k])||Number(p[k])<0)fail()}
 if(t.learning){const l=object(t.learning);if(l.task_id!==t.task_id||!['pending','saved','conflict','failed'].includes(String(l.state)))fail();if(l.state==='saved'){const d=readDraft(l.revision,undefined,undefined,'learning');if(!d.manifest.run_refs.some(r=>r.service_id==='world'&&r.run_id==='analysis-v1:'+t.task_id))fail()}else if(l.revision)fail()}
 return t as unknown as AnalysisTask
}
export function taskLabel(t:AnalysisTask):string{
 if(t.progress?.last_error_code==='budget_exhausted')return '预算已用尽，未发送的调用已停止'
 if(t.progress)return stageLabels[t.progress.stage]
 return {pending:'等待准备',preparing:'准备原片与音轨',queued:'等待分析',failed:'来源暂不可分析'}[t.request_state]
}
export function shouldPoll(t:AnalysisTask):boolean{return !['failed','prompt_failed','result_failed'].some(k=>Number(t.progress?.[k])>0)&&t.progress?.association_state!=='failed'&&t.request_state!=='failed'&&!['saved','failed','conflict'].includes(t.learning?.state||'')&&(t.progress?.last_error_code!=='budget_exhausted'||['running','acceptance_unknown','prompt_running','prompt_acceptance_unknown','result_running','result_acceptance_unknown'].some(k=>Number(t.progress?.[k])>0))}

export function createAnalysisSubmission(sdk:AppSDK,storage:PublicationStorage){
 const api=createDraftAPI(sdk),key='world.analysis-submit.v1:'+JSON.stringify(publicationPrincipal(sdk.context.get()));let working=false
 function pending():AnalysisSubmit|null{api.live();let raw:string|null;try{raw=storage.getItem(key)}catch{throw new DraftError('STORAGE_UNAVAILABLE')}return raw?readSubmit(JSON.parse(raw)):null}
 async function resume():Promise<AnalysisTask>{
  if(working)throw Error('正在确认原拆解任务。');working=true
  try{const r=pending();if(!r)throw Error('没有待恢复的提交。');const t=readTask(await api.analyzeRequest(r),r.task_id,r);try{storage.removeItem(key)}catch{throw new DraftError('STORAGE_UNAVAILABLE')}return t}finally{working=false}
 }
 function start(input:Omit<AnalysisSubmit,'mode'|'task_id'>):Promise<AnalysisTask>{
  if(working||pending())return Promise.reject(Error('请先恢复上次提交，避免重复拆解。'))
  const r=readSubmit({...input,mode:'submit',task_id:operationID()})
  try{storage.setItem(key,JSON.stringify(r))}catch{return Promise.reject(new DraftError('STORAGE_UNAVAILABLE'))}
  return resume()
 }
 return {pending,start,resume,dispose:api.dispose}
}
