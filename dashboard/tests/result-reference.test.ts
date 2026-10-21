import test from 'node:test'
import assert from 'node:assert/strict'
import { resultReference } from '../src/platform/result-reference.ts'
import { resultReading, readingText } from '../src/platform/result-reading.ts'
import type { LearningDocument, LearningResult } from '../src/platform/learning-results.ts'
function fixture(kind='character') {
 const result={key:'person',type_key:kind,type_version:1,title:'人物参考',basis_note:'观察说明',basis:'observed',domain:'character',payload:{appearance:'黑色长发'},source_refs:[{kind:'source',key:'original'},{kind:'source',key:'evidence'}],evidence:[{source_ref:{kind:'source',key:'evidence'},locator:{kind:'lines',start_line:2,end_line:2}}],use_conditions:[]} as LearningResult
 const rows=[{kind:'analysis_context',value:{draft_id:'draft',revision_id:'fixed',file_id:'video',source:{duration_ms:10000,sha256:'a'.repeat(64)}}},
 {kind:'result_candidate',value:{key:'person',type_key:kind,evidence_keys:['entity']}},
 {kind:'observation',value:{key:'entity',kind:kind==='character'?'entities':'speakers',links:{},value:{description:'人物'}}},
 {kind:'observation',value:{key:'shot',kind:kind==='character'?'shots':'speech',scope:{start_ms:1000,end_ms:2000},links:kind==='character'?{entities:['entity']}:{speakers:['entity']},value:{description:'对应片段'}}},
 {kind:'observation',value:{key:'other',kind:'shots',scope:{start_ms:3000,end_ms:4000},links:{entities:['another']},value:{description:'标题相似但不相关'}}}]
 const doc={schema_version:1,document_kind:'reference-learning',origin:'generated',sources:[{key:'original',title:'原片',role:'original',media_kind:'video',acquisition_state:'ready',content:{kind:'world_draft',draft_id:'draft',revision_id:'fixed',file_id:'video'}},{key:'evidence',title:'证据',content:{kind:'inline',text:rows.map(r=>JSON.stringify(r)).join('\n')}}],results:[result],coverage:[],relations:[],uses:[]} as unknown as LearningDocument
 return {result,doc,rows,update:()=>{doc.sources[1].content={kind:'inline',text:rows.map(r=>JSON.stringify(r)).join('\n')}}}
}
test('visual and voice previews resolve explicit membership, not broad or similar-title ranges',()=>{
 for(const kind of ['character','sound_cue']) {const {result,doc}=fixture(kind);result.payload.scope={kind:'time',start_tick:0,end_tick:10000};assert.deepEqual(resultReference(result,doc)?.ranges,[{start:1,end:2,note:'对应片段'}])}
})
test('missing, ambiguous, uncited and out-of-bounds evidence never invents media',()=>{
 for(const edit of [
  (f:ReturnType<typeof fixture>)=>{f.result.evidence=[]},
  (f:ReturnType<typeof fixture>)=>{f.rows[1].value.key='different'},
  (f:ReturnType<typeof fixture>)=>{f.rows[3].value.scope!.end_ms=11000},
  (f:ReturnType<typeof fixture>)=>{f.rows[3].value.scope!.start_ms=-1},
  (f:ReturnType<typeof fixture>)=>{f.doc.sources[0].content={kind:'world_draft',draft_id:'draft',revision_id:'latest',file_id:'video'}},
  (f:ReturnType<typeof fixture>)=>{f.rows.push(f.rows[1])},
  (f:ReturnType<typeof fixture>)=>{f.rows.push(f.rows[3])},
 ]){const f=fixture();edit(f);f.update();assert.equal(resultReference(f.result,f.doc),null)}
})
test('business copy contains readable instructions, not internal step IDs or placeholder prose',()=>{
 const {result}=fixture();result.payload.costume='原片镜头内可见的服装款式、色彩与配饰特征。'
 assert(!readingText(result).includes('原片镜头内可见'))
 result.type_key='method';result.payload={objective:'用动作连接回忆',steps:[{key:'step_internal_123',input_keys:['private_ref'],instruction:'用相同动作衔接两个时空。'}],inputs:[{key:'internal',description:'准备核心道具。'}]}
 assert.match(readingText(result),/用相同动作/);assert(!readingText(result).includes('step_internal_123'));assert(!readingText(result).includes('private_ref'))
 assert.match(resultReading(result).limitation,/尚不等于/)
 result.type_key='sound_cue';assert.match(resultReading(result).limitation,/混合音轨/)
})

test('human category numbers stay stable under display sorting and MCP copy carries exact version and key',async()=>{
 const {result,doc}=fixture(),{resultCode,agentReference,businessOrder}=await import('../src/platform/result-locator.ts')
 const shot={...result,key:'shot-unique',type_key:'shot'},second={...result,key:'second-person'}
 doc.results=[shot,result,second]
 assert.equal(resultCode(result,doc),'人物-001');assert.equal(resultCode(second,doc),'人物-002')
 assert.equal(businessOrder(doc.results)[0].key,result.key);assert.equal(doc.results[0].key,shot.key)
 const instruction=agentReference(second,doc,'世界学习记录 fixed-draft / 修订 fixed-revision')
 assert.match(instruction,/fixed-revision/);assert.match(instruction,/second-person/);assert.match(instruction,/world.draft/);assert.match(instruction,/编号仅在这份固定文档内有效/)
 const {useResultBrowser}=await import('../src/platform/result-browser.ts'),browser=useResultBrowser(()=>businessOrder(doc.results),r=>resultCode(r,doc))
 browser.query.value='人物-002';assert.deepEqual(browser.filtered.value.map(r=>r.key),['second-person'])
})
