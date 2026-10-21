<script setup lang="ts">
import { computed, inject, onBeforeUnmount, ref, watch } from 'vue'
import type { LearningDocument, LearningResult } from '../platform/learning-results'
import { resultReference, timeLabel } from '../platform/result-reference'
import { referenceMediaKey } from '../platform/reference-media'
const props=defineProps<{result:LearningResult;document:LearningDocument;thumbnail?:boolean}>()
const media=inject(referenceMediaKey,null),reference=computed(()=>resultReference(props.result,props.document))
const audio=computed(()=>['sound_cue','music_structure'].includes(props.result.type_key))
const image=computed(()=>['character','scene','prop'].includes(props.result.type_key))
const source=ref(''),poster=ref(''),loading=ref(false),error=ref(''),selected=ref(0),player=ref<HTMLMediaElement|null>(null),audioPlaying=ref(false),elapsed=ref(0)
const range=computed(()=>reference.value?.ranges[selected.value])
let generation=0,alive=true
function stop(){player.value?.pause();source.value='';poster.value='';audioPlaying.value=false;elapsed.value=0;loading.value=false}
async function load(){
 const current=++generation;stop();error.value=''
 if(!media?.active.value||!reference.value||props.thumbnail&&audio.value)return
 const value=reference.value,r=range.value;if(!r)return
 loading.value=true
 const valid=()=>alive&&current===generation&&media.active.value
 try{
  const url=await media.load(value);if(!valid())return
  if(!props.thumbnail)source.value=url
  if(!audio.value){const frame=await media.frame(value,r.start+(r.end-r.start)/2);if(valid())poster.value=frame}
 }catch{if(valid())error.value='这段原片暂时无法读取。请重试；没有用其他画面或声音替代。'}
 finally{if(valid())loading.value=false}
}
function loaded(){
 const p=player.value,r=range.value;if(!p||!r)return
 if(!Number.isFinite(p.duration)||r.end>p.duration+.05){p.pause();error.value='片段时间超出原片范围，暂不能播放。';source.value='';return}
 p.currentTime=r.start
}
function boundary(){const p=player.value,r=range.value;if(!p||!r)return;elapsed.value=Math.max(0,Math.min(r.end-r.start,p.currentTime-r.start));if(p.currentTime>=r.end){p.pause();p.currentTime=r.end}else if(p.currentTime<r.start-.05)p.currentTime=r.start}
function play(){const p=player.value,r=range.value;if(p&&r&&(p.currentTime>=r.end-.05||p.currentTime<r.start))p.currentTime=r.start}
async function toggleAudio(){const p=player.value;if(!p)return;try{if(p.paused)await p.play();else p.pause()}catch{error.value='这段原片暂不能播放，请重新读取。'}}
function seekAudio(event:Event){const p=player.value,r=range.value;if(p&&r)p.currentTime=r.start+Math.max(0,Math.min(r.end-r.start,Number((event.target as HTMLInputElement).value)))}
async function preview(){try{if(poster.value&&media)await media.preview(poster.value,props.result.title+' · 原片参考画面')}catch{error.value='当前无法放大画面，请重新打开成果。'}}
watch(()=>[props.result.key,media?.epoch.value,media?.active.value],()=>{selected.value=0;void load()},{immediate:true})
watch(selected,()=>void load())
onBeforeUnmount(()=>{alive=false;generation++;stop()})
</script>
<template>
<div v-if="thumbnail" class="reference-thumbnail" aria-hidden="true"><img v-if="poster" :src="poster" alt=""><span v-else>{{loading?'正在读取原片画面':reference?'原片参考画面':'缺少对应画面'}}</span></div>
<section v-else class="reference-panel" aria-label="原片参考">
<header><strong>{{audio?'听听原片中的声音':image?(result.type_key==='character'?'人物参考图片':'原片参考图片'):'观看对应原片片段'}}</strong><span>{{audio?'原片混合音轨':'原片参考 · 非生成样例'}}</span></header>
<template v-if="reference && range">
<label v-if="reference.ranges.length>1" class="segment-select">{{image?'选择参考图片':'选择出现片段'}}<select v-model="selected"><option v-for="(r,i) in reference.ranges" :key="i" :value="i">片段 {{i+1}} · {{timeLabel(r.start)}}–{{timeLabel(r.end)}}</option></select></label>
<div v-if="loading" class="loading" role="status">正在读取原片{{audio?'声音':'画面'}}…</div>
<button v-if="image && poster" class="still" :aria-label="`放大${result.title}的原片画面`" @click="preview"><img :src="poster" :alt="`${result.title}的关联镜头参考画面`"></button>
<template v-if="source && audio"><audio ref="player" :src="source" preload="metadata" @loadedmetadata="loaded" @play="play();audioPlaying=true" @pause="audioPlaying=false" @timeupdate="boundary" @seeked="boundary" @error="error='浏览器暂不能播放这段原片声音。'"/><div class="audition"><button class="btn primary" @click="toggleAudio">{{audioPlaying?'暂停试听':'试听原片声音'}}</button><label>试听进度<input type="range" min="0" :max="range.end-range.start" step="0.05" :value="elapsed" aria-label="试听片段进度" @input="seekAudio"></label><span>{{elapsed.toFixed(1)}} / {{(range.end-range.start).toFixed(1)}} 秒</span></div></template>
<video v-else-if="source && !image" ref="player" :src="source" :poster="poster" controls playsinline preload="metadata" @loadedmetadata="loaded" @play="play" @timeupdate="boundary" @seeked="boundary" @error="error='浏览器暂不能播放这段原片。'"/>
<div class="caption"><span>{{timeLabel(range.start)}}–{{timeLabel(range.end)}} · {{(range.end-range.start).toFixed(1)}} 秒</span></div>
<details v-if="image && source" class="original-video"><summary @click="player?.pause()">查看原片来源视频</summary><video ref="player" :src="source" controls playsinline preload="metadata" @loadedmetadata="loaded" @play="play" @timeupdate="boundary" @seeked="boundary"/></details>
<p v-if="range.note" class="observation">{{range.note}}</p><p class="note">{{audio?'保留原片中的全部声音，未分离人声或配乐。':'原片截图 · 对应关系由自动分析定位，待核对。'}}</p>
<p v-if="error" role="alert">{{error}}</p><button v-if="error && media?.active.value" class="btn" @click="load">重试读取原片</button>
</template>
<div v-else class="missing"><strong>{{audio?'缺少可定位的试听片段':'缺少可定位的原片画面'}}</strong><p>这项目前只有分析说明，尚不能作为完整的{{audio?'声音':'视觉'}}参考。</p></div>
</section>
</template>
<style scoped>
.audition{display:flex;gap:16px;align-items:center;flex-wrap:wrap;padding:18px 20px}.audition label{flex:1;min-width:130px;font-size:12px;color:var(--muted)}.audition input{display:block;width:100%;accent-color:var(--accent)}.audition span{font-size:12px;color:var(--muted)}audio{display:none}.original-video{margin:16px 20px}.original-video summary{cursor:pointer;font-size:12px;color:var(--muted);margin-bottom:10px}.reference-thumbnail{height:152px;background:var(--shell-button);border-radius:9px;overflow:hidden;display:grid;place-items:center;color:var(--muted);font-size:12px;width:100%}.reference-thumbnail img{width:100%;height:100%;object-fit:contain}.reference-panel{border:1px solid var(--line);border-radius:12px;overflow:hidden;background:var(--shell-button)}header{padding:18px 20px;display:flex;gap:10px;align-items:center;justify-content:space-between;flex-wrap:wrap}header strong{font-size:15px}header span,.note,.caption,.observation{font-size:12px;color:var(--muted);line-height:1.8}.still{display:block;width:100%;border:0;padding:0;background:var(--shell-button);cursor:zoom-in}.still img{width:100%;max-height:460px;object-fit:contain;display:block}video{display:block;width:100%;max-height:460px;background:var(--shell-button)}audio{display:block;width:calc(100% - 40px);margin:18px 20px}.caption{display:flex;gap:12px;align-items:center;justify-content:space-between;flex-wrap:wrap;margin:16px 20px}.note,.observation{margin:12px 20px}.observation{color:var(--ink)}.segment-select{display:flex;gap:12px;align-items:center;flex-wrap:wrap;margin:0 20px 16px;font-size:12px;color:var(--muted)}select{max-width:100%;padding:8px;border:1px solid var(--line);border-radius:8px;color:var(--ink);background:var(--shell-panel);font:inherit}.loading,.missing{padding:32px 20px;color:var(--muted);font-size:13px;line-height:1.8}.missing strong{font-weight:500}p[role=alert]{padding:0 20px;color:var(--danger);font-size:13px}.reference-panel>.btn{margin:0 20px 20px}
</style>
