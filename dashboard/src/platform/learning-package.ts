import { packageJSON, rejectPackage, validatePackageSchema } from './package-json.ts'
import { packageLimits, readPackageZIP, writePackageZIP } from './package-zip.ts'
import { bytesHash } from './package-upload.ts'
import type { CollectionDocument } from './draft-model.ts'

export interface PackageEntry {key:string;source_ref:Record<string,unknown>;path:string;sha256:string;size_bytes:number;mime:string;role:'baseline'|'working'|'relations'|'reference';editable:boolean}
export interface LearningPackageManifest {schema_version:1;kind:'learning-package';package_id:string;created_at:string;root_refs:Record<string,unknown>[];entries:PackageEntry[];relation_file_path:string;scope:'complete'|'selected';selected_result_keys:string[];missing_items:{source_ref:Record<string,unknown>;required:boolean;reason:'not_included'|'unavailable'|'unsupported'|'over_limit'}[]}
export interface PackageInspection {manifest:LearningPackageManifest;package_sha256:string;files:Map<string,Uint8Array>;baseline:CollectionDocument;working:CollectionDocument;changed_keys:string[];source_trust:'unverified';expanded_bytes:number}
export function packageCanonical(value:unknown):string {
  if(Array.isArray(value))return '['+value.map(packageCanonical).join(',')+']'
  if(value&&typeof value==='object')return '{'+Object.keys(value).sort().map(k=>JSON.stringify(k)+':'+packageCanonical((value as Record<string,unknown>)[k])).join(',')+'}'
  return JSON.stringify(value)
}
function learningDocument(bytes:Uint8Array):CollectionDocument {
  const raw=packageJSON(bytes) as Record<string,unknown>
  if(!raw||typeof raw!=='object'||![1,2].includes(raw.schema_version as number))rejectPackage('UNSUPPORTED_DOCUMENT_VERSION')
  validatePackageSchema(raw.schema_version===2?'reference-learning-v2':'reference-learning',raw)
  return raw as CollectionDocument
}
export async function inspectLearningPackage(bytes:Uint8Array,signal?:AbortSignal):Promise<PackageInspection> {
  const files=await readPackageZIP(bytes,signal)
  const read=(path:string,limit=packageLimits.json):Uint8Array=>{const raw=files.get(path);if(!raw||raw.byteLength>limit)return rejectPackage('MISSING_OR_OVERSIZED_ENTRY');return raw}
  const raw=packageJSON(read('manifest.json'));validatePackageSchema('learning-package',raw)
  const manifest=raw as LearningPackageManifest,entries=manifest.entries,paths=new Set(entries.map(e=>e.path)),keys=new Set(entries.map(e=>e.key))
  if(paths.size!==entries.length||keys.size!==entries.length||paths.has('manifest.json')||paths.has('README.txt')||files.size!==paths.size+2||[...files.keys()].some(k=>k!=='manifest.json'&&k!=='README.txt'&&!paths.has(k)))rejectPackage('UNDECLARED_OR_DUPLICATE_ENTRY')
  try{new TextDecoder('utf-8',{fatal:true}).decode(read('README.txt'))}catch{rejectPackage('INVALID_README')}
  const one=(role:PackageEntry['role'])=>{const list=entries.filter(e=>e.role===role);if(list.length!==1)return rejectPackage('INVALID_DOCUMENT_ROLES');return list[0]}
  const baseline=one('baseline'),working=one('working'),relations=one('relations'),canonical=packageCanonical
  if(canonical(baseline.source_ref)!==canonical(working.source_ref)||!manifest.root_refs.some(r=>canonical(r)===canonical(baseline.source_ref)))rejectPackage('BASELINE_IDENTITY_MISMATCH')
  if(relations.path!==manifest.relation_file_path)rejectPackage('RELATION_PATH_MISMATCH')
  const changed:string[]=[]
  for(const entry of entries){
    signal?.throwIfAborted()
    if(entry.editable!==(entry.role==='working'))rejectPackage('INVALID_EDITABLE_ENTRY')
    const body=read(entry.path,entry.role==='reference'?packageLimits.file:packageLimits.json)
    const same=body.byteLength===entry.size_bytes&&await bytesHash(body)===entry.sha256
    if(!same&&!entry.editable)rejectPackage('BASELINE_OR_FILE_CHANGED')
    if(!same)changed.push(entry.key)
  }
  const original=learningDocument(read(baseline.path)),edited=learningDocument(read(working.path))
  if(original.schema_version!==edited.schema_version)rejectPackage('DOCUMENT_VERSION_CHANGED')
  const resultKeys=original.results.map(r=>String((r as Record<string,unknown>).key)),selected=manifest.selected_result_keys
  if(new Set(resultKeys).size!==resultKeys.length||selected.some(k=>!resultKeys.includes(k))||manifest.scope==='complete'&&(selected.length!==resultKeys.length||resultKeys.some(k=>!selected.includes(k)))||manifest.scope==='selected'&&!selected.length)rejectPackage('INVALID_SELECTION')
  const links=packageJSON(read(relations.path)) as Record<string,unknown>
  if(!links||typeof links!=='object'||Object.keys(links).sort().join(',')!=='document_relations,entry_refs,schema_version'||links.schema_version!==1)rejectPackage('INVALID_RELATIONS')
  if(canonical(links.document_relations)!==canonical(original.relations))rejectPackage('RELATIONS_DIFFER_FROM_BASELINE')
  if(canonical(links.entry_refs)!==canonical(Object.fromEntries(entries.map(e=>[e.key,e.source_ref]))))rejectPackage('RELATION_MAPPING_NOT_CLOSED')
  return {manifest,package_sha256:await bytesHash(bytes),files,baseline:original,working:edited,changed_keys:changed,source_trust:'unverified',expanded_bytes:[...files.values()].reduce((s,b)=>s+b.byteLength,0)}
}

/** Lower-level byte writer. Source reads and permission checks belong to the
 * exporter, not to untrusted fields in this manifest. */
export async function encodeLearningPackage(manifest:LearningPackageManifest,payloads:Map<string,Uint8Array>,readme:string):Promise<Uint8Array> {
  const files=new Map<string,Uint8Array>(),encoder=new TextEncoder()
  validatePackageSchema('learning-package',manifest)
  if(payloads.size!==manifest.entries.length||manifest.entries.some(e=>!payloads.has(e.key)))rejectPackage('ENTRY_SET_MISMATCH')
  files.set('manifest.json',encoder.encode(JSON.stringify(manifest)));files.set('README.txt',encoder.encode(readme))
  for(const entry of manifest.entries)files.set(entry.path,payloads.get(entry.key)!)
  const zip=writePackageZIP(files);await inspectLearningPackage(zip);return zip
}

export async function verifyPackageBaseline(value:PackageInspection,authorizedBytes:Uint8Array):Promise<void> {
  const baseline=value.manifest.entries.find(e=>e.role==='baseline')!
  if(authorizedBytes.byteLength!==baseline.size_bytes||await bytesHash(authorizedBytes)!==baseline.sha256)rejectPackage('SOURCE_BASELINE_MISMATCH')
}
