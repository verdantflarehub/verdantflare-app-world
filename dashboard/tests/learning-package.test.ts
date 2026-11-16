import test from 'node:test'
import assert from 'node:assert/strict'
import { deflateRawSync } from 'node:zlib'
import { packageJSON } from '../src/platform/package-json.ts'
import { packageCRC, readPackageZIP, safePackagePath, writePackageZIP } from '../src/platform/package-zip.ts'
import { encodeLearningPackage,inspectLearningPackage,verifyPackageBaseline,type LearningPackageManifest } from '../src/platform/learning-package.ts'
import { bytesHash } from '../src/platform/package-upload.ts'
const id='01900000-0000-7000-8000-000000000001',ref={kind:'world_draft',draft_id:id,revision_id:id,file_id:id,purpose:'learning_source'},encode=(v:unknown)=>new TextEncoder().encode(JSON.stringify(v))
async function fixture(version:1|2=1){
  const doc:Record<string,unknown>={schema_version:version,document_kind:'reference-learning',origin:'manual',sources:[],results:[],coverage:[],relations:[],uses:[]}
  if(version===2)doc.curation={packages:[],assessments:[]}
  const payloads=new Map<string,Uint8Array>([['baseline',encode(doc)],['working',encode(doc)]])
  const entries=await Promise.all((['baseline','working','relations'] as const).map(async role=>({key:role,path:role+'/document.json',source_ref:ref,role,editable:role==='working',mime:'application/json',size_bytes:0,sha256:''})))
  payloads.set('relations',encode({schema_version:1,document_relations:[],entry_refs:Object.fromEntries(entries.map(e=>[e.key,e.source_ref]))}))
  for(const e of entries){const bytes=payloads.get(e.key)!;e.size_bytes=bytes.byteLength;e.sha256=await bytesHash(bytes)}
  const manifest:LearningPackageManifest={schema_version:1,kind:'learning-package',package_id:id,created_at:'2026-11-15T00:00:00Z',root_refs:[ref],entries,scope:'complete',selected_result_keys:[],missing_items:[],relation_file_path:'relations/document.json'}
  return {manifest,payloads,raw:await encodeLearningPackage(manifest,payloads,'Editable copy and immutable source; execution unverified.')}
}
test('central schemas validate exact v1/v2 ZIP round trips',async()=>{
  for(const v of [1,2] as const){const f=await fixture(v),r=await inspectLearningPackage(f.raw);assert.equal(r.baseline.schema_version,v);assert.equal(r.source_trust,'unverified');assert.deepEqual(r.changed_keys,[]);assert.deepEqual(r.files.get('baseline/document.json'),f.payloads.get('baseline'));await verifyPackageBaseline(r,f.payloads.get('baseline')!);await assert.rejects(verifyPackageBaseline(r,encode({other:'source'})))}
})
test('editable copy changes are displayed without changing the baseline',async()=>{
  const f=await fixture(),files=await readPackageZIP(f.raw),doc=JSON.parse(new TextDecoder().decode(files.get('working/document.json')));doc.origin='imported';files.set('working/document.json',encode(doc))
  const value=await inspectLearningPackage(writePackageZIP(files));assert.deepEqual(value.changed_keys,['working']);assert.equal(value.baseline.origin,'manual')
})
test('baseline modifications and closed-schema failures rejected',async()=>{
  const f=await fixture();for(const path of ['baseline/document.json','relations/document.json']){const files=await readPackageZIP(f.raw);files.set(path,encode({fake:'source'}));await assert.rejects(inspectLearningPackage(writePackageZIP(files)))}
  const files=await readPackageZIP(f.raw);files.set('working/document.json',encode({schema_version:1,document_kind:'reference-learning',origin:'manual',sources:[],results:[{key:'invalid-result'}],coverage:[],relations:[],uses:[]}));await assert.rejects(inspectLearningPackage(writePackageZIP(files)))
})
test('strict JSON rejects duplicates including escaped keys, invalid utf8 and depth',()=>{
  for(const s of ['{"a":1,"a":2}','{"a":1,"\\u0061":2}','{"a":NaN}','[1,]','{"a":1e999}', '['.repeat(130)+'0'+']'.repeat(130)])assert.throws(()=>packageJSON(new TextEncoder().encode(s)))
  assert.throws(()=>packageJSON(new Uint8Array([0xff])))
  const v=packageJSON(encode({text:'中文\\"line',list:[true,false,null,-1.25]}));assert.equal((v as any).text,'中文\\"line')
})
test('unsafe and platform-colliding names rejected before unpacking',()=>{
  for(const p of ['../x','/x','a\\b','C:/x','con.txt','a/nul','a/..','a.','a//b'])assert.throws(()=>safePackagePath(p))
  for(const names of [['A','a'],['e\u0301','é'],['straße','STRASSE'],['a','a/b']])assert.throws(()=>writePackageZIP(new Map(names.map(n=>[n,encode(1)]))))
})
test('undeclared entries, corrupt CRC, central count and trailing data rejected',async()=>{
  const f=await fixture(),files=await readPackageZIP(f.raw);files.set('surprise.js',encode('untrusted'));await assert.rejects(inspectLearningPackage(writePackageZIP(files)))
  let raw=new Uint8Array(f.raw);raw[31]^=1;await assert.rejects(readPackageZIP(raw))
  raw=new Uint8Array(f.raw);const view=new DataView(raw.buffer);view.setUint16(raw.length-14,1001,true);view.setUint16(raw.length-12,1001,true);await assert.rejects(readPackageZIP(raw))
  raw=new Uint8Array(f.raw.length+1);raw.set(f.raw);await assert.rejects(readPackageZIP(raw))
})
test('deflate stream is bounded by declared length and checked against CRC',async()=>{
  const bytes=new TextEncoder().encode('some bytes '.repeat(100)),name=new TextEncoder().encode('data'),compressed=deflateRawSync(bytes),crc=packageCRC(bytes)
  const local=new Uint8Array(30+name.length),l=new DataView(local.buffer);l.setUint32(0,0x04034b50,true);l.setUint16(4,20,true);l.setUint16(8,8,true);l.setUint32(14,crc,true);l.setUint32(18,compressed.length,true);l.setUint32(22,bytes.length,true);l.setUint16(26,name.length,true);local.set(name,30)
  const central=new Uint8Array(46+name.length),c=new DataView(central.buffer);c.setUint32(0,0x02014b50,true);c.setUint16(6,20,true);c.setUint16(10,8,true);c.setUint32(16,crc,true);c.setUint32(20,compressed.length,true);c.setUint32(24,bytes.length,true);c.setUint16(28,name.length,true);central.set(name,46)
  const end=new Uint8Array(22),e=new DataView(end.buffer);e.setUint32(0,0x06054b50,true);e.setUint16(8,1,true);e.setUint16(10,1,true);e.setUint32(12,central.length,true);e.setUint32(16,local.length+compressed.length,true)
  const raw=new Uint8Array(local.length+compressed.length+central.length+22);raw.set(local);raw.set(compressed,local.length);raw.set(central,local.length+compressed.length);raw.set(end,raw.length-22)
  assert.deepEqual((await readPackageZIP(raw)).get('data'),bytes)
  new DataView(raw.buffer).setUint32(22,1,true);new DataView(raw.buffer).setUint32(local.length+compressed.length+24,1,true)
  await assert.rejects(readPackageZIP(raw))
})
test('already aborted inspection never proceeds',async()=>{const f=await fixture(),c=new AbortController();c.abort();await assert.rejects(inspectLearningPackage(f.raw,c.signal))})
test('unindexed payload before central directory is refused',async()=>{
  const f=await fixture(),view=new DataView(f.raw.buffer),end=f.raw.length-22,central=view.getUint32(end+16,true),hidden=new TextEncoder().encode('unindexed payload')
  const raw=new Uint8Array(f.raw.length+hidden.length);raw.set(f.raw.subarray(0,central));raw.set(hidden,central);raw.set(f.raw.subarray(central),central+hidden.length)
  new DataView(raw.buffer).setUint32(end+hidden.length+16,central+hidden.length,true)
  await assert.rejects(readPackageZIP(raw),/UNDECLARED_ZIP_DATA/)
})
