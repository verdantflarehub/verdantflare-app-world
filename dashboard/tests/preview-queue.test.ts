import { test } from 'node:test'
import assert from 'node:assert/strict'
import { queuePreview } from '../src/platform/preview-queue.ts'

test('catalog reads stay within four concurrent pipelines and skip cancelled cards', async () => {
  let active = 0, peak = 0, cancelledStarted = false
  const releases: Array<() => void> = []
  const read = () => new Promise<number>(resolve => {
    active++; peak = Math.max(peak, active)
    releases.push(() => { active--; resolve(1) })
  })
  const live = Array.from({ length: 4 }, () => queuePreview(read, new AbortController().signal))
  await Promise.resolve()
  const cancelled = new AbortController()
  const skipped = queuePreview(async () => { cancelledStarted = true }, cancelled.signal)
  const rejected = assert.rejects(skipped, { name: 'AbortError' })
  const next = queuePreview(read, new AbortController().signal)
  cancelled.abort(); await rejected
  assert.equal(releases.length, 4)
  releases.shift()!()
  await live[0]; await new Promise(resolve => setImmediate(resolve))
  assert.equal(releases.length, 4)
  releases.splice(0).forEach(release => release())
  await Promise.all([...live, next])
  assert.equal(peak, 4); assert.equal(cancelledStarted, false)
})

test('a failed preview releases capacity for the next visible card', async () => {
  await assert.rejects(queuePreview(async () => { throw Error('unreadable') }, new AbortController().signal), /unreadable/)
  assert.equal(await queuePreview(async () => 'next', new AbortController().signal), 'next')
})
