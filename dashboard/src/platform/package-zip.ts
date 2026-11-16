import { rejectPackage } from './package-json.ts'
import casefold from '../generated/package-filename-casefold.json' with { type: 'json' }
export const packageLimits={archive:256*1024*1024,file:64*1024*1024,json:4*1024*1024,entries:1000} as const
const encoder=new TextEncoder(),decoder=new TextDecoder('utf-8',{fatal:true})
export function safePackagePath(path:string):string {
  if(!path||path.length>240||/[\\<>:"|?*\x00-\x1f\x7f]/.test(path)||path.split('/').some(p=>!p||p==='.'||p==='..'||/[. ]$/.test(p)||/^(con|prn|aux|nul|com[1-9]|lpt[1-9])(?:\..*)?$/i.test(p)))rejectPackage('INVALID_PATH')
  return Array.from(path.normalize('NFC'),c=>(casefold as Record<string,string>)[c]??c.toLowerCase()).join('')
}
const crcTable=Uint32Array.from({length:256},(_,n)=>{for(let i=0;i<8;i++)n=n&1?0xedb88320^(n>>>1):n>>>1;return n>>>0})
export function packageCRC(bytes:Uint8Array):number {let n=0xffffffff;for(const b of bytes)n=crcTable[(n^b)&255]^(n>>>8);return (n^0xffffffff)>>>0}
interface ZIPEntry {path:string;flags:number;method:number;crc:number;size:number;compressed:number;offset:number}

/** Bounded, in-memory ZIP reader. No filesystem extraction or code execution. */
export async function readPackageZIP(input:Uint8Array,signal?:AbortSignal):Promise<Map<string,Uint8Array>> {
  if(input.byteLength>packageLimits.archive)rejectPackage('PACKAGE_LIMIT')
  const raw=new Uint8Array(input),view=new DataView(raw.buffer),n=raw.length
  const u16=(p:number)=>view.getUint16(p,true),u32=(p:number)=>view.getUint32(p,true)
  if(n<22||u32(0)!==0x04034b50)rejectPackage('INVALID_ZIP')
  let end=-1
  for(let i=n-22;i>=Math.max(0,n-65557);i--)if(u32(i)===0x06054b50){end=i;break}
  if(end<0)rejectPackage('INVALID_ZIP')
  const count=u16(end+10),centralSize=u32(end+12),centralOffset=u32(end+16)
  if(u16(end+4)||u16(end+6)||u16(end+8)!==count||end+22+u16(end+20)!==n||centralOffset+centralSize!==end)rejectPackage('UNSUPPORTED_ZIP_LAYOUT')
  if(!count||count>packageLimits.entries)rejectPackage('ENTRY_LIMIT')
  const entries:ZIPEntry[]=[],names=new Set<string>();let p=centralOffset,total=0
  for(let i=0;i<count;i++) {
    signal?.throwIfAborted()
    if(p+46>end||u32(p)!==0x02014b50)rejectPackage('INVALID_CENTRAL_DIRECTORY')
    const flags=u16(p+8),method=u16(p+10),crc=u32(p+16),compressed=u32(p+20),size=u32(p+24),nameLength=u16(p+28),extra=u16(p+30),comment=u16(p+32),disk=u16(p+34),attr=u32(p+38),offset=u32(p+42)
    if(p+46+nameLength+extra+comment>end||disk||flags&~0x808||![0,8].includes(method)||((attr>>>16)&0xf000)!==0&&((attr>>>16)&0xf000)!==0x8000)rejectPackage('UNSUPPORTED_ZIP_ENTRY')
    const nameBytes=raw.subarray(p+46,p+46+nameLength)
    if(!(flags&0x800)&&nameBytes.some(v=>v>127))rejectPackage('UNSUPPORTED_FILENAME_ENCODING')
    let path:string;try{path=decoder.decode(nameBytes)}catch{rejectPackage('INVALID_PATH')}
    const normalized=safePackagePath(path!)
    if(names.has(normalized))rejectPackage('DUPLICATE_PATH')
    if([...names].some(v=>normalized.startsWith(v+'/')||v.startsWith(normalized+'/')))rejectPackage('PATH_COLLISION')
    names.add(normalized);total+=size
    if(size>packageLimits.file||total>packageLimits.archive)rejectPackage('EXPANDED_LIMIT')
    entries.push({path:path!,flags,method,crc,compressed,size,offset});p+=46+nameLength+extra+comment
  }
  if(p!==end)rejectPackage('INVALID_CENTRAL_DIRECTORY')
  const ranges:{start:number;end:number}[]=[],result=new Map<string,Uint8Array>()
  for(const e of entries) {
    signal?.throwIfAborted();p=e.offset
    if(p+30>centralOffset||u32(p)!==0x04034b50||u16(p+6)!==e.flags||u16(p+8)!==e.method)rejectPackage('INVALID_LOCAL_HEADER')
    const length=u16(p+26),extra=u16(p+28),start=p+30+length+extra,stop=start+e.compressed
    if(stop>centralOffset||start>stop)rejectPackage('INVALID_LOCAL_HEADER')
    if(decoder.decode(raw.subarray(p+30,p+30+length))!==e.path)rejectPackage('LOCAL_FILENAME_MISMATCH')
    if(!(e.flags&8)&&(u32(p+14)!==e.crc||u32(p+18)!==e.compressed||u32(p+22)!==e.size))rejectPackage('LOCAL_SIZE_MISMATCH')
    let endOfEntry=stop
    if(e.flags&8){let d=stop;if(d+4<=centralOffset&&u32(d)===0x08074b50)d+=4;if(d+12>centralOffset||u32(d)!==e.crc||u32(d+4)!==e.compressed||u32(d+8)!==e.size)rejectPackage('INVALID_DESCRIPTOR');endOfEntry=d+12}
    if(ranges.some(r=>p<r.end&&endOfEntry>r.start))rejectPackage('OVERLAPPING_ENTRIES')
    ranges.push({start:p,end:endOfEntry})
    const compressed=raw.subarray(start,stop);let bytes:Uint8Array
    if(e.method===0)bytes=new Uint8Array(compressed)
    else {
      const stream=new Blob([new Uint8Array(compressed)]).stream().pipeThrough(new DecompressionStream('deflate-raw')),reader=stream.getReader(),parts:Uint8Array[]=[]
      let size=0;const cancel=()=>{void reader.cancel().catch(()=>{})};signal?.addEventListener('abort',cancel,{once:true})
      try{for(;;){const next=await reader.read();signal?.throwIfAborted();if(next.done)break;size+=next.value.length;if(size>e.size)rejectPackage('EXPANDED_LIMIT');parts.push(next.value)}}
      catch(error){await reader.cancel().catch(()=>{});throw error}finally{signal?.removeEventListener('abort',cancel);reader.releaseLock()}
      bytes=new Uint8Array(size);let at=0;for(const part of parts){bytes.set(part,at);at+=part.length}
    }
    if(bytes.length!==e.size||packageCRC(bytes)!==e.crc)rejectPackage('ZIP_INTEGRITY_MISMATCH')
    result.set(e.path,bytes)
  }
  ranges.sort((a,b)=>a.start-b.start)
  if(ranges[0].start!==0||ranges.at(-1)!.end!==centralOffset||ranges.some((r,i)=>i>0&&r.start!==ranges[i-1].end))rejectPackage('UNDECLARED_ZIP_DATA')
  return result
}

/** Deterministic store ZIP: avoids lossy rewrites of authoritative JSON bytes. */
export function writePackageZIP(files:Map<string,Uint8Array>):Uint8Array {
  if(!files.size||files.size>packageLimits.entries)rejectPackage('ENTRY_LIMIT')
  const parts:Uint8Array[]=[],central:Uint8Array[]=[],names=new Set<string>();let offset=0,total=0
  for(const [path,bytes] of files){
    const norm=safePackagePath(path),name=encoder.encode(path);total+=bytes.length
    if(names.has(norm)||[...names].some(v=>norm.startsWith(v+'/')||v.startsWith(norm+'/')))rejectPackage('PATH_COLLISION');names.add(norm)
    if(bytes.length>packageLimits.file||total>packageLimits.archive)rejectPackage('EXPANDED_LIMIT')
    const local=new Uint8Array(30+name.length),view=new DataView(local.buffer),crc=packageCRC(bytes)
    view.setUint32(0,0x04034b50,true);view.setUint16(4,20,true);view.setUint16(6,0x800,true);view.setUint32(14,crc,true);view.setUint32(18,bytes.length,true);view.setUint32(22,bytes.length,true);view.setUint16(26,name.length,true);local.set(name,30)
    const directory=new Uint8Array(46+name.length),d=new DataView(directory.buffer)
    d.setUint32(0,0x02014b50,true);d.setUint16(4,20,true);d.setUint16(6,20,true);d.setUint16(8,0x800,true);d.setUint32(16,crc,true);d.setUint32(20,bytes.length,true);d.setUint32(24,bytes.length,true);d.setUint16(28,name.length,true);d.setUint32(42,offset,true);directory.set(name,46)
    parts.push(local,bytes);central.push(directory);offset+=local.length+bytes.length
  }
  const centralSize=central.reduce((sum,p)=>sum+p.length,0),size=offset+centralSize+22
  if(size>packageLimits.archive)rejectPackage('PACKAGE_LIMIT')
  const end=new Uint8Array(22),e=new DataView(end.buffer);e.setUint32(0,0x06054b50,true);e.setUint16(8,files.size,true);e.setUint16(10,files.size,true);e.setUint32(12,centralSize,true);e.setUint32(16,offset,true)
  const raw=new Uint8Array(size);let at=0;for(const part of [...parts,...central,end]){raw.set(part,at);at+=part.length}return raw
}
