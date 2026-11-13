import test from 'node:test'
import assert from 'node:assert/strict'
import { assetFileURL } from '../src/platform/asset-media.ts'
const scope = { asset_id: 'asset', asset_version_id: 'version' }
const file = (n: number) => ({ file_id: String(n), path: `${n}.jpg`, role: 'preview', content_ref: {store_id:'store',artifact_id:String(n),version_id:'version'} })
const path = (n: number) => `/studio/media/${'a'.repeat(64)}/${n.toString(16).padStart(64,'0')}/original-v1`
test('more than 128 images use stable representations without temporary handles', async () => {
  let active=0, peak=0, count=0
  const call=async (name: string, args: Record<string, unknown>) => {
    assert.equal(name,'artifact.read'); assert.equal(args.representation,'original-v1')
    active++;peak=Math.max(peak,active);count++
    await new Promise(r=>setTimeout(r,1));active--
    return {content_path:path(Number((args.content_ref as {artifact_id:string}).artifact_id))}
  }
  const urls=await Promise.all(Array.from({length:180},(_,n)=>assetFileURL(call,scope,file(n))))
  assert.equal(new Set(urls).size,180);assert.equal(count,180);assert.ok(peak<=6)
  assert.equal(await assetFileURL(call,scope,file(0)),path(0));assert.equal(count,180)
})
test('failed reads can retry and different callers never share cached authorization', async () => {
  let n=0
  const call=async()=>{if(++n===1)throw Error('unavailable');return {content_path:path(n)}}
  await assert.rejects(assetFileURL(call,scope,file(1)))
  assert.equal(await assetFileURL(call,scope,file(1)),path(2))
  assert.equal(await assetFileURL(call,scope,file(1),true),path(3))
  await assert.rejects(assetFileURL(async()=>{throw Error('denied')},scope,file(1)),/denied/)
})
test('reject external, malformed and legacy temporary image paths', async () => {
  for(const content_path of ['https://example.com/image.jpg','/studio/media/a/b/original-v1','/studio/dashboards/x/content/y',path(1)+'?token=x'])
    await assert.rejects(assetFileURL(async()=>({content_path}),scope,file(1)))
})
