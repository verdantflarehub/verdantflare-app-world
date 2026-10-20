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
  assert.equal(declaration.dashboard.appApiPolicyRef, 'world-learning-v1')
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