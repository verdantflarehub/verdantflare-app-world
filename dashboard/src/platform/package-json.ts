import { Ajv2020 } from 'ajv/dist/2020.js'
import addFormats from 'ajv-formats'
import schemas from '../generated/learning-package-contracts.json' with { type: 'json' }

export class PackageError extends Error { constructor(readonly code: string) { super(code) } }
export const rejectPackage=(code:string):never=>{throw new PackageError(code)}

/** Strict JSON with duplicate-key and depth rejection. Data is never evaluated. */
export function packageJSON(bytes:Uint8Array):unknown {
  if(bytes.byteLength>4*1024*1024)rejectPackage('JSON_LIMIT')
  let text=''
  try{text=new TextDecoder('utf-8',{fatal:true}).decode(bytes)}catch{rejectPackage('INVALID_UTF8')}
  let i=0
  const white=()=>{while(i<text.length&&/[\x20\t\r\n]/.test(text[i]))i++}
  function string():string {
    const start=i++;let escape=false
    while(i<text.length){const c=text[i++];if(!escape&&c==='"'){try{return JSON.parse(text.slice(start,i)) as string}catch{rejectPackage('INVALID_JSON')}}if(!escape&&c==='\\')escape=true;else escape=false}
    return rejectPackage('INVALID_JSON')
  }
  function value(depth:number):unknown {
    if(depth>128)rejectPackage('JSON_DEPTH_LIMIT');white()
    if(text[i]==='"')return string()
    if(text[i]==='{'){
      i++;white();const result:Record<string,unknown>=Object.create(null)
      if(text[i]==='}'){i++;return result}
      for(;;){white();if(text[i]!=='"')rejectPackage('INVALID_JSON');const key=string();if(Object.hasOwn(result,key))rejectPackage('DUPLICATE_JSON_KEY');white();if(text[i++]!==':')rejectPackage('INVALID_JSON');result[key]=value(depth+1);white();const c=text[i++];if(c==='}')return result;if(c!==',')rejectPackage('INVALID_JSON')}
    }
    if(text[i]==='['){i++;white();const result:unknown[]=[];if(text[i]===']'){i++;return result}for(;;){result.push(value(depth+1));white();const c=text[i++];if(c===']')return result;if(c!==',')rejectPackage('INVALID_JSON')}}
    const token=/^(?:true|false|null|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)/.exec(text.slice(i))?.[0]
    if(!token)rejectPackage('INVALID_JSON');i+=token!.length
    const result=JSON.parse(token!) as unknown;if(typeof result==='number'&&!Number.isFinite(result))rejectPackage('INVALID_JSON');return result
  }
  const result=value(0);white();if(i!==text.length)rejectPackage('INVALID_JSON');return result
}

let validator:Ajv2020|undefined
function contracts() {
  if(validator)return validator
  const ajv=new Ajv2020({strict:false,allErrors:false,ownProperties:true})
  addFormats(ajv)
  for(const [id,original] of Object.entries(schemas)){
    const schema=structuredClone(original)
    function normalize(node:unknown):void {
      if(!node||typeof node!=='object')return
      if(Array.isArray(node)){node.forEach(normalize);return}
      const o=node as Record<string,unknown>
      if(typeof o.$ref==='string'&&!o.$ref.startsWith('#')&&!o.$ref.includes('://'))o.$ref=id.slice(0,id.lastIndexOf('/')+1)+o.$ref.replace(/\.schema\.json(?=#|$)/,'')
      Object.values(o).forEach(normalize)
    }
    normalize(schema);ajv.addSchema(schema,id)
  }
  validator=ajv;return ajv
}
export function validatePackageSchema(name:'learning-package'|'reference-learning'|'reference-learning-v2'|'world-draft-request'|'world-draft-response',value:unknown):void {
  const validate=contracts().getSchema('station://contracts/v2/'+name)
  if(!validate||!validate(value))rejectPackage('INVALID_'+name.toUpperCase().replaceAll('-','_'))
}
