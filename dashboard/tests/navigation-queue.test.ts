import test from 'node:test'
import assert from 'node:assert/strict'
import { createNavigationQueue } from '../src/platform/navigation-queue.ts'
const tick=()=>new Promise(resolve=>setImmediate(resolve))
test('rapid return waits for older host request and ignores its late echo',async()=>{
 let host='/collections', finish!:()=>void
 const calls:string[]=[], rolled:string[]=[]
 const q=createNavigationQueue(()=>host,path=>{calls.push(path);return new Promise<void>(resolve=>{finish=()=>{host=path;resolve()}})},path=>rolled.push(path))
 q.request('/collections?trash=1')
 q.request('/collections') // Same as old host route, but an earlier request is in flight.
 assert(q.staleEcho('/collections?trash=1'))
 assert.deepEqual(calls,['/collections?trash=1'])
 finish();await tick()
 assert.deepEqual(calls,['/collections?trash=1','/collections'])
 assert.equal(q.staleEcho('/collections'),false)
 finish();await tick();assert.equal(host,'/collections');assert.deepEqual(rolled,[])
})
test('a failed superseded request does not roll back the latest click',async()=>{
 let host='/', reject!:(e:Error)=>void
 const calls:string[]=[],rolled:string[]=[]
 const q=createNavigationQueue(()=>host,path=>{calls.push(path);if(path==='/old')return new Promise((_,r)=>{reject=r});host=path;return Promise.resolve()},p=>rolled.push(p))
 q.request('/old');q.request('/latest');reject(new Error('offline'));await tick()
 assert.equal(host,'/latest');assert.deepEqual(rolled,[])
 q.dispose();q.request('/ignored');assert.deepEqual(calls,['/old','/latest'])
})
test('current denied navigation restores the authorized host route',async()=>{
 const rolled:string[]=[];const q=createNavigationQueue(()=>'/authorized',()=>Promise.reject(Error('denied')),p=>rolled.push(p))
 q.request('/denied');await tick();assert.deepEqual(rolled,['/authorized'])
})
test('request resolution precedes host echo: quick return is retained until both settle', async () => {
 let host='/collections',finish!:()=>void
 const calls:string[]=[],rolled:string[]=[]
 const q=createNavigationQueue(()=>host,path=>{calls.push(path);return new Promise<void>(r=>{finish=r})},p=>rolled.push(p))
 q.request('/collections?trash=1')
 finish();await tick() // Host router finished; SDK subscription is still stale.
 q.request('/collections')
 assert.deepEqual(calls,['/collections?trash=1'])
 host='/collections?trash=1'
 assert.equal(q.staleEcho(host),true) // Late echo must not undo the user's return.
 await tick();assert.deepEqual(calls,['/collections?trash=1','/collections'])
 finish();await tick()
 host='/collections';assert.equal(q.staleEcho(host),false)
 await tick();assert.deepEqual(rolled,[])
 q.request('/collections');await tick();assert.equal(calls.length,2)
 q.dispose()
})
test('disposal while waiting for host echo releases the pending queue', async () => {
 const calls:string[]=[]
 const q=createNavigationQueue(()=>'/old',p=>{calls.push(p);return Promise.resolve()},()=>assert.fail('disposed rollback'))
 q.request('/new');await tick();q.request('/later');q.dispose();await tick()
 assert.deepEqual(calls,['/new'])
})
