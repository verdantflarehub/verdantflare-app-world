import { resultRouteBase } from './route-links.ts'
export const worldDomains = [
  {key:'character',label:'人物',types:['character','character-image'],glyph:'人 · 物'},
  {key:'pose',label:'姿势',types:['pose-reference'],glyph:'姿 · 势'},
  {key:'costume',label:'服装',types:['costume-reference'],glyph:'服 · 装'},
  {key:'scene',label:'场景',types:['scene'],glyph:'场 · 景'},
  {key:'sound',label:'声音',types:['sound','voice-model'],glyph:'声 · 音'},
  {key:'music',label:'音乐',types:['music','music-work'],glyph:'音 · 乐'},
  {key:'shot',label:'镜头',types:['shot','video-project'],glyph:'镜 · 头'},
  {key:'script',label:'剧本',types:['script'],glyph:'剧 · 本'},
  {key:'knowledge',label:'知识',types:['knowledge','prompt'],glyph:'知 · 识'},
] as const
export const catalogTypes = [
  {value:'character',label:'人物设定'}, {value:'character-image',label:'人物形象'},
  {value:'pose-reference',label:'姿势参考'}, {value:'costume-reference',label:'服装参考'},
  {value:'scene',label:'场景与道具'}, {value:'sound',label:'声音方案'}, {value:'voice-model',label:'声音模型'},
  {value:'music',label:'音乐结构与参考'}, {value:'music-work',label:'音乐作品'},
  {value:'shot',label:'镜头与运镜'}, {value:'video-project',label:'视频工程'},
  {value:'script',label:'剧本与场次'}, {value:'knowledge',label:'知识与方法'}, {value:'prompt',label:'提示词'},
]
export function catalogDomain(path:string) { return worldDomains.find(d=>resultRouteBase(path)==='/'+d.key) }
export function assetDomain(type:string) { return worldDomains.find(d=>(d.types as readonly string[]).includes(type)) }
export function assetTypeLabel(type:string) { return catalogTypes.find(t=>t.value===type)?.label||type }
export function catalogQuery(path:string,query:Record<string,unknown>) {
  const domain=catalogDomain(path)?.key
  const asset_type=typeof query.asset_type==='string'?query.asset_type:''
  const q=typeof query.q==='string'?query.q.trim():''
  const relation=typeof query.relation==='string'?query.relation:''
  return {...(domain?{domain}:{}),...(asset_type?{asset_type}:{}),...(q?{q}:{}),...(relation?{relation}:{})}
}
