import test from 'node:test'
import assert from 'node:assert/strict'
import { preparePackageImport, mapPackageImport, packageTypedObjects } from '../src/platform/package-import.ts'
import type { PackageInspection } from '../src/platform/learning-package.ts'
import { validateImportedDocument, validateImportedBindings } from '../src/platform/package-semantics.ts'
import { packageCanonical } from '../src/platform/learning-package.ts'

const id = '01900000-0000-7000-8000-000000000001'
const fixed = {kind:'world_draft',draft_id:id,revision_id:id,file_id:id,purpose:'learning_source'}
function result(key: string, parent?: string) {
  return {key,...(parent?{parent_key:parent}:{}),title:key,domain:'knowledge',type_key:'prompt',type_version:1,basis:'created',basis_note:'draft',source_refs:[],evidence:[],use_conditions:[{mode:'reference',availability:'ready',required_inputs:[],missing_items:[],purpose:'reference'}],payload:{creative_intent:'intent',variables:[],variants:[{key:'v',revision:1,requested_label:'model',task_type:'image',input_mode:'text',prompt_text:'prompt',parameters:{kind:'result',key:'opaque'},reference_bindings:[],compatibility:'draft',execution_evidence:[],quality_review:{state:'accepted',note:'old approval'}}]}}
}
function fixture(v:1|2=1): PackageInspection {
  const working:any={schema_version:v,document_kind:'reference-learning',origin:'manual',sources:[],results:[result('parent'),result('child','parent'),result('unselected')],coverage:[],relations:[],uses:[]}
  if(v===2)working.curation={packages:[{key:'group',purpose:'group',result_keys:['child','unselected']}],assessments:working.results.map((r:any)=>({result_key:r.key,package_keys:r.key==='parent'?[]:['group'],selection:'unassessed',reason_codes:['pending_review'],note:'old',evidence_refs:[],merged_from_refs:[],previews:[]}))}
  return {working,baseline:structuredClone(working),manifest:{scope:'selected',selected_result_keys:['child']}} as PackageInspection
}
test('selected import adds parent dependency without importing unrelated results or mutating baseline',()=>{
  for(const version of [1,2] as const){const f=fixture(version),original=structuredClone(f);const p=preparePackageImport(f)
    assert.deepEqual(p.document.results.map((r:any)=>r.key),['parent','child']);assert.deepEqual(p.dependency_keys,['parent'])
    assert.equal(p.document.origin,'imported');assert.deepEqual(f,original)
    const variant=(p.document.results[1] as any).payload.variants[0]
    assert.equal(variant.quality_review.state,'unreviewed');assert.equal(variant.compatibility,'unresolved');assert.deepEqual(variant.parameters,{kind:'result',key:'opaque'})
    assert.equal((p.document.results[1] as any).use_conditions[0].availability,'unknown')
    if(version===2)assert.deepEqual((p.document.curation as any).packages[0].result_keys,['child'])
  }
})
test('missing selected result, missing parent and parent cycle reject preparation',()=>{
  for(const mutate of [(f:any)=>{f.manifest.selected_result_keys=['absent']},(f:any)=>{f.working.results[1].parent_key='absent'},(f:any)=>{f.working.results[0].parent_key='child'}]){
    const f=fixture();mutate(f);assert.throws(()=>preparePackageImport(f))
  }
})
test('external UUIDs stay pending until explicit mapping and all mappings must close',()=>{
  const f=fixture();(f.working.results[1] as any).source_refs=[fixed]
  const p=preparePackageImport(f);assert.deepEqual(p.required_refs,[fixed])
  assert.throws(()=>mapPackageImport(p,[]));assert.throws(()=>mapPackageImport(p,[{source:fixed,state:'pending'}]))
  const target={kind:'file',file_id:id};const saved=mapPackageImport(p,[{source:fixed,state:'imported',target}])
  assert.deepEqual((saved.results[1] as any).source_refs,[target]);assert.deepEqual((p.document.results[1] as any).source_refs,[fixed])
  assert.throws(()=>mapPackageImport(p,[{source:fixed,state:'reused',target},{source:fixed,state:'reused',target}]))
})
test('v2 retains merge lineage but resets preview success and review',()=>{
  const f=fixture(2),a=(f.working.curation as any).assessments[1]
  a.merged_from_refs=[{...fixed,object_key:'old-child'}]
  a.previews=[{key:'preview',state:'ready',role:'original_reference',media_kind:'image',note:'old',file_ref:fixed,locator:{kind:'whole'},sha256:'a'.repeat(64)}]
  const p=preparePackageImport(f),actual=(p.document.curation as any).assessments[1]
  assert.deepEqual(actual.merged_from_refs,a.merged_from_refs);assert.equal(actual.previews[0].state,'missing');assert.equal(actual.previews[0].file_ref,undefined)
  assert.deepEqual(p.required_refs,a.merged_from_refs)
})
test('typed references include ref defaults but exclude opaque parameters and ordinary defaults',()=>{
  const nodes=[...packageTypedObjects({parameters:{kind:'file'},a:{value_type:'ref',default:{kind:'file',file_id:id}},b:{value_type:'text',default:{kind:'file'}}})]
  assert.equal(nodes.filter(n=>n.kind==='file').length,1)
})

