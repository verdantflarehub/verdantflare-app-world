import test from 'node:test'
import assert from 'node:assert/strict'
import { effectScope, ref } from 'vue'
import { useResultBrowser, resultSummary } from '../src/platform/result-browser.ts'
import type { LearningResult } from '../src/platform/learning-results.ts'
import { resultReviews, reviewSelection, unassessedReview, fixedMergeLabel } from '../src/platform/result-review.ts'
function result(i:number):LearningResult {return {key:`item-${i}`,title:`成果 ${i}`,type_key:i%2?'shot':'scene',type_version:1,domain:'shot',basis:'observed',basis_note:'来源说明',use_conditions:[],source_refs:[],evidence:[],payload:{description:`内容 ${i}`}}}

test('review filters intersect type and search, reset paging, and never promote legacy results',()=>{
 const scope=effectScope()
 scope.run(()=>{
  const rows=Array.from({length:60},(_,i)=>result(i))
  const states=['unassessed','recommended','needs_review','excluded'] as const
  const state=useResultBrowser(()=>rows,r=>r.key,r=>states[Number(r.key.split('-')[1])%4])
  state.page.value=4;state.selected.value='item-40'
  state.selection.value='excluded'
  assert.equal(state.page.value,1);assert.equal(state.selected.value,'');assert.equal(state.filtered.value.length,15)
  state.type.value='scene';assert.equal(state.filtered.value.length,0)
  state.type.value='shot';state.query.value='item-7';assert.deepEqual(state.filtered.value.map(r=>r.key),['item-7'])
  state.query.value='';state.page.value=2;state.selected.value='item-55';state.selected.value=''
  assert.equal(state.selection.value,'excluded');assert.equal(state.page.value,2)
  const legacy=useResultBrowser(()=>rows);legacy.selection.value='recommended';assert.equal(legacy.filtered.value.length,0)
  legacy.selection.value='unassessed';assert.equal(legacy.filtered.value.length,60)
 });scope.stop()
 assert.equal(reviewSelection('toString'),'');assert.equal(reviewSelection(['recommended']),'')
 assert.equal(resultReviews().size,0);assert.equal(unassessedReview.selection,'unassessed')
 assert.match(fixedMergeLabel({kind:'asset',asset_id:'asset',asset_version_id:'fixed',file_id:'file',object_key:'result'}),/fixed/)
})
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
