import { promptVariants, type LearningResult } from './learning-results.ts'
import { record } from './result-reference.ts'
import type { AssetOpen } from './world-model.ts'

export function shotAssetReading(shot: LearningResult) {
  return { sections: [['构图', 'composition'], ['运镜', 'camera_motion'], ['主体动作', 'subject_motion'], ['衔接', 'transition']]
    .flatMap(([title, key]) => {
      const value = shot.payload[key]
      return typeof value === 'string' && value.trim() ? [{ title, lines: [value.trim()] }] : []
    }) }
}

// The delivered asset exposes one readable prompt from an accepted previs
// configuration. Model candidates and their review history stay in the workbench.
export function approvedShotPrompt(results: LearningResult[]): string {
  for (const result of results.filter(r => r.type_key === 'prompt' && r.type_version === 1)) {
    const variants = promptVariants(result).filter(v => v.quality_review.state === 'accepted'
      && v.key === 'editable-previs' && v.model_id === 'threejs-procedural-previs' && v.route === 'local-renderer')
      .sort((a, b) => b.revision - a.revision)
    for (const variant of variants) {
      try {
        const config = record(JSON.parse(variant.prompt_text || ''))
        if (config.schema_version !== 1 || typeof config.prompt !== 'string' || !config.prompt.trim()) continue
        return [config.prompt.trim(), typeof config.subject === 'string' ? config.subject.trim() : '',
          typeof config.duration_seconds === 'number' && config.duration_seconds > 0 && config.duration_seconds <= 3600
            ? `时长 ${config.duration_seconds} 秒。` : ''].filter(Boolean).join('\n\n')
      } catch { /* An invalid configuration cannot become a public prompt. */ }
    }
  }
  return ''
}

export function shotAssetReference(asset: AssetOpen, title: string, prompt: string, description: string, agent = false): string {
  return [agent ? `请在项目中使用镜头资产「${title}」。` : title, prompt || description,
    `固定资产：${asset.asset_id}\n固定版本：${asset.asset_version_id}`,
    agent ? '读取此固定资产版本，使用其预演和 Blender 工程编排镜头；根据项目替换主体与环境，保持相机和主体的相对运动。' : ''].filter(Boolean).join('\n\n')
}
