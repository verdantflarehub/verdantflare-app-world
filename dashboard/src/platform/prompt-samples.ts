import { object } from './draft-model.ts'
import { isID, readAsset, type AssetOpen } from './world-model.ts'
import type { LearningDocument, PromptVariant } from './learning-results.ts'

type Call = (name: string, args: Record<string, unknown>) => Promise<unknown>
export interface PromptSample { key: string; reference: unknown; model: string; route: string; note: string }
export function promptSamples(variant: PromptVariant): PromptSample[] {
  return variant.execution_evidence.flatMap((raw, i) => {
    const e = object(raw)
    if (e.state !== 'succeeded') return []
    if (!Array.isArray(e.output_refs) || !e.output_refs.length) throw Error('生成记录缺少输出引用。')
    return e.output_refs.map((reference, j) => ({ key: `${i}-${j}`, reference,
      model: typeof e.model_id === 'string' ? e.model_id : '未记录模型',
      route: typeof e.route === 'string' ? e.route : '未记录渠道',
      note: typeof e.note === 'string' ? e.note : '' }))
  })
}

// Resolve only explicit file relations. Never guess by title, extension or latest version.
export function sampleFileReference(raw: unknown, document: LearningDocument, seen = new Set<string>()): Record<string, unknown> {
  const r = object(raw)
  if (r.kind === 'file' || r.kind === 'asset') return r
  if (!['source', 'result'].includes(String(r.kind))) throw Error('该样例引用暂不支持预览，固定引用已保留。')
  const key = `${r.kind}:${r.key}`
  if (seen.has(key) || seen.size >= 16) throw Error('样例引用循环，无法预览。')
  seen.add(key)
  if (r.kind === 'source') {
    const source = document.sources.map(object).find(s => s.key === r.key)
    if (!source || source.acquisition_state !== 'ready') throw Error('样例来源尚未收录实际文件。')
    return sampleFileReference(source.content, document, seen)
  }
  const result = document.results.find(s => s.key === r.key)
  if (result?.type_key !== 'file_asset' || result.type_version !== 1 || !Array.isArray(result.payload.files) || result.payload.files.length !== 1)
    throw Error('该成果不是单个样例文件，请查看其固定文件清单。')
  return sampleFileReference(result.payload.files[0], document, seen)
}

export function sampleContentPath(value: unknown): string {
  const uuid = '[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}'
  if (typeof value !== 'string' || !new RegExp(`^/studio/dashboards/${uuid}/content/${uuid}$`).test(value))
    throw Error('样例没有有效的宿主受控读取地址。')
  return value
}
export interface SampleDownload { path: string; mime: string; size: number; sha256: string }
export async function sampleDownload(call: Call, asset: AssetOpen, reference: Record<string, unknown>): Promise<SampleDownload> {
  let owner = asset
  if (reference.kind === 'asset') {
    if (reference.object_key !== undefined) throw Error('该样例指向结构化对象，尚不能作为媒体文件预览。')
    const a = reference.asset_id, v = reference.asset_version_id
    if (!isID(a) || !isID(v)) throw Error('样例缺少固定资产版本。')
    if (a !== asset.asset_id || v !== asset.asset_version_id) {
      if (!asset.manifest.depends_on.some(d => d.asset_id === a && d.asset_version_id === v)) throw Error('样例不在当前资产的固定依赖中。')
      owner = readAsset(await call('world.get', { asset_id: a, asset_version_id: v }), a, v)
    }
  } else if (reference.kind !== 'file') throw Error('该样例引用暂不支持预览。')
  const file = owner.manifest.files.find(f => f.file_id === reference.file_id)
  if (!file) throw Error('样例文件不在固定资产清单中。')
  const read = object(await call('artifact.read', { mode: 'download', content_ref: file.content_ref,
    access: { asset_id: owner.asset_id, asset_version_id: owner.asset_version_id } }))
  const v = object(read.version), ref = file.content_ref
  if (read.mode !== 'download' || v.store_id !== ref.store_id || v.artifact_id !== ref.artifact_id || v.version_id !== ref.version_id)
    throw Error('样例与所选固定内容版本不一致。')
  if (typeof v.mime !== 'string' || !Number.isSafeInteger(v.size) || Number(v.size) < 0 || typeof v.sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(v.sha256))
    throw Error('样例文件信息不完整。')
  return { path: sampleContentPath(read.content_path), mime: v.mime.split(';')[0].trim().toLowerCase(), size: Number(v.size), sha256: v.sha256 }
}
export function sampleKind(mime: string): 'image' | 'video' | 'audio' | 'text' | 'unsupported' {
  if (['image/png','image/jpeg','image/webp','image/gif'].includes(mime)) return 'image'
  if (['video/mp4','video/webm','video/ogg'].includes(mime)) return 'video'
  if (['audio/mpeg','audio/mp3','audio/mp4','audio/wav','audio/x-wav','audio/flac','audio/ogg'].includes(mime)) return 'audio'
  if (['text/plain','text/markdown','application/json'].includes(mime)) return 'text'
  return 'unsupported'
}
export async function readSampleBytes(read: SampleDownload, signal: AbortSignal, fetcher: typeof fetch = fetch): Promise<Blob> {
  const limit = sampleKind(read.mime) === 'text' ? 4 * 1024 * 1024 : 128 * 1024 * 1024
  if (sampleKind(read.mime) === 'unsupported') throw Error('此文件格式不支持内嵌预览。')
  if (read.size > limit) throw Error('样例超过预览大小上限。')
  const response = await fetcher(sampleContentPath(read.path), { signal, credentials: 'same-origin', redirect: 'error', cache: 'no-store' })
  if (!response.ok || !response.body) throw Error('样例读取失败或无权访问，请重试。')
  const reader = response.body.getReader(), chunks: BlobPart[] = []
  let size = 0
  try {
    for (;;) {
      const next = await reader.read()
      if (next.done) break
      size += next.value.byteLength
      if (size > read.size || size > limit) { await reader.cancel(); throw Error('样例大小与固定记录不符。') }
      chunks.push(new Uint8Array(next.value))
    }
  } finally { reader.releaseLock() }
  signal.throwIfAborted()
  const blob = new Blob(chunks, { type: read.mime })
  if (blob.size !== read.size) throw Error('样例未完整读取。')
  const hash = await crypto.subtle.digest('SHA-256', await blob.arrayBuffer())
  signal.throwIfAborted()
  if (Array.from(new Uint8Array(hash), b => b.toString(16).padStart(2,'0')).join('') !== read.sha256) throw Error('样例内容校验失败。')
  return blob
}
