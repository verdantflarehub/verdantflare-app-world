<script setup lang="ts">
import { reviewLabels, previewRoles, fixedMergeLabel, type ResultReview } from '../platform/result-review'
defineProps<{ review: ResultReview }>()
</script>
<template><section class="review" aria-label="用途审核与预览">
<h4>{{reviewLabels[review.selection]}}</h4><p>{{review.note}}</p>
<p v-if="review.purposes.length">记录用途：{{review.purposes.join(' · ')}}</p>
<p v-if="review.selection==='recommended'">推荐仅对记录用途有效，不代表模型效果或审美验收。</p>
<details v-if="review.merged.length"><summary>固定合并来源（{{review.merged.length}}）</summary><ul><li v-for="(source,i) in review.merged" :key="i">{{fixedMergeLabel(source)}}</li></ul></details>
<p v-else>合并来源：未记录固定合并来源。</p>
<div v-for="(label,role) in previewRoles" :key="role" class="preview-state">
<strong>{{label}}</strong>
<template v-if="review.previews.some(p=>p.role===role)"><p v-for="p in review.previews.filter(p=>p.role===role)" :key="String(p.key)">{{p.state==='ready'?'已记录媒体引用':'缺少预览'}} · {{p.note}}</p></template>
<p v-else>{{role==='original_reference'?'用途审核未单独登记原作预览；已有原作信息见正文。':'尚无此类预览记录。'}}</p>
</div>
<p class="boundary">原作参考不代表生成或重建效果；媒体引用已登记不等于当前可访问或已通过效果验收。</p>
</section></template>
<style scoped>
.review{border:1px solid var(--line);border-radius:12px;padding:18px;margin-bottom:24px;background:var(--shell-panel);min-width:0;overflow-wrap:anywhere}.review h4{margin:0 0 10px;font-size:14px}.review p,.review li,.review summary{font-size:12px;line-height:1.8;color:var(--muted)}.review p{margin:6px 0;white-space:pre-wrap}.review summary{cursor:pointer}.review ul{padding-left:20px}.preview-state{margin-top:10px}.preview-state strong{font-size:12px;font-weight:600}.boundary{border-top:1px solid var(--line);padding-top:10px}
</style>
