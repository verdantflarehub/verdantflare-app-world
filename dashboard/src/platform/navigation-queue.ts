/** Serialize host navigation so delayed permission checks cannot reorder clicks. */
export function createNavigationQueue(hostPath:()=>string, request:(path:string)=>Promise<unknown>, rollback:(path:string)=>void) {
 let desired:string|null=null, inflight:string|null=null, disposed=false
 async function drain(){
  if(inflight!==null||disposed)return
  while(desired!==null&&!disposed){
   const path=desired
   if(path===hostPath()){desired=null;return}
   inflight=path
   try{await request(path)}catch{
    if(!disposed&&desired===path){desired=null;rollback(hostPath())}
   }
   if(desired===path)desired=null
   inflight=null
  }
 }
 return {
  request(path:string){if(disposed)return;desired=path;void drain()},
  staleEcho(path:string){return inflight===path&&desired!==null&&desired!==path},
  dispose(){disposed=true;desired=null},
 }
}
