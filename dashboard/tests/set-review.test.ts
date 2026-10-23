import test from 'node:test'
import assert from 'node:assert/strict'
import { canonicalJSON, compareEvidence, createReviewClient, fullReviewEligible, mappedReviewOptions, parseReviewPath, readReview, resolveReviewScope, reviewPath, reviewRequestHash, reviewViewerTarget, type Evidence, type ReviewLocator, type ReviewOpen, type ReviewScope, type TargetView } from '../src/platform/set-review.ts'
import type { AppSDK, Context, Json, Lifecycle } from '../src/platform/types.ts'

const id = (n: number) => `01900000-0000-7000-8000-${String(n).padStart(12,'0')}`
const hash = 'a'.repeat(64)
const scope: ReviewScope = { kind:'evidence', evidence_ids:['render'], camera_ids:[], region_ids:[], claims:['image_reading'] }
function locator(domain: 'project'|'world'): ReviewLocator { return domain === 'project' ? { kind:domain, project_id:id(1), content_revision_id:id(2), material_key:'street', state_key:'day', version_key:'v1' } : { kind:domain, asset_id:id(1), asset_version_id:id(2) } }
function evidence(name='render', n=3): Evidence { return { evidence_id:name, file_id:id(n), content_ref:{store_id:id(8),artifact_id:id(n),version_id:id(n+10)}, sha256:String(n%10).repeat(64), size_bytes:20, mime:'image/png', role:'render',camera_ids:['north'],region_ids:['courtyard'],frame_range:null } }
function page(l: ReviewLocator): ReviewOpen { return { target_locator:l,target_hash:hash,target_view:{schema_version:1,target_locator:l,definition:{},source_manifest:{},evidence:[evidence()],dependencies:[],coverage:{camera_ids:['north'],region_ids:[]},requirements:[]},availability:[],missing:[],records_head_record_id:null,review_state:'unreviewed',scope_decisions:[],records:[],next_cursor:null,capabilities:{can_comment:true,can_decide:true,reason_codes:[]},...(l.kind==='project'?{head_revision_id:id(2),records_revision_id:id(2)}:{}) } }
function fixture(domain: 'project'|'world') {
  const l=locator(domain), values=new Map<string,string>(), contexts=new Set<(v:Context)=>void>(), lifecycle=new Set<(v:Lifecycle)=>void>(), calls: {path:string;body:Json;signal?:AbortSignal}[]=[]
  let context:Context={userId:'test-user',organizationId:'test-org',stationId:'test-station',appId:domain,installationId:'test-installation',contextRevision:'grant-1',projectId:null}
  let handle: (input:{path:string;body:Json})=>Promise<{status:number;data:Json;requestId:string}>=async()=>({status:200,data:page(l) as unknown as Json,requestId:'test'})
  const sdk:AppSDK={version:'1.1.0',context:{get:()=>context,subscribe:fn=>{contexts.add(fn);return()=>contexts.delete(fn)}},lifecycle:{subscribe:fn=>{lifecycle.add(fn);return()=>lifecycle.delete(fn)}},theme:{get:()=> 'light',subscribe:()=>()=>{}},navigation:{get:()=>({path:'/',revision:1}),subscribe:()=>()=>{},request:async()=>{}},capabilities:{call:async()=>{throw Error('unused')}},app:{request:async(input,options)=>{calls.push({...input,signal:options?.signal});return handle(input)}}}
  const storage={getItem:(key:string)=>values.get(key)||null,setItem:(key:string,value:string)=>{values.set(key,value)},removeItem:(key:string)=>{values.delete(key)}}
  return {l,values,calls,sdk,storage,handle:(fn:typeof handle)=>{handle=fn},emit:(event:Lifecycle)=>{for(const fn of lifecycle)fn(event)},change:()=>{context={...context,organizationId:'other-org'};for(const fn of contexts)fn(context)}}
}
for(const domain of ['project','world'] as const) {
  test(`${domain}: fixed routes survive roundtrip and reject duplicate, foreign and mutable locators`,()=>{
    const l=locator(domain), path=reviewPath(l,hash)
    assert.deepEqual(parseReviewPath(path),{locator:l,hash})
    for(const bad of [path+'&target='+hash,path+'&actor=forged','https://host.invalid'+path,path.replace(id(2),'latest')])assert.equal(parseReviewPath(bad),undefined)
  })
  test(`${domain}: original request survives lost reply, refresh, status and verified readback`,async()=>{
    const f=fixture(domain), first=createReviewClient(f.sdk,domain,f.l,f.storage)
    await first.begin(page(f.l),'comment',scope,'  cafe\u0301  ')
    const original=first.pending()!
    assert.equal(f.calls.length,0);assert.equal(original.request.body,'café')
    assert.equal(original.requestHash,await reviewRequestHash(original.request))
    assert.equal('actor' in original.request,false);assert.equal('supersedes_record_id' in original.request,false)
    f.handle(async()=>{assert.equal(f.values.size,1);throw Error('lost reply')})
    await assert.rejects(first.submit());first.dispose()
    const second=createReviewClient(f.sdk,domain,f.l,f.storage)
    const result={state:'committed',commit_id:original.request.commit_id,record_id:id(9),target_hash:hash,review_state:'in_review',records_head_record_id:id(9)}
    f.handle(async input=>({status:200,data:(input.path.endsWith('/status')?{state:'committed',request_sha256:original.requestHash,result}:result) as Json,requestId:'test'}))
    assert.deepEqual((await second.status()).result,result)
    await second.submit();assert.deepEqual(f.calls.filter(c=>c.path.endsWith('/append')).map(c=>c.body),[original.request,original.request])
    await assert.rejects(second.readback(result as never));assert.ok(second.pending())
    f.handle(async()=>({status:200,data:{...page(f.l),records_head_record_id:id(9),records:[{record_id:id(9),target_hash:hash,action:'comment',scope,body:'café'}]} as unknown as Json,requestId:'test'}))
    assert.equal((await second.readback(result as never)).records[0].record_id,id(9));assert.equal(second.pending(),undefined);second.dispose()
  })
  test(`${domain}: fingerprint mismatch and storage failure cannot dispatch or clear a request`,async()=>{
    const f=fixture(domain),c=createReviewClient(f.sdk,domain,f.l,f.storage)
    await c.begin(page(f.l),'comment',scope,'original')
    f.handle(async()=>({status:200,data:{state:'rejected',request_sha256:'b'.repeat(64)},requestId:'test'}))
    await assert.rejects(c.status(),/INVALID_RESPONSE/);assert.throws(()=>c.clearRejected(),/STATE_UNKNOWN/);assert.ok(c.pending());c.dispose()
    const blocked=createReviewClient(f.sdk,domain,f.l,{...f.storage,getItem:()=>null,setItem:()=>{throw Error('quota')}})
    await assert.rejects(blocked.begin(page(f.l),'comment',scope,'not sent'),/STORAGE_UNAVAILABLE/);assert.equal(f.calls.length,1);blocked.dispose()
  })
  test(`${domain}: hidden or changed identities reject late reads and keep durable recovery isolated`,async()=>{
    const f=fixture(domain),c=createReviewClient(f.sdk,domain,f.l,f.storage)
    await c.begin(page(f.l),'comment',scope,'pending')
    let resolve!:(v:{status:number;data:Json;requestId:string})=>void
    f.handle(()=>new Promise(done=>{resolve=done}))
    const pending=c.open();f.emit('hidden');assert.equal(f.calls[0].signal?.aborted,true)
    resolve({status:200,data:page(f.l) as unknown as Json,requestId:'late'})
    await assert.rejects(pending,/SESSION_REVOKED/);f.emit('active');f.change();await assert.rejects(c.submit(),/SESSION_REVOKED/)
    const other=createReviewClient(f.sdk,domain,f.l,f.storage);assert.equal(other.pending(),undefined);assert.equal(f.values.size,1);other.dispose();c.dispose()
  })
  test(`${domain}: legacy, foreign and restricted targets cannot gain write authority`,async()=>{
    const f=fixture(domain),p=page(f.l),c=createReviewClient(f.sdk,domain,f.l,f.storage)
    assert.throws(()=>readReview({...p,target_hash:null},f.l),/INVALID_RESPONSE/)
    await assert.rejects(c.begin({...p,target_hash:null,capabilities:{can_comment:false,can_decide:false,reason_codes:[]}},'comment',scope,'blocked'),/PERMISSION_DENIED/)
    const other=locator(domain);if(other.kind==='project')other.project_id=id(7);else other.asset_id=id(7)
    await assert.rejects(c.begin(page(other),'comment',scope,'foreign'),/INVALID_RESPONSE/)
    assert.throws(()=>readReview({...p,target_view:{...p.target_view,target_locator:other}},f.l),/INVALID_RESPONSE/)
    const restricted={...p,capabilities:{can_comment:false,can_decide:false,reason_codes:['RESTRICTED']},records:[{record_id:id(9),visibility:'restricted',record_view:null}]}
    assert.equal(readReview(restricted,f.l).records.length,1)
    assert.throws(()=>readReview({...restricted,records:[{...restricted.records[0],body:'leaked'}]},f.l),/INVALID_RESPONSE/);assert.equal(f.calls.length,0);c.dispose()
  })
}
test('camera comparison requires actual distinct evidence and verified fixed spatial mappings',()=>{
  const a=page(locator('project')).target_view!,b=structuredClone(a),x=evidence(),y=evidence('alternate',4),basis=evidence('basis',5),params=evidence('parameters',6)
  function configure(t:TargetView,e:Evidence){t.evidence=[e,basis,params];t.coverage={camera_ids:['north'],region_ids:[],space_basis:{evidence_id:basis.evidence_id,sha256:basis.sha256},cameras:[{camera_id:'north',parameters_evidence_id:params.evidence_id,parameters_sha256:params.sha256,evidence_ids:[e.evidence_id],region_ids:[],verified:true}]}}
  configure(a,x);configure(b,y);const size:[{width:number;height:number},{width:number;height:number}]=[{width:1920,height:1080},{width:1920,height:1080}]
  assert.deepEqual(compareEvidence(a,x,b,y,size,'north'),[])
  for(const change of ['unverified','basis','parameters','frame','dimensions','same']){
    const next=structuredClone(b),other=next.evidence[0],dims=structuredClone(size)
    if(change==='unverified')(next.coverage.cameras as {verified:boolean}[])[0].verified=false
    if(change==='basis')(next.coverage.space_basis as {sha256:string}).sha256='9'.repeat(64)
    if(change==='parameters')(next.coverage.cameras as {parameters_sha256:string}[])[0].parameters_sha256='9'.repeat(64)
    if(change==='frame')other.frame_range={start:0,end:1,fps_numerator:24,fps_denominator:1}
    if(change==='dimensions')dims[1].width=1280
    if(change==='same')other.sha256=x.sha256
    assert.ok(compareEvidence(a,x,next,other,dims,'north').length,change)
  }
})
test('JCS keeps UTF-16 property ordering and exact JSON numeric values',()=>{assert.equal(canonicalJSON({z:1e30,a:1e-7,'😀':4,'\uE000':3}),'{'+'"a":1e-7,"z":1e+30,"😀":4,"\uE000":3}')})
test('viewer handoff pins the owning revision and package without installation, latest or return URL fields',()=>{
  for(const domain of ['project','world'] as const){
    const result=reviewViewerTarget(locator(domain),{schema_version:'1.0',package_id:'fixture-set-v1'})
    assert.equal(result.appId,'viewer')
    assert.deepEqual(Object.keys(result),['appId','path'])
    const path=new URL(result.path,'https://app.invalid')
    assert.equal(path.pathname,`/${domain}/${id(1)}`)
    assert.equal(path.searchParams.get(domain==='project'?'revision':'version'),id(2))
    assert.equal(path.searchParams.get('pkg'),'fixture-set-v1')
    assert.equal(path.hash,'')
    for(const package_id of ['','../escape','wrong key','https://elsewhere.invalid'])assert.throws(()=>reviewViewerTarget(locator(domain),{schema_version:'1.0',package_id}),/INVALID_RESPONSE/)
  }
})

