import test from 'node:test'
import assert from 'node:assert/strict'
import { createCollectionLifecycle } from '../src/platform/collection-lifecycle.ts'
import { readLifecycle, type DraftLifecycle } from '../src/platform/draft-model.ts'
import type { AppSDK, Context, Json } from '../src/platform/types.ts'
const id=(n:number)=>'01900000-0000-7000-8000-'+n.toString(16).padStart(12,'0')
const active:DraftLifecycle={state:'active',version:1,trashed_at:null,trashed_by:null,can_trash:true,can_restore:false}
function fixture(){
 const values=new Map<string,string>(),receipts=new Map<string,Json>(),calls:Record<string,unknown>[]=[]
 let lost=false,fail='',context:Context={userId:'a',organizationId:'o',stationId:'s',appId:'world',installationId:'i',contextRevision:'r',projectId:null}
 const listeners=new Set<(c:Context)=>void>()
 const sdk:AppSDK={version:'1.1.0',context:{get:()=>context,subscribe:f=>{listeners.add(f);return()=>listeners.delete(f)}},theme:{get:()=>'dark',subscribe:()=>()=>{}},lifecycle:{subscribe:()=>()=>{}},navigation:{get:()=>({path:'/',revision:1}),subscribe:()=>()=>{},request:async()=>{}},capabilities:{call:async()=>null},app:{request:async input=>{
  assert.equal(input.path,'/api/v1/world/draft');const r=input.body as Record<string,unknown>;calls.push(r)
  if(fail)return {status:409,data:{code:fail},requestId:'x'}
  let result=receipts.get(String(r.operation_id))
  if(!result){const lifecycle:DraftLifecycle=r.mode==='trash'?{state:'trashed',version:Number(r.expected_lifecycle_version)+1,trashed_at:'2026-10-24T00:00:00Z',trashed_by:id(2),can_trash:false,can_restore:true}:{...active,version:Number(r.expected_lifecycle_version)+1};result={kind:'lifecycle',draft_id:String(r.draft_id),operation_id:String(r.operation_id),lifecycle:{...lifecycle}};receipts.set(String(r.operation_id),result)}
  if(lost){lost=false;throw Error('connection lost after commit')}
  return {status:200,data:result,requestId:'x'}
 }}}
 return {sdk,calls,receipts,storage:{getItem:(k:string)=>values.get(k)||null,setItem:(k:string,v:string)=>{values.set(k,v)},removeItem:(k:string)=>{values.delete(k)}},lose:()=>{lost=true},fail:(code:string)=>{fail=code},change:()=>{context={...context,userId:'b'};listeners.forEach(f=>f(context))}}
}
test('trash response loss recovers same operation after reload and supports undo',async()=>{
 const f=fixture();f.lose();let writer=createCollectionLifecycle(f.sdk,f.storage)
 await assert.rejects(writer.start('trash',id(1),active));const pending=writer.pending();assert.ok(pending)
 assert.throws(()=>writer.releaseFailure());writer.dispose();writer=createCollectionLifecycle(f.sdk,f.storage)
 const result=await writer.resume();assert.equal(result.lifecycle.state,'trashed');assert.equal(f.receipts.size,1);assert.deepEqual(f.calls[0],f.calls[1]);assert.equal(writer.pending(),null)
 const restored=await writer.start('restore',id(1),result.lifecycle);assert.equal(restored.lifecycle.state,'active');assert.equal(restored.lifecycle.version,3);assert.equal(f.receipts.size,2);writer.dispose()
})
test('conflicts do not retry blindly; unavailable recovery storage prevents any mutation',async()=>{
 const f=fixture();f.fail('REVISION_CONFLICT');const writer=createCollectionLifecycle(f.sdk,f.storage)
 await assert.rejects(writer.start('trash',id(1),active));assert.equal(writer.pending()?.failure,'REVISION_CONFLICT')
 await assert.rejects(writer.resume());assert.equal(f.calls.length,1);writer.releaseFailure();assert.equal(writer.pending(),null);writer.dispose()
 const denied=createCollectionLifecycle(f.sdk,{...f.storage,setItem:()=>{throw Error('full')}})
 assert.throws(()=>denied.start('trash',id(1),active));assert.equal(f.calls.length,1);denied.dispose()
})
test('identity changes prevent stale writes and legacy records have no guessed mutation permissions',()=>{
 const f=fixture(),writer=createCollectionLifecycle(f.sdk,f.storage);f.change()
 assert.throws(()=>writer.start('trash',id(1),active));assert.equal(f.calls.length,0);writer.dispose()
 assert.equal(readLifecycle(undefined),undefined)
 assert.throws(()=>readLifecycle({...active,state:'trashed'}))
 assert.throws(()=>readLifecycle({...active,version:1.5}))
})
