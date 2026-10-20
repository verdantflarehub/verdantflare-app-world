import test from 'node:test'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { metadata } from '../scripts/prepare-dashboard.mjs'

const fixture = () => [{ path: 'index.html', bytes: Buffer.from('<!doctype html>') }, { path: 'assets/main.js', bytes: Buffer.from('export {};') }]
test('release metadata binds exact resource bytes, application and minimum SDK', () => {
  const entries = fixture(), a = metadata('world', '0.1.0', entries), b = metadata('world', '0.1.0', [...entries].reverse())
  assert.deepEqual(a, b)
  const index = JSON.parse(a.index.toString()), declaration = JSON.parse(a.declaration.toString())
  assert.equal(index.files['assets/main.js'], createHash('sha256').update(entries[1].bytes).digest('hex'))
  assert.equal(declaration.dashboard.artifactDigest, 'sha256:' + createHash('sha256').update(a.index).digest('hex'))
  assert.equal(declaration.appId, 'world')
  assert.equal(declaration.dashboard.appApiPolicyRef, 'world-analysis-v1')
  assert.deepEqual(declaration.dashboard.optionalCapabilities, ['artifact.read', 'artifact.write', 'navigation.openApp'])
  assert.equal(declaration.dashboard.minimumSdkVersion, '1.1.0')
  entries[1].bytes = Buffer.from('export const changed = true;')
  assert.notDeepEqual(metadata('world', '0.1.0', entries).declaration, a.declaration)
})
test('packaging rejects untrusted paths, unsupported files, duplicate entries and mutable versions', () => {
  for (const path of ['../main.js', 'assets/../main.js', 'assets//main.js', 'assets/main.js.map', 'assets/config.json']) {
    assert.throws(() => metadata('world', '0.1.0', [...fixture(), { path, bytes: Buffer.from('x') }]))
  }
  assert.throws(() => metadata('world', 'latest', fixture()))
  assert.throws(() => metadata('world', '0.1.0', [...fixture(), ...fixture()]))
})


test('navigation binds the same app release and digest without entering the frontend index', () => {
  for (const app of ['project', 'world']) {
    const result = metadata(app, '0.1.0', fixture()), navigation = JSON.parse(result.navigation.toString())
    assert.equal(navigation.artifactDigest, JSON.parse(result.declaration.toString()).dashboard.artifactDigest)
    assert.equal(navigation.appId, app)
    assert.equal(navigation.entry.accessRuleRef, app + '-read')
    assert.equal(navigation.entry.route, '/')
    assert.equal(Object.hasOwn(JSON.parse(result.index.toString()).files, 'navigation-declaration.json'), false)
  }
})
test('World publishes the approved sidebar with an explicit overview and authorized pure paths', async () => {
  const navigation = JSON.parse(metadata('world', '0.1.10', fixture()).navigation.toString())
  assert.equal(navigation.schemaVersion, 2)
  assert.deepEqual(navigation.entry.children.map(({ title, route }) => [title, route]), [
    ['资产总览', '/'], ['人物', '/character'], ['场景', '/scene'], ['声音', '/sound'],
    ['音乐', '/music'], ['镜头', '/shot'], ['剧本', '/script'], ['知识', '/knowledge'],
    ['收录内容', '/collections'], ['拆解作品', '/learn'],
  ])
  const { canonicalRoute } = await import('../src/platform/route-links.ts')
  for (const [order, child] of navigation.entry.children.entries()) {
    assert.equal(child.accessRuleRef, navigation.entry.accessRuleRef)
    assert.equal(child.order, order)
    assert.equal(canonicalRoute('world', child.route), child.route)
  }
  assert.equal(new Set(navigation.entry.children.map(child => child.entryId)).size, 10)
  const project = JSON.parse(metadata('project', '0.1.10', fixture()).navigation.toString())
  assert.equal(project.schemaVersion, 1)
  assert.equal(Object.hasOwn(project.entry, 'children'), false)
})
