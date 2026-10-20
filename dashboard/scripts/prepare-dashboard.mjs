import { createHash } from 'node:crypto'
import { readdir, readFile, writeFile } from 'node:fs/promises'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { join } from 'node:path'

const hash = bytes => createHash('sha256').update(bytes).digest('hex')
const versionPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/

/** Central dashboard-v1 metadata. No fabricated release digests or runtime addresses. */
export function metadata(appId, version, entries) {
  if (!['project', 'world'].includes(appId) || !versionPattern.test(version)) throw Error('INVALID_RELEASE_IDENTITY')
  if (!Array.isArray(entries) || !entries.length || entries.length > 256) throw Error('INVALID_ASSET_COUNT')
  const files = {}
  for (const entry of [...entries].sort((a, b) => a.path.localeCompare(b.path, 'en'))) {
    if (!/^[a-zA-Z0-9][a-zA-Z0-9._/-]*\.(html|js|css)$/.test(entry.path) || entry.path.split('/').some(part => !part || part === '.' || part === '..') || Object.hasOwn(files, entry.path)) throw Error('INVALID_ASSET_PATH')
    if (!(entry.bytes instanceof Uint8Array) || entry.bytes.byteLength > 4 * 1024 * 1024) throw Error('INVALID_ASSET_SIZE')
    files[entry.path] = hash(entry.bytes)
  }
  if (!Object.hasOwn(files, 'index.html')) throw Error('ENTRY_NOT_FOUND')
  const index = Buffer.from(JSON.stringify({ schemaVersion: 1, files }))
  if (index.byteLength > 64 * 1024) throw Error('INDEX_TOO_LARGE')
  const declaration = Buffer.from(JSON.stringify({ schemaVersion: 1, appId, appVersion: version, dashboard: {
    entryRef: 'dashboard/index.html', artifactDigest: 'sha256:' + hash(index),
    adapter: 'wujie', sdkMajor: 1, minimumSdkVersion: '1.1.0',
    requiredCapabilities: ['app.request', 'navigation.request'],
    optionalCapabilities: appId === 'project' ? ['artifact.read', 'navigation.openApp'] : ['artifact.read', 'artifact.write', 'navigation.openApp'],
    appApiPolicyRef: appId === 'project' ? 'project-dashboard-v1' : 'world-learning-v1', keepAlive: true,
  } }))
  const navigation = Buffer.from(JSON.stringify({ schemaVersion: 1, appId, appVersion: version, artifactDigest: 'sha256:' + hash(index), entry: {
    entryId: 'main', title: appId === 'project' ? '项目' : '数字世界', iconRef: appId === 'project' ? 'folder' : 'globe', route: '/', accessRuleRef: appId + '-read',
  } }))
  return { index, declaration, navigation }
}

async function main(appId) {
  const output = fileURLToPath(new URL('../dist/', import.meta.url))
  const pkg = JSON.parse(await readFile(new URL('../package.json', import.meta.url), 'utf8'))
  const version = (await readFile(new URL('../../VERSION', import.meta.url), 'utf8')).trim()
  if (version !== pkg.version) throw Error('RELEASE_VERSION_MISMATCH')
  const entries = []
  async function walk(directory, prefix = '') {
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const name = prefix + entry.name, path = join(directory, entry.name)
      if (entry.isSymbolicLink()) throw Error('SYMLINK_NOT_ALLOWED')
      if (entry.isDirectory()) await walk(path, name + '/')
      else if (entry.isFile()) entries.push({ path: name, bytes: await readFile(path) })
      else throw Error('INVALID_ASSET')
    }
  }
  await walk(output)
  const result = metadata(appId, pkg.version, entries)
  await writeFile(join(output, 'asset-index.json'), result.index, { flag: 'wx' })
  await writeFile(join(output, 'dashboard-declaration.json'), result.declaration, { flag: 'wx' })
  await writeFile(join(output, 'navigation-declaration.json'), result.navigation, { flag: 'wx' })
  process.stdout.write('Dashboard release metadata prepared.\n')
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  main(process.argv[2]).catch(error => { process.stderr.write(error.message + '\n'); process.exitCode = 1 })
}
