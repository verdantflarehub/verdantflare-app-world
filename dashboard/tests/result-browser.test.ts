import test from 'node:test'
import assert from 'node:assert/strict'
import { effectScope, ref } from 'vue'
import { useResultBrowser, resultSummary } from '../src/platform/result-browser.ts'
import type { LearningResult } from '../src/platform/learning-results.ts'
function result(i:number):LearningResult {return {key:`item-${i}`,title:`成果 ${i}`,type_key:i%2?'shot':'scene',type_version:1,domain:'shot',basis:'observed',basis_note:'来源说明',use_conditions:[],source_refs:[],evidence:[],payload:{description:`内容 ${i}`}}}
test('232 results stay bounded; detail return preserves filtering and page; adjacent crosses pages',()=>{
 const scope=effectScope()
 scope.run(()=>{
  const state=useResultBrowser(()=>Array.from({length:232},(_,i)=>result(i)))
  assert.equal(state.pages.value,20);assert.equal(state.visible.value.length,12);assert.equal(state.active.value,undefined)
  state.page.value=2;state.selected.value='item-23'
  assert.equal(state.active.value?.title,'成果 23')
  state.move(1);assert.equal(state.selected.value,'item-24');assert.equal(state.page.value,3)
  state.selected.value='';assert.equal(state.visible.value[0].key,'item-24')
  state.type.value='scene';assert.equal(state.page.value,1);assert.equal(state.filtered.value.length,116)
  state.page.value=3;state.selected.value='item-48';state.selected.value=''
  assert.equal(state.page.value,3);assert.equal(state.type.value,'scene')
  state.query.value='no matches';assert.equal(state.page.value,1);assert.equal(state.filtered.value.length,0);assert.equal(state.active.value,undefined)
  state.type.value='';state.query.value='';assert.equal(state.filtered.value.length,232)
  state.page.value=20;assert.equal(state.visible.value.length,4)
  state.selected.value='item-231';state.move(1);assert.equal(state.selected.value,'item-231')
 });scope.stop()
})
test('replacement data clamps page and removes stale detail without altering source payload',()=>{
 const scope=effectScope()
 scope.run(()=>{
  const values=ref(Array.from({length:232},(_,i)=>result(i))), state=useResultBrowser(()=>values.value)
  state.page.value=20;state.selected.value='item-231'
  values.value=[result(0)];assert.equal(state.page.value,1);assert.equal(state.selected.value,'');assert.equal(state.visible.value.length,1)
  assert.equal(resultSummary(values.value[0]),'内容 0');assert.equal(values.value[0].payload.description,'内容 0')
  values.value=[];assert.equal(state.pages.value,1);assert.equal(state.visible.value.length,0)
 });scope.stop()
})
