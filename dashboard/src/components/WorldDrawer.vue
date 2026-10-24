<script setup lang="ts">
import { onBeforeUnmount, ref, useId } from 'vue'
defineProps<{ title: string }>()
const emit=defineEmits<{ 'request-close': [] }>()
const panel=ref<HTMLDialogElement|null>(null),titleID=useId()
let opener:HTMLElement|null=null
function showModal(){opener=document.activeElement as HTMLElement;panel.value?.showModal()}
function stopMedia(){panel.value?.querySelectorAll<HTMLMediaElement>('audio,video').forEach(media=>media.pause())}
function close(){stopMedia();panel.value?.close();if(opener?.isConnected)opener.focus({preventScroll:true})}
function isOpen(){return !!panel.value?.open}
onBeforeUnmount(stopMedia)
defineExpose({showModal,close,isOpen})
</script>
<template><dialog ref="panel" class="world-drawer" :aria-labelledby="titleID" @cancel.prevent="emit('request-close')" @click="event=>{if(event.target===panel)emit('request-close')}">
  <header class="drawer-head"><h2 :id="titleID">{{title}}</h2><button class="btn" aria-label="关闭面板" @click="emit('request-close')">关闭</button></header>
  <div class="drawer-body"><slot/></div>
  <footer v-if="$slots.footer" class="drawer-foot"><slot name="footer"/></footer>
</dialog></template>
<style scoped>
.world-drawer{position:fixed;inset:0 0 0 auto;margin:0;width:min(760px,95vw);height:100dvh;max-width:95vw;max-height:100dvh;border:0;border-left:1px solid var(--line);border-radius:16px 0 0 16px;padding:0;background:var(--panel);color:var(--ink);box-shadow:-16px 0 60px #0002}
.world-drawer[open]{display:flex;flex-direction:column;animation:drawer-in .2s ease-out}.world-drawer::backdrop{background:#111c174d;backdrop-filter:blur(3px)}
.drawer-head{padding:23px 28px;border-bottom:1px solid var(--line);display:flex;justify-content:space-between;align-items:center;gap:16px;flex-shrink:0}.drawer-head h2{font-size:19px;margin:0;font-weight:550;overflow-wrap:anywhere}
.drawer-body{padding:26px 28px;overflow-y:auto;flex:1;min-height:0;font-size:14px;line-height:1.8}.drawer-foot{padding:18px 28px;border-top:1px solid var(--line);display:flex;gap:10px;justify-content:flex-end;flex-wrap:wrap;background:var(--panel);flex-shrink:0}
@keyframes drawer-in{from{transform:translateX(24px);opacity:0}to{transform:translateX(0);opacity:1}}@media(max-width:700px){.drawer-head,.drawer-body,.drawer-foot{padding-left:20px;padding-right:20px}}@media(prefers-reduced-motion:reduce){.world-drawer[open]{animation:none}}
</style>
