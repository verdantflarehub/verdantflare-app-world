import type { LearningResult } from './learning-results.ts'
import { record } from './result-reference.ts'
export interface ReadingSection { title: string; lines: string[] }
export interface ResultReading { purpose: string; limitation: string; sections: ReadingSection[] }
const text = (v: unknown): string => typeof v === 'string' ? v.trim() : ''
const list = (v: unknown): unknown[] => Array.isArray(v) ? v : []
const placeholders = new Set(['原片镜头内可见的服装款式、色彩与配饰特征。','镜头内观察到的神态、肢体动作与表演方式。','标准焦段自然透视与景深控制。','硬切衔接相邻镜头。'])
export function resultReading(r: LearningResult): ResultReading {
  const p = r.payload, sections: ReadingSection[] = []
  const field = (title: string, key: string) => { const value = text(p[key]); if (value && !placeholders.has(value)) sections.push({title,lines:[value]}) }
  const items = (title: string, key: string, project: (v: Record<string,unknown>) => string = v => text(v.description)) => {
    const lines = list(p[key]).map(v => typeof v === 'string' ? v : project(record(v))).filter(Boolean)
    if (lines.length) sections.push({title,lines})
  }
  let purpose = '作为新创作的参考说明，复制后可交给项目中的智能体使用。', limitation = '这是参考分析，实际制作效果尚未验证。'
  switch (r.type_version===1?r.type_key:'unknown') {
    case 'character':
      purpose='参考人物的外形、造型与表演，帮助项目设计新角色。'; limitation='原片画面是形象参考；尚不是独立角色设定图或可直接调用的演员模型。'
      field('人物是谁','identity_notes'); field('外形特点','appearance'); field('服装与配饰','costume'); items('人物特点','traits'); field('表演特点','performance_notes'); break
    case 'scene':
      purpose='参考空间、光线和材质，为新场景的美术与布景提供依据。'; limitation='这是原片场景参考，尚不是可编辑的三维片场。'
      field('场景印象','setting'); field('空间布局','spatial_layout'); field('光线与氛围','lighting_notes'); field('材质','material_notes'); field('道具与空间的关系','prop_roles'); break
    case 'prop':
      purpose='参考道具的外形和剧情作用，在项目中设计或制作新道具。'; limitation='原片中的道具画面不是已抠出的素材或三维模型。'
      field('道具说明','description'); field('外形','appearance'); field('材质','material'); field('用途','function'); field('剧情作用','narrative_role');
      items('怎样互动','interaction_points',v=>({grip_and_hug:'可以衔取或抱住',opening:'开合盒盖',gripping:'握住把手',picking_up:'拾取',carrying:'提拿'} as Record<string,string>)[text(v.purpose)]||text(v.description)); break
    case 'shot':
      purpose='参考这段镜头的构图、主体动作与运镜，编排新项目的分镜。'; limitation='可观看原片运镜；尚不是可编辑的摄影机动画工程。'
      field('景别','shot_scale'); field('拍摄角度','camera_angle'); field('构图','composition'); field('镜头怎样移动','camera_motion'); field('主体怎样动作','subject_motion'); field('画面内其他运动','graphic_motion'); field('镜头与景深','lens_note'); field('镜头如何衔接','transition'); items('动作过程','action_beats',v=>text(v.action)); break
    case 'sound_cue':
      purpose='试听声音特征与出现时机，作为项目配音、音效或声音设计的参考。'; limitation='试听来自原片混合音轨，可能包含对白、音乐和环境声；没有分离出独立声线或音效。'
      field('声音听起来怎样','description'); field('台词原文（未翻译）','transcript'); field('情绪与表达','delivery'); break
    case 'music_structure':
      purpose='参考配器、节奏和情绪，为新项目的配乐提出具体要求。'; limitation='试听来自原片混合音轨，尚不是单独的配乐或分轨文件。'
      field('音乐说明','description'); field('节奏','rhythm'); field('情绪','mood');
      items('听到的乐器层次','instrument_layers',v => { const name=text(v.instrument); return ({'Acoustic Guitar':'原声吉他','Percussion':'打击乐'} as Record<string,string>)[name]||name }); break
    case 'script_unit':
      purpose='参考故事设定、冲突与转折，在项目中发展新的故事。'; limitation='这是分析梗概，不能当作完整逐字剧本或已经核实的事实。'
      field('故事梗概','text'); field('故事设定','premise'); field('开场吸引点','hook'); field('主要冲突','conflict'); field('转折','reversal'); field('人物要得到什么','goal'); field('失败的代价','stakes'); field('主题','theme'); break
    case 'style':
      purpose='把可读的风格规则作为项目的美术、镜头和叙事参考。'; limitation='这些是分析归纳的规则，实际效果需要新项目验证。'
      field('风格印象','scope_note'); items('怎样做出这种感觉','rules',v=>text(v.instruction)); items('可以借鉴的例子','positive_examples'); items('不符合这种风格的例子','counterexamples'); break
    case 'method': case 'skill_candidate':
      purpose='按步骤借鉴创作方法，将参考说明交给项目智能体执行。'; limitation='方法说明尚不等于已经安装、可执行或经过验证的技能。'
      field('这个方法解决什么','objective'); field('适合什么创作','applicability'); items('先准备什么','inputs'); items('操作步骤','steps',v=>text(v.instruction)); items('应该得到什么','expected_outputs'); items('不适用的例子','counterexamples'); items('注意事项','constraints',v=>[text(v.condition),text(v.effect)].filter(Boolean).join('：')); break
    default:
      field('内容说明','description'); field('用途','purpose'); limitation='这一类型尚缺可读预览，不能仅凭技术记录认定已可用。'
  }
  if (!sections.length && r.basis_note) sections.push({title:'分析说明',lines:[r.basis_note]})
  return {purpose,limitation,sections}
}
export function readingText(r: LearningResult): string {
  const reading=resultReading(r)
  return [r.title, ...reading.sections.map(s=>`${s.title}\n${s.lines.join('\n')}`), `怎么用\n${reading.purpose}`, `使用边界\n${reading.limitation}`].join('\n\n')
}
