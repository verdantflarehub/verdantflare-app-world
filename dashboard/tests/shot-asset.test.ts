import test from 'node:test'
import assert from 'node:assert/strict'
import { approvedShotPrompt, shotAssetReference } from '../src/platform/shot-asset.ts'
import type { LearningResult } from '../src/platform/learning-results.ts'
import type { AssetOpen } from '../src/platform/world-model.ts'

const variant = (key: string, state: string, prompt: string, revision = 1) => ({ key, revision,
  requested_label: key, task_type: 'video', input_mode: 'text', compatibility: 'draft',
  model_id: 'threejs-procedural-previs', route: 'local-renderer',
  prompt_text: JSON.stringify({ schema_version: 1, prompt, subject: '方头机器人', duration_seconds: 4, provenance: 'internal note' }),
  reference_bindings: [], execution_evidence: [], quality_review: { state, note: 'review' } })
test('public shot prompt comes only from accepted previs and excludes candidates and internal fields', () => {
  const r = { type_key: 'prompt', type_version: 1, payload: { creative_intent: 'camera', variables: [], variants: [
    variant('editable-previs', 'accepted', '固定机位，水平旋转。'), variant('draft', 'unreviewed', 'UNREVIEWED', 9),
    { ...variant('model', 'accepted', 'MODEL', 10), model_id: 'model' }, variant('rejected', 'rejected', 'REJECTED', 11),
  ] } } as unknown as LearningResult
  const text = approvedShotPrompt([r])
  assert.equal(text, '固定机位，水平旋转。\n\n方头机器人\n\n时长 4 秒。')
  assert.doesNotMatch(text, /internal|UNREVIEWED|MODEL|REJECTED/)
  r.payload.variants = [variant('draft', 'unreviewed', 'not ready')]
  assert.equal(approvedShotPrompt([r]), '')
  r.payload.variants = [{ ...variant('editable-previs', 'accepted', 'broken'), prompt_text: '{invalid' }]
  assert.equal(approvedShotPrompt([r]), '')
})
test('copy for agent carries exact fixed asset and usable instructions', () => {
  const asset = { asset_id: 'asset-fixed', asset_version_id: 'version-fixed' } as AssetOpen
  const text = shotAssetReference(asset, '横摇', '向右旋转。', 'description', true)
  assert.match(text, /向右旋转/)
  assert.match(text, /固定资产：asset-fixed\n固定版本：version-fixed/)
  assert.match(text, /Blender/)
  assert.doesNotMatch(text, /description|UNREVIEWED|来源与分析依据/)
})
