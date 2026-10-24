/** A host request can resolve before its route subscription catches up. */
export function createNavigationQueue(hostPath: () => string, request: (path: string) => Promise<unknown>, rollback: (path: string) => void) {
  let desired: string | null = null, inflight: string | null = null, disposed = false
  let acknowledge: (() => void) | null = null
  async function drain() {
    if (inflight !== null || disposed) return
    while (desired !== null && !disposed) {
      const path = desired
      if (path === hostPath()) { desired = null; return }
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
      } catch {
        if (!disposed && desired === path) { desired = null; rollback(hostPath()) }
      } finally {
        if (timer) clearTimeout(timer)
        acknowledge = null
      }
      if (desired === path) desired = null
      inflight = null
    }
  }
  return {
    request(path: string) { if (disposed) return; desired = path; void drain() },
    staleEcho(path: string) {
      if (inflight !== path) return false
      acknowledge?.()
      return desired !== null && desired !== path
    },
    dispose() { disposed = true; desired = null; acknowledge?.() },
  }
}
