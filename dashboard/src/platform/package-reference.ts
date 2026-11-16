import type { AppSDK } from './types.ts'
import { createDraftAPI, DraftError } from './draft-client.ts'
import { object, contentRef, type ContentRef, type CollectionDocument } from './draft-model.ts'
import { publicationIdentity } from './publication-client.ts'
import { packageCanonical } from './learning-package.ts'
import { packageJSON, validatePackageSchema } from './package-json.ts'
import { validateImportedBindings } from './package-semantics.ts'
import { bytesHash } from './package-upload.ts'
import { sampleContentPath } from './prompt-samples.ts'

type Row = Record<string, unknown>
export interface FixedPackageReference { kind: 'reference'; reference: Row; content_ref: ContentRef; mime: string; size: number; sha256: string }
const equal = (a: unknown, b: unknown) => packageCanonical(a) === packageCanonical(b)
const invalid = (): never => { throw Error('固定引用的内容身份或摘要不一致。') }
export function packageReferenceAccess(ref: Row): Row {
  if (ref.kind === 'world_draft') return { world_draft_id: ref.draft_id, world_draft_revision_id: ref.revision_id }
  if (ref.kind === 'project') return { project_id: ref.project_id, project_revision_id: ref.revision_id }
  if (ref.kind === 'asset') return { asset_id: ref.asset_id, asset_version_id: ref.asset_version_id }
  return invalid()
}

/** Current fixed business reads only; no URL, token or file enumeration from a
 * package. Own lifecycle cancellation also covers streaming response bodies. */
export function createPackageReferenceReader(sdk: AppSDK, fetcher: typeof fetch = fetch) {
  const api = createDraftAPI(sdk), initial = publicationIdentity(sdk.context.get()), organization = sdk.context.get().organizationId
  let controller = new AbortController(), hidden = false, disposed = false
  const stops: (() => void)[] = []
  function live(signal = controller.signal) {
    api.live()
    if (disposed || publicationIdentity(sdk.context.get()) !== initial) throw new DraftError('SESSION_REVOKED')
    if (hidden || signal.aborted) throw new DraftError('DASHBOARD_HIDDEN')
  }
  function dispose() { disposed = true; controller.abort(); stops.splice(0).forEach(stop => stop()); api.dispose() }
  stops.push(sdk.context.subscribe(() => { if (publicationIdentity(sdk.context.get()) !== initial) dispose() }))
  stops.push(sdk.lifecycle.subscribe(event => {
    if (event === 'dispose') dispose()
    else if (event === 'hidden') { hidden = true; controller.abort() }
    else { hidden = false; if (controller.signal.aborted) controller = new AbortController() }
  }))
  async function resolve(reference: Row): Promise<FixedPackageReference> {
    const signal = controller.signal; live(signal)
    const request = { mode: 'resolve_reference', reference: structuredClone(reference) }
    validatePackageSchema('world-draft-request', request)
    const reply = object(await api.request(request)); live(signal)
    validatePackageSchema('world-draft-response', reply)
    if (reply.kind !== 'reference' || !equal(reply.reference, request.reference) || !Number.isSafeInteger(reply.size)) invalid()
    contentRef(reply.content_ref)
    return reply as unknown as FixedPackageReference
  }
  async function download(fixed: FixedPackageReference, limit: number, signal: AbortSignal): Promise<Uint8Array> {
    live(signal)
    if (!Number.isSafeInteger(limit) || limit < 0 || limit > 64 * 1024 * 1024 || fixed.size > limit) throw new DraftError('PACKAGE_LIMIT')
    const reply = object(await api.request({ mode: 'download', content_ref: fixed.content_ref, access: packageReferenceAccess(fixed.reference) }, 'artifact.read'))
    live(signal)
    const version = object(reply.version)
    if (reply.mode !== 'download' || !equal(contentRef(version), fixed.content_ref) || version.organization_id !== organization || version.size !== fixed.size || version.sha256 !== fixed.sha256 || version.mime !== fixed.mime) invalid()
    const path = sampleContentPath(reply.content_path)
    const response = await fetcher(path, { credentials: 'same-origin', redirect: 'error', cache: 'no-store', signal })
    try { live(signal) } catch (error) { await response.body?.cancel().catch(() => {}); throw error }
    if (!response.ok || !response.body) throw Error('文件读取失败或权限已变化。')
    const stream = response.body.getReader(), parts: Uint8Array[] = []
    let size = 0
    const cancel = () => { void stream.cancel().catch(() => {}) }
    signal.addEventListener('abort', cancel, { once: true })
    try {
      for (;;) {
        const next = await stream.read(); live(signal)
        if (next.done) break
        size += next.value.byteLength
        if (size > fixed.size || size > limit) invalid()
        parts.push(next.value)
      }
    } catch (error) { await stream.cancel().catch(() => {}); throw error }
    finally { signal.removeEventListener('abort', cancel); stream.releaseLock() }
    if (size !== fixed.size) invalid()
    const bytes = new Uint8Array(size); let offset = 0
    for (const part of parts) { bytes.set(part, offset); offset += part.byteLength }
    if (await bytesHash(bytes) !== fixed.sha256) invalid()
    live(signal)
    return bytes
  }
  async function read(reference: Row, limit = 64 * 1024 * 1024) {
    const signal = controller.signal, fixed = await resolve(reference)
    const bytes = await download(fixed, limit, signal)
    return { fixed, bytes }
  }
  async function authorize(refs: Row[], document: CollectionDocument): Promise<void> {
    const signal = controller.signal, objects = new Map<string, Row>(), documents = new Map<string, Row>()
    for (const reference of refs) {
      const fixed = await resolve(reference); live(signal)
      if (!reference.object_key) continue
      const key = packageCanonical([fixed.content_ref, packageReferenceAccess(reference)])
      let source = documents.get(key)
      if (!source) {
        source = object(packageJSON(await download(fixed, 4 * 1024 * 1024, signal)))
        validatePackageSchema(source.schema_version === 2 ? 'reference-learning-v2' : 'reference-learning', source)
        documents.set(key, source)
      }
      const selected = [...source.sources as Row[], ...source.results as Row[]].filter(r => r.key === reference.object_key)
      if (selected.length !== 1) invalid()
      objects.set(packageCanonical(reference), selected[0])
    }
    validateImportedBindings(document, objects); live(signal)
  }
  return { resolve, read, authorize, dispose }
}
