import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

const pagesDir = join(import.meta.dirname, '..', 'src', 'pages')
const readPage = (name: string) => readFileSync(join(pagesDir, name), 'utf8')

test('every World subpage renders a single internal breadcrumb component and no page-level back button', () => {
  for (const name of ['ReferenceGallery.vue', 'ReferenceDetailPage.vue', 'CollectionsPage.vue', 'LearnPage.vue', 'PublicationPage.vue', 'WorldBrowser.vue']) {
    const src = readPage(name)
    assert.match(src, /<WorldTrail\b/, `${name} must render WorldTrail`)
  }
  const review = readPage('SetReviewPage.vue')
  assert.match(review, /aria-label="面包屑"/, 'SetReviewPage.vue must render a single accessible breadcrumb')

  for (const [name, forbidden] of [
    ['CollectionsPage.vue', '← 返回收录内容'],
    ['LearnPage.vue', '返回数字世界'],
    ['LearnPage.vue', '返回拆解入口'],
    ['PublicationPage.vue', '返回资产目录'],
  ] as const) {
    assert.equal(readPage(name).includes(forbidden), false, `${name} must not contain page-level back link "${forbidden}"`)
  }
})
