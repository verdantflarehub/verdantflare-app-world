import { object } from './draft-model.ts'
import { promptVariants, readLearningResults, type LearningResult, type PromptVariant } from './learning-results.ts'
import type { AssetOpen } from './world-model.ts'
import { canonical } from './world-publication.ts'
export interface PromptEdit {
 name:string;intent:string;requested_label:string;model_id:string;model_version:string;provider:string;route:string
 prompt_text:string;messages:{role:string;content:string}[];negative_prompt:string;parameters:string
}
export interface PromptCopyPlan {name:string;subjects:string[];document:ReturnType<typeof readLearningResults>;source_refs:Record<string,unknown>[];asset_refs:Record<string,unknown>[]}
const clone=<T>(v:T):T=>JSON.parse(JSON.stringify(v)) as T
export function promptEdit(result:LearningResult,variant:PromptVariant):PromptEdit {
 return {name:result.title,intent:String(result.payload.creative_intent),requested_label:variant.requested_label,model_id:variant.model_id||'',model_version:variant.model_version||'',provider:variant.provider||'',route:variant.route||'',prompt_text:variant.prompt_text||'',messages:clone(variant.messages||[]),negative_prompt:variant.negative_prompt||'',parameters:JSON.stringify(variant.parameters||{},null,2)}
}
export function preparePromptCopy(asset:AssetOpen,result:LearningResult,variantKey:string,input:PromptEdit):PromptCopyPlan {
 if(result.type_key!=='prompt'||result.type_version!==1)throw Error('此提示词版本尚不支持编辑。')
 const original=promptVariants(result).find(v=>v.key===variantKey)
 const domain=asset.manifest.domain_documents?.find(d=>d.document_type==='reference-learning')
 if(!original||!domain)throw Error('未找到所选固定提示词版本。')
 for(const field of ['name','intent','requested_label'] as const)if(!input[field].trim()||input[field].length>(field==='intent'?4096:256))throw Error('请填写名称、创作目标与目标模型说明，并保持长度在字段范围内。')
 if(!input.prompt_text.trim()&&!input.messages.length)throw Error('至少保留提示词正文或一条对话消息。')
 const parameters=object(JSON.parse(input.parameters));if(Object.keys(parameters).length>128)throw Error('参数最多 128 项。')
 const refs=new Map<string,Record<string,unknown>>()
 function retain(r:Record<string,unknown>){refs.set(canonical(r),r);return r}
 function assetRef(file_id:string,object_key?:string):Record<string,unknown>{
  if(!asset.manifest.files.some(f=>f.file_id===file_id))throw Error('参考文件没有包含在所选资产中。')
  return retain({kind:'asset',asset_id:asset.asset_id,asset_version_id:asset.asset_version_id,file_id,purpose:'prompt_derivation',...(object_key?{object_key}:{})})
 }
 const origin=assetRef(domain.file_id,result.key)
 function remap(raw:unknown):unknown {
  const r=object(raw)
  if(r.kind==='source'||r.kind==='result')return assetRef(domain!.file_id,String(r.key))
  if(r.kind==='file')return assetRef(String(r.file_id))
  if(['asset','world_draft','project'].includes(String(r.kind)))return retain(clone(r))
  throw Error('参考绑定缺少可解析的固定来源。')
 }
 const variables=clone(result.payload.variables) as Record<string,unknown>[]
 for(const v of variables)if(v.value_type==='ref'&&v.default!==undefined)v.default=remap(v.default)
 const variant=clone(original)
 variant.revision++
 variant.requested_label=input.requested_label.trim()
 for(const k of ['model_id','model_version','provider','route','negative_prompt'] as const){if(input[k].trim())variant[k]=input[k].trim();else delete variant[k]}
 if(input.prompt_text.trim())variant.prompt_text=input.prompt_text;else delete variant.prompt_text
 if(input.messages.length)variant.messages=clone(input.messages);else delete variant.messages
 variant.parameters=parameters
 variant.reference_bindings=original.reference_bindings.map(raw=>{const b=object(raw);return {...clone(b),input_ref:remap(b.input_ref)}})
 variant.compatibility=variant.model_id?'draft':'unresolved'
 delete variant.capability_ref;delete variant.capability_sha256
 variant.execution_evidence=[];variant.quality_review={state:'unreviewed',note:'提示词已修改，尚未执行或审核新版本。'}
 const use_conditions=result.use_conditions.map(c=>({...clone(c),availability:c.availability==='ready'?'unknown':c.availability}))
 if(use_conditions.length<16)use_conditions.push({mode:'direct',availability:'ready',purpose:'复制新提示词正文',required_inputs:[],missing_items:[]})
 const newResult={key:'edited_prompt',title:input.name.trim(),domain:'knowledge',type_key:'prompt',type_version:1,basis:'created',basis_note:'基于固定资产的所选提示词变体修改；原分析与验证记录保留在来源中。',source_refs:[origin],evidence:[],use_conditions,payload:{creative_intent:input.intent.trim(),variables,variants:[variant]}}
 const document=readLearningResults(JSON.stringify({schema_version:1,document_kind:'reference-learning',origin:'manual',sources:[],coverage:[],results:[newResult],relations:[{key:'derived_prompt',from_ref:{kind:'result',key:'edited_prompt'},to_ref:origin,relation_type:'derived_from',basis:'created',description:'用户修改提示词并另存为独立成果。',attributes:{change_summary:'修改正文、目标说明或参数；旧验证不继承。'}}],uses:[]}))
 if(new TextEncoder().encode(JSON.stringify(document)).length>1<<20)throw Error('提示词配置超过当前文本保存上限。')
 const assets=new Map<string,Record<string,unknown>>()
 for(const r of refs.values())if(r.kind==='asset'){const a={asset_id:r.asset_id,asset_version_id:r.asset_version_id,purpose:'prompt_reference'};assets.set(canonical(a),a)}
 return {name:input.name.trim(),subjects:clone(asset.manifest.subjects),document,source_refs:[...refs.values()],asset_refs:[...assets.values()]}
}

// Editing a confirmed failed save keeps its original fixed lineage and bindings.
export function revisePromptCopy(plan:PromptCopyPlan,input:PromptEdit):PromptCopyPlan {
 const next=clone(plan),r=next.document.results[0],v=promptVariants(r)[0]
 for(const key of ['name','intent','requested_label'] as const)if(!input[key].trim()||input[key].length>(key==='intent'?4096:256))throw Error('名称、创作目标或模型说明无效。')
 next.name=input.name.trim();r.title=next.name;r.payload.creative_intent=input.intent.trim()
 v.requested_label=input.requested_label.trim()
 for(const k of ['model_id','model_version','provider','route','negative_prompt'] as const){if(input[k].trim())v[k]=input[k].trim();else delete v[k]}
 if(input.prompt_text.trim())v.prompt_text=input.prompt_text;else delete v.prompt_text
 if(input.messages.length)v.messages=clone(input.messages);else delete v.messages
 v.parameters=object(JSON.parse(input.parameters));if(Object.keys(v.parameters).length>128)throw Error('参数最多 128 项。')
 v.compatibility=v.model_id?'draft':'unresolved';delete v.capability_ref;delete v.capability_sha256;v.execution_evidence=[];v.quality_review={state:'unreviewed',note:'提示词已修改，尚未执行或审核新版本。'}
 const text=JSON.stringify(next.document);if(new TextEncoder().encode(text).length>1<<20)throw Error('提示词配置超过当前文本保存上限。');readLearningResults(text)
 return next
}
