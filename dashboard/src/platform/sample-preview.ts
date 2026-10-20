import type { AppSDK } from './types.ts'
let owner: object | undefined, generation = 0
export function closeSamplePreview(sdk: AppSDK, key: object) {
  if (owner !== key) return
  owner = undefined; generation++; sdk.media?.close()
}
export async function openSamplePreview(sdk: AppSDK, key: object, blob: Blob, title: string) {
  if (!sdk.media) throw Error('当前宿主尚未提供图片放大能力。')
  owner = key; const current = ++generation
  const bytes = await blob.arrayBuffer()
  if (owner !== key || current !== generation) return
  await sdk.media.preview({ bytes, mime: blob.type, title: title.slice(0,256) })
}
