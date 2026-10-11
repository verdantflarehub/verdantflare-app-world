import test from 'node:test'
import assert from 'node:assert/strict'
import { canonicalRoute, legacyStudioTarget, projectTarget, worldTarget, useAssetTarget } from '../src/platform/route-links.ts'
const a = '01900000-0000-7000-8000-000000000001', b = '01900000-0000-7000-8000-000000000002'
test('legacy bookmarks preserve the fixed revision, filters and app route', () => {
  const path = `/projects?project=${a}&revision=${b}&category=video&q=summer&tab=cast&item=lead`
  assert.deepEqual(legacyStudioTarget(path), { appId: 'project', path })
  assert.deepEqual(legacyStudioTarget(`/projects?mode=world&asset=${a}&version=${b}&asset_type=music-work`), { appId: 'world', path: `/?asset=${a}&version=${b}&asset_type=music-work` })
  assert.deepEqual(legacyStudioTarget('/projects?view=files'), { appId: 'project', path: '/projects' })
  assert.deepEqual(legacyStudioTarget('/projects?view=files&project=' + a), { appId: 'project', path: '/workspace?project=' + a })
  assert.deepEqual(legacyStudioTarget('/projects?mode=world&view=files&asset=' + a), { appId: 'world', path: '/?asset=' + a })
  assert.equal(canonicalRoute('project', '/use-asset?asset=' + a + '&version=' + b), '/use-asset?asset=' + a + '&version=' + b)
  assert.equal(canonicalRoute('project', '/?category=music'), '/projects?category=music')
})
test('cross application targets contain only app identity and fixed business version', () => {
  assert.deepEqual(projectTarget(a, b), { appId: 'project', path: `/projects?project=${a}&revision=${b}` })
  assert.deepEqual(worldTarget(a, b), { appId: 'world', path: `/?asset=${a}&version=${b}` })
  assert.deepEqual(useAssetTarget(a, b), { appId: 'project', path: `/use-asset?asset=${a}&version=${b}` })
  assert.throws(() => useAssetTarget(a, 'head'))
  assert.throws(() => projectTarget(a, 'head'))
  assert.throws(() => worldTarget(a, 'latest'))
  for (const path of ['//outside.example/projects', '/projects?project=x&project=y', '/projects#other', '/projects/../world']) assert.throws(() => canonicalRoute('project', path))
})
