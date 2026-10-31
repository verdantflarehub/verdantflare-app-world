// Display model for the pose-reference / costume-reference domain JSON
// (contracts v2 PoseReference / CostumeReference).
export const REFERENCE_TYPES = ['pose-reference', 'costume-reference'] as const
export const isReferenceAsset = (type: string) => (REFERENCE_TYPES as readonly string[]).includes(type)

export const RIGHTS_LABELS: Record<string, string> = {
  'third-party-internal-only': '第三方 · 仅内部参考 · 不得再分发',
  licensed: '已授权',
  original: '原创',
}
const ANGLES: Record<string, string> = { front: '正面', diag: '斜侧', side: '侧面', high: '俯视', low: '仰视' }
const ANGLE_ORDER = Object.keys(ANGLES)

export interface ReferenceView { key: string; label: string; image: string; thumb?: string }
export interface ReferenceDomain {
  kind: 'pose-reference' | 'costume-reference'
  title: string
  summary: string
  overview: Array<[string, string]>
  default: string
  views: ReferenceView[]
  source: { collection: string; source_ids: string[]; author?: string; rights: string }
}

function obj(v: unknown): Record<string, unknown> {
  if (!v || typeof v !== 'object' || Array.isArray(v)) throw new Error('资产领域文件格式不正确。')
  return v as Record<string, unknown>
}
const str = (v: unknown) => typeof v === 'string' ? v : ''
const zh = (v: unknown) => str(obj(v).zh)

export function readReferenceDomain(value: unknown): ReferenceDomain {
  const d = obj(value), s = obj(d.source)
  const source = { collection: str(s.collection), source_ids: Array.isArray(s.source_ids) ? s.source_ids.map(str) : [], author: str(s.author) || undefined, rights: str(s.rights) }
  if (d.schema_version !== 1 || !source.collection || !source.rights) throw new Error('资产领域文件格式不正确。')
  if (d.kind === 'pose-reference') {
    const views = (Array.isArray(d.views) ? d.views : []).map(v => obj(v)).map(v => ({ key: str(v.angle), label: ANGLES[str(v.angle)] || str(v.angle), image: str(v.image), thumb: str(v.thumb) || undefined }))
      .filter(v => v.key && v.image).sort((a, b) => ANGLE_ORDER.indexOf(a.key) - ANGLE_ORDER.indexOf(b.key))
    if (!views.length) throw new Error('姿势资产没有视图。')
    const system = zh(d.system), category = zh(d.category), people = d.people === 2 ? '双人' : '单人'
    return {
      kind: 'pose-reference', title: zh(d.name), summary: [system, category, people, `${views.length} 个视角`].filter(Boolean).join(' · '),
      overview: [['资产类型', '姿势参考'], ['体系', system], ['动作分类', category], ['人数', people], ['景别', d.framing === 'upper-body' ? '上半身' : '全身'], ['日文名', str(obj(d.name).ja)]].filter(r => r[1]) as Array<[string, string]>,
      default: str(d.default_view), views, source,
    }
  }
  if (d.kind === 'costume-reference') {
    const image = str(d.image)
    if (!image) throw new Error('服装资产没有图片。')
    const tags = obj(d.tags), items = Array.isArray(d.items) ? d.items.map(str).filter(Boolean) : []
    const scope = d.scope === 'item' ? '单品' : '穿搭', category = zh(d.category)
    return {
      kind: 'costume-reference', title: zh(d.name), summary: [category, scope, str(d.costume_key)].filter(Boolean).join(' · '),
      overview: [['资产类型', `服装参考 · ${scope}`], ['分类', category], ['单品', items.join('、')], ['标签', (Array.isArray(tags.zh) ? tags.zh.map(str) : []).join('、')], ['英文关键词', str(tags.en)], ['穿在人身上', d.worn_on_body ? '是' : '否']].filter(r => r[1]) as Array<[string, string]>,
      default: 'image', views: [{ key: 'image', label: scope, image, thumb: str(d.thumb) || undefined }], source,
    }
  }
  throw new Error('不是姿势或服装参考资产。')
}

export function referenceViews(d: ReferenceDomain) { return d.views }
