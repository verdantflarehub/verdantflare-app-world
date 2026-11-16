import test from 'node:test'
import assert from 'node:assert/strict'
import { exportLearningPackage } from '../src/platform/package-export.ts'
import { inspectLearningPackage } from '../src/platform/learning-package.ts'
import { bytesHash } from '../src/platform/package-upload.ts'
const id='01900000-0000-7000-8000-000000000001',other='01900000-0000-7000-8000-000000000002',third='01900000-0000-7000-8000-000000000003'
const root={kind:'world_draft',draft_id:id,revision_id:id,file_id:id,purpose:'learning_source'}
function fixture(){
  const source=(key:string,file_id:string)=>({key,title:key,media_kind:'image',role:'original',acquisition_state:'ready',content:{kind:'file',file_id}})
  const result=(key:string)=>({key:'style-'+key,title:key,domain:'knowledge',type_key:'style',type_version:1,basis:'created',basis_note:'Synthetic style',source_refs:[{kind:'source',key}],evidence:[],use_conditions:[{mode:'reference',availability:'unknown',required_inputs:[],missing_items:[],purpose:'reference'}],payload:{scope_note:'Visual style',rules:[{key:'rule',domain:'knowledge',instruction:'Use muted colors',evidence_refs:[]}],positive_examples:[],counterexamples:[]}})
  const document={schema_version:1,document_kind:'reference-learning',origin:'manual',sources:[source('a',other),source('b',third)],results:[result('a'),result('b')],coverage:[],relations:[],uses:[]}
  const original=new TextEncoder().encode(JSON.stringify(document)),calls:any[]=[],data=new Uint8Array([1,2,3]);let denied=false
  const reader={read:async(reference:any)=>{calls.push(reference);if(denied&&reference.file_id!==id)throw Error('private path must not appear');const bytes=reference.file_id===id?original:data;return {fixed:{kind:'reference' as const,reference,content_ref:{store_id:id,artifact_id:id,version_id:id},mime:reference.file_id===id?'application/json':'image/png',size:bytes.length,sha256:await bytesHash(bytes)},bytes}}}
  return {reader,original,calls,deny:()=>{denied=true}}
}
test('selected export preserves full original and reads only selected dependency closure',async()=>{
  const f=fixture(),value=await exportLearningPackage(f.reader,root,['style-a'],true),checked=await inspectLearningPackage(value.bytes)
  assert.equal(checked.baseline.results.length,2);assert.deepEqual(checked.files.get('baseline/document.json'),f.original)
  assert.deepEqual(value.manifest.selected_result_keys,['style-a']);assert.equal(value.reference_files,1);assert.deepEqual(f.calls.map(r=>r.file_id),[id,other]);assert.equal(value.manifest.missing_items.length,0)
})
test('complete export includes both references and exact original working copy',async()=>{
  const f=fixture(),value=await exportLearningPackage(f.reader,root,null,true),checked=await inspectLearningPackage(value.bytes)
  assert.equal(value.results,2);assert.equal(value.reference_files,2);assert.deepEqual(checked.files.get('working/document.json'),f.original);assert.deepEqual(checked.changed_keys,[])
})
test('unreadable dependencies remain explicit without leaking underlying failure details',async()=>{
  const f=fixture();f.deny();const value=await exportLearningPackage(f.reader,root,['style-a'],true)
  assert.equal(value.reference_files,0);assert.equal(value.manifest.missing_items.length,1);assert.equal(value.manifest.missing_items[0].required,true);assert(!JSON.stringify(value.manifest).includes('private path'))
})
test('explicit metadata-only export does not fetch references and abort never returns partial package',async()=>{
  const f=fixture(),value=await exportLearningPackage(f.reader,root,null,false)
  assert.equal(f.calls.length,1);assert.equal(value.manifest.missing_items.length,2)
  const controller=new AbortController();controller.abort();await assert.rejects(exportLearningPackage(f.reader,root,null,true,controller.signal));assert.equal(f.calls.length,1)
})