for (const domain of ['project','world'] as const) {
  for (const kind of ['camera','region'] as const) {
    test(`${domain}: ${kind} scope requires registered IDs and actual readable image mappings`, () => {
      const p = page(locator(domain)), t = p.target_view!, field = kind === 'camera' ? 'camera_ids' : 'region_ids'
      const a = evidence('a',3), b = evidence('b',4), unread = evidence('unread',5), unrelated = evidence('unrelated',6), attachment = evidence('attachment',7)
      a[field] = ['first']; b[field] = ['first','second']; unread[field] = ['second','unread-only']; unrelated[field] = ['other','undeclared']; attachment[field] = ['attachment-only']; attachment.mime = 'application/json'
      t.evidence = [a,b,unread,unrelated,attachment]; t.coverage[field] = ['first','second','unread-only','attachment-only','missing','other']
      const readable = ['a','b','unrelated','attachment'], options = mappedReviewOptions(p,kind,readable)
      assert.deepEqual(options.find(option => option.id === 'first')?.evidenceIDs,['a','b'])
      for (const name of ['unread-only','attachment-only','missing']) assert.deepEqual(options.find(option => option.id === name)?.evidenceIDs,[])
      assert.equal(options.some(option => option.id === 'undeclared'),false)
      const selection = (ids: string[]) => ({ kind, ...(kind === 'camera' ? { cameraIDs: ids } : { regionIDs: ids }) })
      const result = resolveReviewScope(p,selection(['second','first']),readable)
      assert.deepEqual(result.scope,{kind,evidence_ids:['a','b'],camera_ids:kind === 'camera' ? ['first','second'] : [],region_ids:kind === 'region' ? ['first','second'] : [],claims:['image_reading']})
      for (const ids of [[],['first','missing'],['first','unread-only'],['attachment-only'],['undeclared'],['first','first']]) assert.equal(resolveReviewScope(p,selection(ids),readable).scope,undefined)
      assert.equal(resolveReviewScope(p,selection(['second']),['a']).scope,undefined,'losing the last readable mapping invalidates the selected scope')
      t.coverage[field] = ['first']
      assert.equal(resolveReviewScope(p,selection(['second']),readable).scope,undefined,'a formerly registered selection cannot survive a changed target')
    })
  }
  test(`${domain}: incomplete full target never falls back to image scope`, () => {
    const p = page(locator(domain)), t = p.target_view!
    t.requirements = ['space_layout','source_recovery','full_set'].map(claim => ({requirement_id:claim,claim,evidence_ids:['render'],state:'satisfied',reason_code:''}))
    assert.equal(fullReviewEligible(p),true)
    assert.equal(resolveReviewScope(p,{kind:'full'},['render']).scope?.kind,'full_target')
    t.requirements[0].state = 'unverified'
    assert.equal(fullReviewEligible(p),false)
    assert.equal(resolveReviewScope(p,{kind:'full',evidenceID:'render'},['render']).scope,undefined)
    assert.equal(resolveReviewScope(p,{kind:'current',evidenceID:'render'},['render']).scope?.kind,'evidence','only explicit image selection creates an image scope')
    t.requirements[0].state = 'satisfied'; p.availability = [{code:'RESTRICTED',evidence_id:'render'}]
    assert.equal(resolveReviewScope(p,{kind:'full'},['render']).scope,undefined)
  })
  test(`${domain}: current and all-image choices cannot include unread or silently replace missing images`, () => {
    const p = page(locator(domain)); p.target_view!.evidence.push(evidence('second',4))
    assert.equal(resolveReviewScope(p,{kind:'current',evidenceID:'missing'},['render']).scope,undefined)
    assert.equal(resolveReviewScope(p,{kind:'current',evidenceID:'render'},[]).scope,undefined)
    assert.equal(resolveReviewScope(p,{kind:'images'},['render']).scope,undefined)
    assert.deepEqual(resolveReviewScope(p,{kind:'images'},['render','second']).scope?.evidence_ids,['render','second'])
  })
  test(`${domain}: region mappings are explicit arrays in the readable evidence DTO`, () => {
    const p = page(locator(domain))
    assert.equal(readReview(p,p.target_locator).target_view!.evidence[0].region_ids[0],'courtyard')
    for (const region_ids of [undefined,null,['courtyard',42]]) assert.throws(() => readReview({...p,target_view:{...p.target_view,evidence:[{...evidence(),region_ids}]}},p.target_locator),/INVALID_RESPONSE/)
  })
}

test('project review routes accept 65 and 128 ASCII key characters and reject 129', () => {
  const original = locator('project')
  if (original.kind !== 'project') throw Error('project fixture required')
  for (const field of ['material_key','state_key','version_key'] as const) {
    for (const length of [65,128,129]) {
      const value = { ...original, [field]: 'A_0-'.repeat(Math.ceil(length / 4)).slice(0,length) }
      const path = reviewPath(value,hash)
      assert.deepEqual(parseReviewPath(path), length <= 128 ? { locator:value,hash } : undefined, `${field}: ${length} characters`)
    }
  }
})
