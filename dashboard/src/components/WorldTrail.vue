<script setup lang="ts">
import type { RouteLocationRaw } from 'vue-router'
export interface TrailItem { label: string; to?: RouteLocationRaw }
defineProps<{ items: TrailItem[] }>()
const emit = defineEmits<{ back: [] }>()
</script>
<template><div class="world-trail"><nav aria-label="面包屑"><template v-for="(item,i) in items" :key="i"><span v-if="i" class="separator" aria-hidden="true">/</span><RouterLink v-if="item.to" :to="item.to">{{item.label}}</RouterLink><button v-else-if="i<items.length-1" @click="emit('back')">{{item.label}}</button><span v-else class="current" aria-current="page">{{item.label}}</span></template></nav><div v-if="$slots.default" class="trail-actions"><slot/></div></div></template>
<style scoped>
.world-trail{display:flex;align-items:center;justify-content:space-between;gap:14px;margin:0 0 24px;min-width:0}.world-trail nav{display:flex;align-items:center;gap:9px;flex-wrap:wrap;min-width:0;font-size:12px;line-height:1.8;color:var(--muted)}a,button{color:inherit;font:inherit;text-decoration:none;background:none;border:0;padding:0;cursor:pointer}a:hover,button:hover{color:var(--accent)}a:focus-visible,button:focus-visible{outline:2px solid var(--accent);outline-offset:4px}.current{color:var(--ink);overflow-wrap:anywhere}.separator{opacity:.5}.trail-actions{display:flex;gap:10px;align-items:center;flex-shrink:0;color:var(--muted);font-size:12px}@media(max-width:760px){.world-trail{flex-wrap:wrap;gap:12px}.trail-actions{width:100%;justify-content:flex-end}}
</style>
