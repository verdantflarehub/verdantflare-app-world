import assert from 'node:assert/strict'
import test from 'node:test'
import {
  buildWorldViewerOpenTarget,
  classifyViewerOpenError,
  detectWorldSetPackage,
} from '../src/platform/set-viewer-entry.ts'

test('detectWorldSetPackage identifies set.json + scene.glb in World asset manifest', () => {
  const pkg = detectWorldSetPackage([
    {
      file_id: '01a1691d-0000-7000-8000-000000000001',
      path: 'sets/saw-bathroom-v9/set.json',
      role: 'set_package',
      content_ref: {
        store_id: '01a1691d-0000-7000-8000-000000000010',
        artifact_id: '01a1691d-0000-7000-8000-000000000011',
        version_id: '01a1691d-0000-7000-8000-000000000012',
      },
    },
    {
      file_id: '01a1691d-0000-7000-8000-000000000002',
      path: 'sets/saw-bathroom-v9/scene.glb',
      role: 'model',
      content_ref: {
        store_id: '01a1691d-0000-7000-8000-000000000010',
        artifact_id: '01a1691d-0000-7000-8000-000000000013',
        version_id: '01a1691d-0000-7000-8000-000000000014',
      },
    },
  ])
  assert.ok(pkg)
  assert.equal(pkg.setId, 'saw-bathroom-v9')
  assert.equal(pkg.sceneGlbFile?.file_id, '01a1691d-0000-7000-8000-000000000002')
})

test('buildWorldViewerOpenTarget pins fixed asset_version_id and classifies uninstalled/forbidden errors', () => {
  const target = buildWorldViewerOpenTarget({
    assetId: '01a16250-d1aa-7667-add1-588f58042bbc',
    versionId: '01a1691d-bd00-7195-bb28-982913b20c14',
    setId: 'saw-bathroom-v9',
    cameraId: 'C1',
  })
  assert.equal(target.appId, 'viewer')
  assert.equal(
    target.path,
    '/?asset=01a16250-d1aa-7667-add1-588f58042bbc&version=01a1691d-bd00-7195-bb28-982913b20c14&set=saw-bathroom-v9&cam=C1'
  )

  assert.throws(() =>
    buildWorldViewerOpenTarget({
      assetId: '01a16250-d1aa-7667-add1-588f58042bbc',
      versionId: 'latest',
    })
  )

  assert.equal(classifyViewerOpenError({ code: 'APP_NOT_INSTALLED' }), 'not_installed')
  assert.equal(classifyViewerOpenError({ code: 'FORBIDDEN' }), 'forbidden')
})
