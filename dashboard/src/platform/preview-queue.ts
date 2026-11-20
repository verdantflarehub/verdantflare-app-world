// Bound the whole catalog preview read, including metadata and verified bytes.
// Cancelled cards leave the queue without starting network work.
const pending: Array<() => void> = []
let running = 0
export function queuePreview<T>(work: () => Promise<T>, signal: AbortSignal): Promise<T> {
  return new Promise((resolve, reject) => {
    const cancel = () => {
      const index = pending.indexOf(run)
      if (index >= 0) pending.splice(index, 1)
      reject(signal.reason)
    }
    const run = () => {
      signal.removeEventListener('abort', cancel)
      if (signal.aborted) { reject(signal.reason); return }
      running++
      Promise.resolve().then(work).then(resolve, reject).finally(() => { running--; pending.shift()?.() })
    }
    if (signal.aborted) { reject(signal.reason); return }
    if (running < 4) run()
    else { pending.push(run); signal.addEventListener('abort', cancel, { once: true }) }
  })
}
