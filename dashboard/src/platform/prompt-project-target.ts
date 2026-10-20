import type { AssetOpen } from './world-model.ts'
import { isID } from './world-model.ts'
import { promptVariants, type LearningResult } from './learning-results.ts'
export function promptProjectTarget(asset:AssetOpen,result:LearningResult,variantKey:string){
 const domain=asset.manifest.domain_documents?.find(d=>d.document_type==='reference-learning')
 const file=asset.manifest.files.find(f=>f.file_id===domain?.file_id)
 const variant=promptVariants(result).find(v=>v.key===variantKey)
 if(result.type_key!=='prompt'||result.type_version!==1||!file||!variant||![asset.asset_id,asset.asset_version_id,file.content_ref.store_id,file.content_ref.artifact_id,file.content_ref.version_id].every(isID)||!Number.isSafeInteger(variant.revision)||variant.revision<1||![result.key,variant.key].every(k=>/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$/.test(k)))throw Error('提示词缺少完整的固定版本定位。')
 return {appId:'project',path:'/create/from-prompt?'+new URLSearchParams({asset:asset.asset_id,version:asset.asset_version_id,store:file.content_ref.store_id,artifact:file.content_ref.artifact_id,content_version:file.content_ref.version_id,result:result.key,variant:variant.key,variant_revision:String(variant.revision)})}
}
