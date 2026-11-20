import type { AssetOpen } from './world-model.ts'
import type { LearningResult } from './learning-results.ts'
import { record } from './result-reference.ts'

// Production previews are explicit current-asset files. Never borrow an
// original from analysis evidence, a similarly named file, or another version.
export function producedMedia(result: LearningResult, asset: AssetOpen): { clips: unknown[]; poster?: unknown } | null {
  if (result.type_version !== 1 || !['shot','camera_animation'].includes(result.type_key)) return null
  const raw = result.type_key === 'camera_animation' ? [result.payload.previs_ref] : result.payload.clip_refs
  if (!Array.isArray(raw)) return null
  const clips = raw.filter(value => {
    const r = record(value)
    return r.kind === 'file' && asset.manifest.files.some(f => f.file_id === r.file_id && ['preview','previs','output'].includes(f.role))
  })
  if (!clips.length) return null
  const images = Array.isArray(result.payload.storyboard_refs) ? result.payload.storyboard_refs : []
  const poster = images.find(value => {
    const r = record(value)
    return r.kind === 'file' && asset.manifest.files.some(f => f.file_id === r.file_id && f.role === 'thumbnail')
  })
  return { clips, ...(poster ? { poster } : {}) }
}
