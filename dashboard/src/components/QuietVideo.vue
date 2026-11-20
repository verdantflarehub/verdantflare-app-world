<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
const props = defineProps<{ source: string; title: string }>()
const emit = defineEmits<{ error: [] }>()
const root = ref<HTMLElement>(), video = ref<HTMLVideoElement>(), paused = ref(true), progress = ref(0), message = ref('')
let observer: IntersectionObserver | undefined, preference: MediaQueryList | undefined, visible = false, userPaused = false
async function play() { try { if (video.value) { video.value.muted = true; await video.value.play() } } catch { paused.value = true } }
function sync() {
  if (!visible || document.hidden || userPaused || preference?.matches) video.value?.pause()
  else void play()
}
function toggle() {
  if (video.value?.paused) { userPaused = false; void play() }
  else { userPaused = true; video.value?.pause() }
}
function seek(event: Event) {
  const v = video.value
  if (v && Number.isFinite(v.duration)) v.currentTime = Number((event.target as HTMLInputElement).value) / 100 * v.duration
}
function escapeFullscreen(event: KeyboardEvent) {
  if (event.key !== 'Escape' || !root.value?.matches(':fullscreen')) return
  event.preventDefault(); event.stopImmediatePropagation()
  void document.exitFullscreen().catch(() => {})
}
async function fullscreen() {
  try {
    if (root.value?.matches(':fullscreen')) await document.exitFullscreen()
    else if (root.value?.requestFullscreen) await root.value.requestFullscreen()
    else message.value = '当前浏览器暂不支持全屏。'
  } catch { message.value = '当前浏览器暂不支持全屏。' }
}
onMounted(() => {
  window.addEventListener('keydown', escapeFullscreen, true)
  preference = window.matchMedia('(prefers-reduced-motion: reduce)')
  preference.addEventListener('change', sync); document.addEventListener('visibilitychange', sync)
  observer = new IntersectionObserver(entries => { visible = entries.some(e => e.isIntersecting); sync() }, { threshold: .15 })
  if (root.value) observer.observe(root.value)
})
onBeforeUnmount(() => { window.removeEventListener('keydown', escapeFullscreen, true); video.value?.pause(); observer?.disconnect(); preference?.removeEventListener('change', sync); document.removeEventListener('visibilitychange', sync) })
</script>
<template>
  <div ref="root" class="quiet-video" :class="{ paused }" role="group" :aria-label="props.title">
    <video ref="video" :src="source" muted loop playsinline preload="metadata" :aria-label="title" @play="paused=false" @pause="paused=true" @timeupdate="progress=video?.duration ? video.currentTime / video.duration * 100 : 0" @error="emit('error')"/>
    <button class="video-action toggle" type="button" :aria-label="paused ? '播放预演' : '暂停预演'" @click="toggle">
      <svg v-if="paused" viewBox="0 0 24 24" aria-hidden="true"><path d="m9 5 11 7-11 7z"/></svg>
      <svg v-else viewBox="0 0 24 24" aria-hidden="true"><path d="M8 5v14M16 5v14"/></svg>
    </button>
    <button class="video-action fullscreen" type="button" aria-label="全屏预演" @click="fullscreen"><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M8 3H3v5m13-5h5v5M3 16v5h5m13-5v5h-5"/></svg></button>
    <input class="seek" type="range" min="0" max="100" step="0.1" :value="progress" aria-label="预演进度" @input="seek">
    <p v-if="message" class="message" role="status">{{message}}</p>
  </div>
</template>
<style scoped>
.quiet-video{position:relative;overflow:hidden;border-radius:12px;background:var(--shell-button)}
video{display:block;width:100%;max-height:480px;object-fit:contain}
.video-action{position:absolute;bottom:23px;width:36px;height:36px;display:grid;place-items:center;border:1px solid #ffffffb3;border-radius:50%;background:#ffffffd9;color:#233832;box-shadow:0 1px 6px #12251c1f;cursor:pointer;opacity:0;transition:opacity .15s}
.video-action svg{width:19px;height:19px;fill:none;stroke:currentColor;stroke-width:1.8;stroke-linecap:round;stroke-linejoin:round}.toggle{left:12px}.toggle svg:first-child{fill:none}.fullscreen{right:12px}
.seek{position:absolute;bottom:3px;left:12px;width:calc(100% - 24px);height:14px;margin:0;accent-color:var(--accent);opacity:0;cursor:pointer}
.quiet-video:hover .video-action,.quiet-video:focus-within .video-action,.paused .video-action,.quiet-video:hover .seek,.quiet-video:focus-within .seek,.paused .seek{opacity:1}
.video-action:focus-visible,.seek:focus-visible{outline:2px solid var(--accent);outline-offset:3px}.message{position:absolute;top:8px;left:8px;padding:6px 10px;border-radius:8px;background:var(--panel);color:var(--text);font-size:12px}
.quiet-video:fullscreen{display:flex;align-items:center;justify-content:center;border-radius:0}.quiet-video:fullscreen video{height:100%;max-height:100vh}
@media(hover:none){.video-action,.seek{opacity:1}}
@media(prefers-reduced-motion:reduce){.video-action{transition:none}}
</style>
