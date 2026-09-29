/**
 * 后端场景响应 → 前端视图模型的唯一入口。
 *
 * 场景详情与列表封面给出的是同一份内容（`content.blocks` 与 `interactive_html`），
 * 只是各自多带一些用不上的字段，所以这里只依赖两者的公共部分：课堂主画布与首页卡片
 * 共用同一套组装逻辑，免得「课堂里怎么画」和「卡片上怎么画」慢慢长成两个样子。
 */
import type { Scene, SlideContent } from '@/types/scene'

/** 讲解页里的一个内容块；与后端 `content.blocks` 的每一项一一对应。 */
export interface SceneBlockDTO {
  key?: string
  type?: string
  content?: string
  text?: string
  interaction?: { kind?: string; controls?: Record<string, unknown>[]; options?: string[]; answer?: string; config?: Record<string, unknown> }
}

/** 组装一个场景所需的最小输入；场景详情与列表封面都满足它。 */
export interface SceneContentSource {
  id: number
  type: string
  title: string
  status: string
  content: { blocks?: SceneBlockDTO[] }
  /** 交互页的完整 HTML 文档；其余场景类型为空串 */
  interactive_html: string
}

/** 比较标题时忽略空白与结尾标点。 */
function sameAsTitle(text: string, title: string): boolean {
  const normalize = (value: string) => value.replace(/\s+/g, '').replace(/[：:。.、]+$/, '')
  return normalize(text) === normalize(title)
}

/**
 * 组装讲解页正文：剔除与页面标题重复的标题块，拆出副标题与收尾结论。
 *
 * 后端每页的第一个块几乎都是与页面标题同名的 heading，直接渲染会让标题出现两次，
 * 所以这里把它记成 `titleKey`；剩下的首块若仍是 heading，说明它是比标题更具体的一句话，
 * 当作副标题；正文末尾的 callout 拿出来做结论条。朗读高亮已删，`titleKey` 只留作版式依据。
 */
export function toSlideContent(title: string, blocks: SceneBlockDTO[]): SlideContent {
  const kept = blocks
    .map((block) => ({ key: block.key ?? '', type: block.type ?? 'paragraph', text: (block.content ?? block.text ?? '').trim() }))
    .filter((block) => block.text.length > 0)

  const titleIndex = kept.findIndex((block) => block.type === 'heading' && sameAsTitle(block.text, title))
  const titleKey = titleIndex >= 0 ? kept[titleIndex].key : ''
  const body = kept.filter((_, index) => index !== titleIndex)

  const lead = body[0]?.type === 'heading' ? body.shift()!.text : ''
  const last = body[body.length - 1]
  const takeaway = last?.type === 'callout' ? body.pop()!.text : ''

  return { titleKey, lead, blocks: body, takeaway, takeawayKey: takeaway ? last.key : '' }
}

/** 把一个场景响应的内容组装成前端视图模型；三种类型的正文来源各不相同。 */
export function toScene(item: SceneContentSource): Scene {
  const type = (item.type as Scene['type']) || 'slide'
  const blocks = item.content.blocks ?? []
  const scene: Scene = {
    id: String(item.id), title: item.title, type, status: item.status as Scene['status'], blocks,
  }
  if (type === 'interactive') {
    scene.interactive = { html: item.interactive_html ?? '' }
  } else if (type === 'quiz') {
    scene.quiz = {
      questions: blocks
        .filter((block) => block.type === 'quiz')
        .map((block) => ({
          key: block.key ?? '',
          question: block.content ?? block.text ?? '',
          options: Array.isArray(block.interaction?.options) ? block.interaction.options : [],
          answer: block.interaction?.answer ?? '',
          explanation: typeof block.interaction?.config?.explanation === 'string' ? block.interaction.config.explanation : '',
        })),
    }
  } else {
    scene.slide = toSlideContent(item.title, blocks)
  }
  return scene
}
