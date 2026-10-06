export type SceneType = 'slide' | 'quiz' | 'interactive' | 'complete'
export type SceneStatus = 'pending' | 'generating' | 'ready' | 'failed' | 'complete'

export interface SlideColumn { title: string; items: string[] }
/** 讲解页里的一个内容块；`type` 与后端内容块类型对应，前端按类型给不同版式。 */
export interface SlideBlock { key: string; type: string; text: string; columns?: SlideColumn[] }
/**
 * 讲解页正文。
 *
 * 后端把整页正文放在有序的内容块里，第一个块通常是与页面标题同名的 `heading`；
 * 那个块在这里被剔除成 `titleKey`，避免标题在页面里出现两次。
 */
export interface SlideContent {
  /** 与页面标题重复、已从正文剔除的标题块 key；讲稿讲到它时高亮页面大标题 */
  titleKey: string
  /** 首个不同于页面标题的标题块，作为副标题显示；没有则为空串 */
  lead: string
  /** 正文块，按原始顺序排列 */
  blocks: SlideBlock[]
  /** 收尾结论：正文最后一个 `callout` 的正文；没有则为空串，渲染成底部结论条 */
  takeaway: string
  takeawayKey: string
}
/**
 * 一道选择题。
 *
 * 选项与答案都取这个块上的 `interaction`：`options` 是候选项，`answer` 是正确答案的
 * **原文**（必须与某一个选项完全一致，不是下标），解析放在 `config.explanation`。
 */
export interface QuizQuestion {
  key: string
  question: string
  options: string[]
  answer: string
  explanation: string
}
/** 一页可以有多道选择题，每个 `type` 为 `quiz` 的内容块对应一道。 */
export interface QuizContent { questions: QuizQuestion[] }
/**
 * 交互页的正文。
 *
 * 它是一份完整、自包含的 HTML 文档，只能由沙箱 iframe 渲染；`blocks` 里只留一个
 * 讲解锚块给讲稿挂钩子。旧课堂没有这一列，交互控件还写在 `blocks[].interaction` 里。
 */
export interface InteractiveContent { html: string }

export interface Scene {
  id: string
  title: string
  type: SceneType
  status: SceneStatus
  slide?: SlideContent
  quiz?: QuizContent
  interactive?: InteractiveContent
  blocks?: { key?: string; type?: string; content?: string; text?: string; columns?: { title?: string; items?: string[] }[]; interaction?: { kind?: string; controls?: Record<string, unknown>[]; options?: string[]; answer?: string; config?: Record<string, unknown> } }[]
}

export interface Classroom { id: string; title: string; scenes: Scene[] }

export const SCENE_TYPE_STYLES: Record<SceneType, { ring: string; gradient: string }> = {
  slide: { ring: 'ring-black/5', gradient: 'from-brand-100 to-brand-200' },
  quiz: { ring: 'ring-gold-200', gradient: 'from-orange-100 to-gold-100' },
  interactive: { ring: 'ring-emerald-200', gradient: 'from-emerald-100 to-brand-100' },
  complete: { ring: 'ring-gold-200', gradient: 'from-gold-100 to-orange-100' },
}

/** 取交互页的沙箱 HTML；不是交互页、或旧课堂只有控件配置时返回空串。 */
export function interactiveHTML(scene: Scene): string {
  if (scene.type !== 'interactive') return ''
  return scene.interactive?.html?.trim() ?? ''
}
