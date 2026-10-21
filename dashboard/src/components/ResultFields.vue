<script setup lang="ts">
import { computed, ref, watch } from 'vue'
defineOptions({ name:'ResultFields' })
const props=withDefaults(defineProps<{value:unknown;depth?:number}>(),{depth:0})
const labels:Record<string,string>={
 variables:'可改变量',parameters:'生成参数',reference_bindings:'参考素材绑定',locator:'来源位置',source_ref:'来源引用',start_ms:'开始时间（毫秒）',end_ms:'结束时间（毫秒）',start_seconds:'开始时间（秒）',end_seconds:'结束时间（秒）',start_sec:'开始时间（秒）',end_sec:'结束时间（秒）',time_range:'时间范围',confidence:'分析置信度',observation:'观察',rationale:'分析理由',summary:'概述',status:'状态',
 text:'正文',premise:'故事前提',theme:'主题',goal:'目标',conflict:'冲突',stakes:'代价',reversal:'反转',hook:'悬念',story_time:'故事时间',dialogue_turns:'对白',
 unit_kind:'剧作单元',section_kind:'音乐段落',scope:'适用范围',meter:'拍号',tempo_events:'速度变化',key_signature:'调性',chord_events:'和声',instrument_layers:'配器层次',energy_curve:'能量曲线',
 segment_kind:'片段类型',shot_scale:'景别',camera_angle:'机位角度',camera_motion:'相机运动',subject_motion:'主体运动',graphic_motion:'图形运动',composition:'构图',lens_note:'镜头说明',transition:'转场',action_beats:'动作节拍',
 character_kind:'人物类型',identity_notes:'身份设定',appearance:'外形',costume:'服装',performance_notes:'表演',motives:'动机',traits:'特征',
 setting:'环境设定',spatial_layout:'空间布局',prop_roles:'道具作用',material_notes:'材质',lighting_notes:'灯光',description:'说明',function:'功能',state:'状态',scale_basis:'尺度依据',interaction_points:'交互点',
 sound_kind:'声音类型',transcript:'转录',delivery:'表达',cue_points:'声音触发点',purpose:'用途',objective:'目标',applicability:'适用条件',inputs:'输入',steps:'步骤',constraints:'约束',expected_outputs:'预期成果',counterexamples:'反例',scope_note:'范围说明',rules:'规则',positive_examples:'正向示例',palette:'色彩',
 research_subjects:'研究对象',corpus_refs:'研究作品引用',modules:'体系模块',coordination_rules:'协同规则',exceptions:'例外',validation_case_refs:'验证案例引用',dependencies:'依赖',outputs:'输出',checks:'检查方法',validation_refs:'验证引用',
 file_kind:'文件类型',files:'文件',format:'格式',intended_use:'适用用途',verification:'验证记录',engineering_ref:'工程引用',units:'单位',coordinate_system:'坐标系',scene_key:'场景标识',object_bindings:'对象绑定',reconstruction_basis:'重建依据',dependency_refs:'依赖引用',
 camera_key:'相机标识',frame_start:'起始帧',frame_end:'结束帧',fps_num:'帧率分子',fps_den:'帧率分母',animation_ref:'动画引用',previs_ref:'预演引用',projection:'投影方式',workflow_ui_ref:'节点画布引用',dependency_lock_ref:'依赖清单引用',execution_snapshot_ref:'执行快照引用',input_bindings:'输入绑定',parameter_overrides:'参数设置',runtime_version:'运行时版本',
 key:'标识',title:'名称',note:'说明',value:'值',value_type:'值类型',required:'必填',default:'默认值',kind:'种类',file_id:'文件标识',asset_id:'资产标识',asset_version_id:'资产版本',source_key:'来源标识',result_key:'成果标识',start:'起点',end:'终点',language:'语言',name:'名称',role:'角色',time:'时间',
}
function hasValue(value:unknown):boolean { return value != null && value !== '' && (!Array.isArray(value) || value.length>0) && (typeof value!=='object' || Object.keys(value).length>0) }
const entries=computed(()=>props.value&&typeof props.value==='object'&&!Array.isArray(props.value)?Object.entries(props.value).filter(([,v])=>hasValue(v)):null)
const technical=/^(key|.*_id|.*_key|.*_ref|.*_refs|locator|sha256|digest)$/
const primary=computed(()=>entries.value?.filter(([key])=>labels[key]&&!technical.test(key))||[])
const extra=computed(()=>entries.value?.filter(([key])=>!labels[key]||technical.test(key))||[])
const empty=computed(()=>!hasValue(props.value)||entries.value?.length===0)
const limit=ref(6)
watch(()=>props.value,()=>{limit.value=6})
</script>
<template>
<span v-if="empty" class="empty">未记录</span>
<details v-else-if="depth>=3" class="supplement"><summary>展开完整内容</summary><pre>{{JSON.stringify(value,null,2)}}</pre></details>
<template v-else-if="Array.isArray(value)"><ul class="items"><li v-for="(item,i) in value.slice(0,limit)" :key="i"><ResultFields :value="item" :depth="depth+1"/></li></ul><button v-if="value.length>limit" class="btn more" @click="limit+=6">再显示 {{Math.min(6,value.length-limit)}} 项 · 还剩 {{value.length-limit}} 项</button><button v-if="limit>6" class="btn more" @click="limit=6">收起为前 6 项</button></template>
<template v-else-if="entries"><dl v-if="primary.length" class="fields"><template v-for="[key,item] in primary" :key="key"><dt>{{labels[key]}}</dt><dd><ResultFields :value="item" :depth="depth+1"/></dd></template></dl><details v-if="extra.length" class="supplement"><summary>补充信息与原始字段 · {{extra.length}} 项</summary><pre>{{JSON.stringify(Object.fromEntries(extra),null,2)}}</pre></details></template>
<span v-else class="scalar">{{typeof value==='boolean'?(value?'是':'否'):value}}</span>
</template>
<style scoped>
.fields{display:grid;grid-template-columns:minmax(90px,140px) minmax(0,1fr);gap:16px 24px;margin:0}.fields dt,.empty{color:var(--muted);font-size:12px;line-height:1.8}.fields dd{margin:0;min-width:0}.items{margin:0;padding-left:20px;display:grid;gap:16px}.items>li{min-width:0}.scalar,pre{white-space:pre-wrap;overflow-wrap:anywhere;line-height:1.8}.scalar{font-size:14px}pre{font:inherit;font-size:12px;margin:14px 0;padding:16px;border:1px solid var(--line);border-radius:8px;background:var(--shell-button)}.supplement{margin-top:14px;font-size:12px;color:var(--muted)}summary{cursor:pointer;line-height:1.8}.more{margin:14px 10px 0 0}.fields .fields{grid-template-columns:1fr;gap:6px}.fields .fields dd{margin-bottom:10px}@media(max-width:700px){.fields{grid-template-columns:1fr;gap:6px}.fields dd{margin-bottom:12px}}
</style>
