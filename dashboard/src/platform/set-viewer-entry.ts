// Set Package v1.0 detection and cross-app navigation.openApp helpers for World dashboard.

const UUID_V7_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/
const SET_ID_RE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/

export interface WorldManifestFile {
  file_id: string
  path: string
  role: string
  content_ref: {
    store_id: string
    artifact_id: string
    version_id: string
  }
}

export interface DetectedWorldSetPackage {
  setId: string
  setJsonFile: WorldManifestFile
  sceneGlbFile?: WorldManifestFile
}

export type ViewerLaunchOutcome = 'opened' | 'not_installed' | 'forbidden' | 'failed'

export function detectWorldSetPackage(
  files: readonly WorldManifestFile[] | undefined | null
): DetectedWorldSetPackage | null {
  if (!Array.isArray(files) || files.length === 0) return null
  const byPath = new Map<string, WorldManifestFile>()
  for (const f of files) {
    if (f && typeof f.path === 'string' && typeof f.file_id === 'string') {
      const clean = f.path.replace(/^\/+/, '').trim()
      if (clean) byPath.set(clean, f)
    }
  }

  for (const [pathStr, file] of byPath.entries()) {
    const m = /^sets\/([^/]+)\/set\.json$/.exec(pathStr)
    if (m) {
      const setId = m[1]
      const glb = byPath.get(`sets/${setId}/scene.glb`)
      if (glb && SET_ID_RE.test(setId)) {
        return {
          setId,
          setJsonFile: file,
          sceneGlbFile: glb,
        }
      }
    }
  }

  const rootSet = byPath.get('set.json')
  const rootGlb = byPath.get('scene.glb')
  if (rootSet && rootGlb) {
    return {
      setId: 'default',
      setJsonFile: rootSet,
      sceneGlbFile: rootGlb,
    }
  }

  const byRole = files.find(f => f.role === 'set_package')
  if (byRole) {
    return {
      setId: 'default',
      setJsonFile: byRole,
      sceneGlbFile: rootGlb,
    }
  }

  return null
}

export function buildWorldViewerOpenTarget(params: {
  assetId: string
  versionId: string
  setId?: string
  cameraId?: string
}): { appId: 'viewer'; path: string } {
  const assetId = (params.assetId || '').trim()
  const versionId = (params.versionId || '').trim()
  if (!UUID_V7_RE.test(assetId) || !UUID_V7_RE.test(versionId)) {
    throw new Error('打开片场查看器需要有效的资产 ID 与固定资产版本 ID。')
  }
  const q = new URLSearchParams({
    asset: assetId,
    version: versionId,
  })
  const setId = (params.setId || '').trim()
  if (setId && setId !== 'default' && SET_ID_RE.test(setId)) {
    q.set('set', setId)
  }
  const cam = (params.cameraId || 'C1').trim()
  if (cam && SET_ID_RE.test(cam)) {
    q.set('cam', cam)
  }
  return {
    appId: 'viewer',
    path: `/?${q.toString()}`,
  }
}

export function classifyViewerOpenError(cause: unknown): ViewerLaunchOutcome {
  const code =
    cause && typeof cause === 'object' && 'code' in cause
      ? String((cause as { code?: unknown }).code || '')
      : cause instanceof Error
        ? cause.message
        : ''
  if (
    [
      'APP_NOT_INSTALLED',
      'NO_ACTIVE_INSTALLATION',
      'APP_NOT_FOUND',
      'NOT_FOUND',
      'CAPABILITY_UNAVAILABLE',
      'TARGET_NOT_FOUND',
      'APP_NAVIGATION_UNAVAILABLE',
    ].includes(code)
  ) {
    return 'not_installed'
  }
  if (['PERMISSION_DENIED', 'FORBIDDEN', 'CAPABILITY_DENIED'].includes(code)) {
    return 'forbidden'
  }
  return 'failed'
}
