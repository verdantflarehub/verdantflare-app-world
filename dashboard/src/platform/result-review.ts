import type { LearningDocument } from './learning-results.ts'

export const reviewLabels = { unassessed: '未评估', recommended: '按用途推荐', needs_review: '待审核', excluded: '不推荐' } as const
export type ReviewSelection = keyof typeof reviewLabels
export function reviewSelection(value: unknown): ReviewSelection | '' {
  return typeof value === 'string' && Object.hasOwn(reviewLabels, value) ? value as ReviewSelection : ''
}
export interface ResultReview {
  selection: ReviewSelection
  note: string
  purposes: string[]
  merged: Record<string, unknown>[]
  previews: Record<string, unknown>[]
}
export const unassessedReview: ResultReview = { selection: 'unassessed', note: '未记录用途审核；已有问题与原始分析保留。', purposes: [], merged: [], previews: [] }
// The document has passed readCollectionDocument. Never infer review from media or titles.
export function resultReviews(document?: LearningDocument): Map<string, ResultReview> {
  const reviews = new Map<string, ResultReview>()
  if (document?.schema_version !== 2) return reviews
  const c = document.curation as { packages: { key: string; purpose: string }[]; assessments: Record<string, unknown>[] }
  const purposes = new Map(c.packages.map(p => [p.key, p.purpose]))
  for (const a of c.assessments) reviews.set(String(a.result_key), {
    selection: reviewSelection(a.selection) || 'unassessed', note: String(a.note),
    purposes: (a.package_keys as string[]).map(key => purposes.get(key)!).filter(Boolean),
    merged: a.merged_from_refs as Record<string, unknown>[], previews: a.previews as Record<string, unknown>[],
  })
  return reviews
}
export function fixedMergeLabel(ref: Record<string, unknown>): string {
  const owner = ref.kind === 'world_draft' ? `世界记录 ${ref.draft_id}` : ref.kind === 'asset' ? `资产 ${ref.asset_id}` : `项目 ${ref.project_id}`
  return `${owner} · 固定版本 ${ref.revision_id || ref.asset_version_id} · 文件 ${ref.file_id} · 成果 ${ref.object_key}`
}
export const previewRoles = { original_reference: '原作参考', generated_effect: '生成效果', reconstruction_preview: '重建预演' } as const
