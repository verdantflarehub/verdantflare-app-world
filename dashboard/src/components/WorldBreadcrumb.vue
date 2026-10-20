<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { catalogDomain } from '../platform/world-catalog'
const route = useRoute()
const section = computed(() => route.path.startsWith('/collections') ? { title: '收录内容', path: '/collections' }
  : route.path === '/learn' ? { title: '拆解作品', path: '/learn' }
  : catalogDomain(route.path) ? { title: catalogDomain(route.path)!.label, path: route.path } : null)
const detail = computed(() => !!route.query.asset || route.path.startsWith('/collections/'))
</script>
<template>
  <nav v-if="section || detail" class="world-breadcrumb" aria-label="当前位置">
    <RouterLink to="/">数字世界</RouterLink>
    <template v-if="section"><span aria-hidden="true">/</span><RouterLink v-if="detail" :to="section.path">{{section.title}}</RouterLink><span v-else aria-current="page">{{section.title}}</span></template>
    <template v-if="detail"><span aria-hidden="true">/</span><span aria-current="page">{{route.path.startsWith('/collections/')?'收录详情':'资产详情'}}</span></template>
  </nav>
</template>
<style scoped>
.world-breadcrumb { display:flex; flex-wrap:wrap; align-items:center; gap:8px; margin-bottom:16px; color:var(--muted); font-size:12px; }
a:hover { color:var(--accent); text-decoration:underline; }
</style>