test('normalized v1/v2 pass semantics but duplicate variants, missing files and v2 membership fail',()=>{
  for(const v of [1,2] as const){const p=preparePackageImport(fixture(v));validateImportedDocument(p.document,[])}
  const p=preparePackageImport(fixture());const bad=structuredClone(p.document)
  const variants=(bad.results[0] as any).payload.variants;variants.push(structuredClone(variants[0]));assert.throws(()=>validateImportedDocument(bad,[]),/VARIANT_KEYS/)
  const missing=structuredClone(p.document);(missing.results[0] as any).source_refs=[{kind:'file',file_id:id}];assert.throws(()=>validateImportedDocument(missing,[]),/REFERENCE/)
  const v2=preparePackageImport(fixture(2)).document;(v2.curation as any).assessments[1].package_keys=[];assert.throws(()=>validateImportedDocument(v2,[]),/PACKAGE_MEMBERSHIP/)
})
test('method ordering and output ownership are checked before saving',()=>{
  const p=preparePackageImport(fixture()).document,method=p.results[0] as any
  method.type_key='method';method.payload={purpose:'creative_reuse',objective:'Explain',applicability:'Visible process',inputs:[{key:'subject',value_type:'string',required:true,description:'Subject'}],steps:[{key:'plan',instruction:'Write plan',input_keys:['subject'],output_keys:['plan']}],constraints:[],expected_outputs:[{key:'plan',type_key:'shot',description:'Shot plan',checks:[]}],counterexamples:[]}
  validateImportedDocument(p,[])
  method.payload.steps[0].input_keys=['future'];assert.throws(()=>validateImportedDocument(p,[]),/FUTURE_INPUT/)
  method.payload.steps[0].input_keys=['subject'];method.payload.steps[0].output_keys=['subject'];assert.throws(()=>validateImportedDocument(p,[]),/OUTPUT_OVERWRITE/)
})
test('merged external objects require freshly resolved matching type and original provenance',()=>{
  const f=fixture(2),a=(f.working.curation as any).assessments[1],ref={...fixed,object_key:'original-child'}
  a.merged_from_refs=[ref]
  const doc=preparePackageImport(f).document,old=result('original-child') as any
  old.source_refs=[{kind:'source',key:'original-source'}]
  const objects=new Map([[packageCanonical(ref),old]])
  assert.throws(()=>validateImportedBindings(doc,new Map()),/UNRESOLVED_FIXED_OBJECT/)
  assert.throws(()=>validateImportedBindings(doc,objects),/MERGE_PROVENANCE_DROPPED/)
  ;(doc.results[1] as any).source_refs=[{...fixed,object_key:'original-source'}]
  validateImportedBindings(doc,objects)
  old.type_key='method';assert.throws(()=>validateImportedBindings(doc,objects),/MERGE_TYPE/)
})
