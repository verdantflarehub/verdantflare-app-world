/** Capture only after a decoded frame is presented; metadata/seeked alone can yield black frames. */
export async function captureReferenceFrame(url:string,at:number,signal:AbortSignal):Promise<Blob>{
 signal.throwIfAborted()
 const video=document.createElement('video')
 video.muted=true;video.playsInline=true;video.preload='auto'
 video.style.cssText='position:fixed;left:-10000px;top:0;width:2px;height:2px;pointer-events:none'
 video.setAttribute('aria-hidden','true');document.body.append(video)
 let frameID:number|undefined
 try{
  return await new Promise<Blob>((resolve,reject)=>{
   let done=false
   const finish=(error?:Error,value?:Blob)=>{if(done)return;done=true;clearTimeout(timer);signal.removeEventListener('abort',abort);video.onloadedmetadata=null;video.onseeked=null;video.onerror=null;error?reject(error):resolve(value!)}
   const abort=()=>finish(Error('预览已停止。')),timer=setTimeout(()=>finish(Error('原片画面读取超时，请重试。')),20000)
   signal.addEventListener('abort',abort,{once:true})
   const draw=()=>{
    video.pause();if(done)return
    if(!video.videoWidth||!video.videoHeight){finish(Error('原片没有可用画面。'));return}
    const canvas=document.createElement('canvas'),scale=Math.min(1,960/video.videoWidth)
    canvas.width=Math.round(video.videoWidth*scale);canvas.height=Math.round(video.videoHeight*scale)
    try{canvas.getContext('2d')!.drawImage(video,0,0,canvas.width,canvas.height);canvas.toBlob(b=>b?finish(undefined,b):finish(Error('无法读取原片画面。')),'image/jpeg',.88)}catch{finish(Error('无法读取原片画面。'))}
   }
   video.onerror=()=>finish(Error('浏览器不能解码这份原片。'))
   video.onloadedmetadata=()=>{if(!Number.isFinite(video.duration)||at<0||at>=video.duration){finish(Error('画面时间超出原片范围。'));return}video.currentTime=at}
   video.onseeked=()=>{
    video.onseeked=null
    if(typeof video.requestVideoFrameCallback==='function'){
     frameID=video.requestVideoFrameCallback(()=>draw())
     void video.play().catch(()=>finish(Error('浏览器未允许读取原片画面。')))
    }else if(video.readyState>=2)draw()
    else finish(Error('浏览器尚未解码此帧。'))
   }
   video.src=url;video.load()
  })
 }finally{if(frameID!==undefined)video.cancelVideoFrameCallback(frameID);video.pause();video.removeAttribute('src');video.load();video.remove()}
}
