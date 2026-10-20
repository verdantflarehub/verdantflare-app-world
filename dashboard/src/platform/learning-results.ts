import type { AssetOpen } from './world-model.ts'
import { object, readCollectionDocument, type CollectionDocument } from './draft-model.ts'

// Presentation only. Central Schema and service authorization remain authoritative.
export const resultTypes: Record<string,string> = {
  music_structure:'音乐结构',script_unit:'剧作',shot:'镜头',character:'人物',scene:'场景',prop:'道具',sound_cue:'声音',
  prompt:'提示词',method:'方法',style:'风格',creative_system:'创作体系',skill_candidate:'技能候选',file_asset:'文件资产',
  blender_scene:'Blender 片场',camera_animation:'连续运镜',comfyui_workflow:'ComfyUI 工程',
}
export const basisLabels: Record<string,string> = {observed:'画面或原文观察',measured:'测量',inferred:'分析推断',external:'外部拆解',created:'原创',reconstruction:'重建方案'}
export const compatibilityLabels: Record<string,string> = {unresolved:'目标模型待解析',draft:'尚未校验适配',schema_checked:'参数结构已校验',incompatible:'当前配置不兼容'}
export interface UseCondition {mode:string;availability:string;purpose:string;required_inputs:string[];missing_items:string[]}
export interface PromptVariant extends Record<string,unknown> {
  key:string;revision:number;requested_label:string;task_type:string;input_mode:string;model_id?:string;model_version?:string;provider?:string;route?:string
  prompt_text?:string;messages?:{role:string;content:string}[];negative_prompt?:string;parameters?:Record<string,unknown>
  reference_bindings:unknown[];compatibility:string;execution_evidence:unknown[];quality_review:{state:string;note:string}
}
export interface LearningResult extends Record<string,unknown> {
  key:string;title:string;type_key:string;type_version:number;domain:string;basis:string;basis_note:string
  use_conditions:UseCondition[];source_refs:unknown[];evidence:unknown[];payload:Record<string,unknown>
}
export interface LearningDocument extends CollectionDocument {results:LearningResult[]}
function fail():never {throw Error('成果正文不完整，不能作为可用配置展示。')}
function text(v:unknown,max=65536):v is string{return typeof v==='string'&&v.trim().length>0&&v.length<=max}
function list(v:unknown,max=10000):unknown[]{if(!Array.isArray(v)||v.length>max)fail();return v as unknown[]}
function texts(v:unknown):string[]{const a=list(v,128);if(!a.every(x=>text(x)))fail();return a as string[]}
export function readLearningResults(raw:string):LearningDocument {
  const d=readCollectionDocument(raw,4 << 20),keys=new Set<string>()
  for(const value of list(d.results)){
    const r=object(value)
    for(const k of ['key','title','type_key','domain','basis','basis_note'])if(!text(r[k]))fail()
    if(keys.has(String(r.key))||!Number.isSafeInteger(r.type_version)||Number(r.type_version)<1)fail()
    keys.add(String(r.key));object(r.payload);list(r.source_refs,256);list(r.evidence,256)
    for(const value of list(r.use_conditions,16)){const c=object(value);if(!text(c.purpose)||!text(c.mode)||!text(c.availability))fail();texts(c.required_inputs);texts(c.missing_items)}
    if(r.type_key==='prompt'&&r.type_version===1)promptVariants(r as unknown as LearningResult)
  }
  return d as LearningDocument
}
export function promptVariants(result:LearningResult):PromptVariant[]{
  const p=result.payload,seen=new Set<string>()
  if(!text(p.creative_intent))fail()
  list(p.variables,128)
  const variants=list(p.variants,128);if(!variants.length)fail()
  for(const value of variants){
    const v=object(value)
    for(const k of ['key','requested_label','task_type','input_mode','compatibility'])if(!text(v[k]))fail()
    if(seen.has(String(v.key))||!Number.isSafeInteger(v.revision)||Number(v.revision)<1)fail();seen.add(String(v.key))
    if(v.prompt_text!==undefined&&!text(v.prompt_text))fail()
    if(v.messages!==undefined){const messages=list(v.messages,128);if(!messages.length)fail();for(const item of messages){const m=object(item);if(!['system','developer','user','assistant'].includes(String(m.role))||!text(m.content))fail()}}
    if(v.prompt_text===undefined&&v.messages===undefined)fail()
    for(const k of ['model_id','model_version','provider','route','negative_prompt'])if(v[k]!==undefined&&!text(v[k]))fail()
    if(!Object.hasOwn(compatibilityLabels,String(v.compatibility)))fail()
    if(v.parameters!==undefined)object(v.parameters)
    list(v.reference_bindings,128);list(v.execution_evidence,128)
    const q=object(v.quality_review);if(!['unreviewed','accepted','rejected'].includes(String(q.state))||!text(q.note))fail()
  }
  return variants as PromptVariant[]
}
export function promptBody(v:PromptVariant):string {
  const parts:string[]=[]
  if(v.prompt_text!==undefined)parts.push(v.prompt_text)
  if(v.messages)parts.push(v.messages.map(m=>`[${m.role}]\n${m.content}`).join('\n\n'))
  return parts.join('\n\n')
}
export function promptConfiguration(result:LearningResult,v:PromptVariant,asset:AssetOpen):string {
  return JSON.stringify({source:{asset_id:asset.asset_id,asset_version_id:asset.asset_version_id,result_key:result.key},
    creative_intent:result.payload.creative_intent,variables:result.payload.variables,variant:v},null,2)
}
export function filterResults(results:LearningResult[],type:string,query:string):LearningResult[]{
  const q=query.trim().toLocaleLowerCase()
  return results.filter(r=>(!type||r.type_key===type)&&(!q||[r.title,r.key,resultTypes[r.type_key]||r.type_key,
    ...(r.type_key==='prompt'&&r.type_version===1?promptVariants(r).flatMap(v=>[v.requested_label,v.model_id||'',v.model_version||'']):[])]
    .some(s=>s.toLocaleLowerCase().includes(q))))
}
export async function loadAssetResults(call:(name:string,args:Record<string,unknown>)=>Promise<unknown>,asset:AssetOpen):Promise<LearningDocument|null>{
  const domain=asset.manifest.domain_documents?.find(d=>d.document_type==='reference-learning')
  if(!domain)return null
  const file=asset.manifest.files.find(f=>f.file_id===domain.file_id);if(!file)fail()
  const ref=file!.content_ref
  const response=object(await call('artifact.read',{mode:'text',content_ref:ref,access:{asset_id:asset.asset_id,asset_version_id:asset.asset_version_id}}))
  const version=object(response.version)
  if(response.mode!=='text'||typeof response.text!=='string'||version.store_id!==ref.store_id||version.artifact_id!==ref.artifact_id||version.version_id!==ref.version_id)throw Error('成果正文与所选固定资产版本不一致。')
  return readLearningResults(response.text)
}
