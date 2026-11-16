/** A host request can resolve before its route subscription catches up. */
export function createNavigationQueue(hostPath: () => string, request: (path: string) => Promise<unknown>, rollback: (path: string) => void) {
  let desired: { path: string; prerequisite?: string } | null = null, inflight: string | null = null, disposed = false
  let acknowledge: (() => void) | null = null
  async function drain() {
    if (inflight !== null || disposed) return
    while (desired !== null && !disposed) {
      const intent = desired
      if (intent.path === hostPath()) { desired = null; return }
      const path = intent.prerequisite && intent.prerequisite !== hostPath() ? intent.prerequisite : intent.path
      inflight = path
      let observed = false, timer: ReturnType<typeof setTimeout> | undefined
      const echo = new Promise<void>(resolve => { acknowledge = () => { observed = true; resolve() } })
      try {
        await request(path)
        if (!disposed && !observed && hostPath() !== path) {
          await Promise.race([echo, new Promise<never>((_, reject) => {
            timer = setTimeout(() => reject(Error('HOST_ROUTE_TIMEOUT')), 15_000)
          })])
        }
      } catch (error) {
        // Host history can change before its subscription reaches the app.
        // A superseded permission check cancels the whole queued intent.
        const superseded = error instanceof Error && error.message === 'NAVIGATION_SUPERSEDED'
        if (!disposed && (superseded || desired === intent)) { desired = null; rollback(hostPath()) }
      } finally {
        if (timer) clearTimeout(timer)
        acknowledge = null
      }
      if (desired === intent) {
        if (path === intent.path) desired = null
        else intent.prerequisite = undefined
      }
      inflight = null
    }
  }
  return {
    request(path: string, prerequisite?: string) { if (disposed) return; desired = { path, prerequisite }; void drain() },
    staleEcho(path: string) {
      if (inflight !== path) {
        // Browser Back or another host navigation supersedes queued app clicks.
        // A pending permission check may still finish; drain must restore this
        // latest host intent rather than replaying a queued detail afterwards.
        if (inflight !== null) desired = { path }
        return false
      }
      acknowledge?.()
      return desired !== null && desired.path !== path
    },
    dispose() { disposed = true; desired = null; acknowledge?.() },
  }
}
