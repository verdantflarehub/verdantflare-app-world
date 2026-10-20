const positions=new Map<string,number>()
export function rememberPosition(scope:string,path:string,top:number) {
  const key=scope+':'+path; positions.delete(key);positions.set(key,Math.max(0,top))
  while(positions.size>100) positions.delete(positions.keys().next().value!)
}
export function recalledPosition(scope:string,path:string) { return positions.get(scope+':'+path)||0 }
