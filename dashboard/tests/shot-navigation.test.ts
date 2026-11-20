import test from 'node:test'
import assert from 'node:assert/strict'
import { reactive } from 'vue'
import { createShotListMemory, extendShotList, rememberShotList, recallShotList, shotPath } from '../src/platform/shot-navigation.ts'
import { canonicalRoute } from '../src/platform/route-links.ts'
import type { AssetItem } from '../src/platform/world-model.ts'
const id=(n:number)=>'01900000-0000-7000-8000-'+n.toString(16).padStart(12,'0')
test('shot detail addresses preserve fixed versions and reject missing or invalid versions',()=>{
 const path=shotPath({asset_id:id(1),head_asset_version_id:id(2)})
 assert.equal(canonicalRoute('world',path),path)
 for(const path of [`/shot/asset/${id(1)}`,`/shot/asset/${id(1)}?version=latest`])assert.throws(()=>canonicalRoute('world',path))
})
test('list snapshots support Vue proxies and restore filters, loaded order, cursor and scroll only for the same identity',()=>{
 const items=reactive([{asset_id:id(1),head_asset_version_id:id(2)},{asset_id:id(3),head_asset_version_id:id(4)}] as AssetItem[])
 rememberShotList('session-a',{path:'/shot?q=tracking&relation=produced_in',items,cursor:id(5),scroll:640})
 items.reverse()
 const remembered=recallShotList('session-a','/shot?q=tracking&relation=produced_in')!
 assert.equal(remembered.items[0].asset_id,id(1));assert.equal(remembered.cursor,id(5));assert.equal(remembered.scroll,640)
 remembered.items=[]
 assert.equal(recallShotList('session-a')?.items.length,2)
 assert.equal(recallShotList('session-a','/shot?q=other'),undefined)
 assert.equal(recallShotList('session-b'),undefined)
})

test('next navigation crosses duplicate-only pages and preserves previously selected fixed versions',async()=>{
 const original={asset_id:id(1),head_asset_version_id:id(2)} as AssetItem
 const extra={asset_id:id(3),head_asset_version_id:id(4)} as AssetItem
 const list={path:'/shot?q=track',items:[original],cursor:'a',scroll:20}
 const calls:string[]=[]
 const next=await extendShotList(list,async cursor=>{calls.push(cursor);return cursor==='a'?{items:[{...original,head_asset_version_id:id(99)}],next_cursor:'b'}:{items:[extra,extra],next_cursor:''}})
 assert.deepEqual(calls,['a','b']);assert.deepEqual(next.items,[original,extra]);assert.equal(list.cursor,'a')
})
test('cyclic or endlessly overlapping pagination fails without changing the restored list',async()=>{
 const list={path:'/shot',items:[],cursor:'a',scroll:20}
 await assert.rejects(extendShotList(list,async cursor=>({items:[],next_cursor:cursor==='a'?'b':'a'})))
 let count=0
 await assert.rejects(extendShotList(list,async()=>({items:[],next_cursor:String(++count)})))
 assert.equal(count,25);assert.equal(list.cursor,'a')
 const empty=await extendShotList(list,async()=>({items:[],next_cursor:''}))
 assert.equal(empty.cursor,'');assert.equal(empty.items.length,0)
})

test('same authorized session restores lists after host remount; changed identity, corruption and blocked storage stay isolated',()=>{
 let raw=''
 const storage={getItem:()=>raw,setItem:(_key:string,value:string)=>{raw=value}}
 const item={asset_id:id(1),head_asset_version_id:id(2),name:'Track',asset_type:'shot',subjects:[],created_at:'2026-11-20T00:00:00Z'}
 const list={path:'/shot?q=track',items:[item],cursor:id(3),scroll:470}
 createShotListMemory(storage).remember('same-authorization',list)
 assert.deepEqual(createShotListMemory(storage).recall('same-authorization'),list)
 assert.equal(createShotListMemory(storage).recall('new-authorization'),undefined)
 raw=raw.replace('/shot?q=track','//untrusted.invalid')
 assert.equal(createShotListMemory(storage).recall('same-authorization'),undefined)
 raw='['
 assert.equal(createShotListMemory(storage).recall('same-authorization'),undefined)
 const blocked=createShotListMemory({getItem:()=>{throw Error('blocked')},setItem:()=>{throw Error('quota')}})
 blocked.remember('session',list);assert.deepEqual(blocked.recall('session'),list)
 assert.equal(blocked.recall('other'),undefined)
})
