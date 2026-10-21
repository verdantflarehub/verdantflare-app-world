import { resultTypes, type LearningDocument, type LearningResult } from './learning-results.ts'
/** Human ordinal is scoped to this immutable document; MCP always receives the full locator. */
export function resultCode(result: LearningResult, document: LearningDocument): string {
  const items=document.results.filter(r=>r.type_key===result.type_key),index=items.findIndex(r=>r.key===result.key)
  return `${resultTypes[result.type_key]||'其他'}-${String(index+1).padStart(3,'0')}`
}
export function agentReference(result:LearningResult, document:LearningDocument, sourceLabel:string):string {
 return [
  `请在数字世界找到「${result.title}」（${resultCode(result,document)}，编号仅在这份固定文档内有效）。`,
  `固定来源：${sourceLabel}。`,
  `精确成果键：${result.key}。类别：${resultTypes[result.type_key]||'其他成果'}。`,
  '通过 MCP 读取上述固定资产版本（world.get）或固定学习修订（world.draft，mode=get），在清单中的 reference-learning 正文里按精确成果键定位；不要只凭名称、显示编号或最新版本替代。',
  '先用可读内容、对应图片或试听说明它能怎样参考。原片参考不等于已生成的人物素材、独立声线或工程文件。',
  '如需改写或制作，确定目标项目后选择相应技能，经 MCP 在项目中执行；保留固定来源，不修改此参考或直接生成世界资产。',
 ].join('\n')
}
export function businessOrder(results:LearningResult[]):LearningResult[]{
 const order=['character','scene','prop','shot','sound_cue','music_structure','script_unit','style','method','prompt']
 const rank=(r:LearningResult)=>{const i=order.indexOf(r.type_key);return i<0?order.length:i}
 return [...results].sort((a,b)=>rank(a)-rank(b))
}
