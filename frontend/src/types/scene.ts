export type SceneType = 'slide' | 'quiz' | 'interactive' | 'pbl' | 'complete'
export type SceneStatus = 'pending' | 'generating' | 'ready' | 'failed' | 'complete'

export interface SlideContent { heading: string; bullets: string[]; bulletKeys?: string[]; accent?: string }
export interface QuizContent { question: string; options: string[]; answer: number }
export interface InteractiveContent { url: string; heading: string; note: string }
export interface PblContent { heading: string; columns: { title: string; items: string[] }[] }

export interface Scene {
  id: string
  title: string
  type: SceneType
  status: SceneStatus
  slide?: SlideContent
  quiz?: QuizContent
  interactive?: InteractiveContent
  pbl?: PblContent
  blocks?: { key?: string; type?: string; content?: string; text?: string; interaction?: { kind?: string; controls?: Record<string, unknown>[]; options?: string[]; answer?: string; config?: Record<string, unknown> } }[]
}

export interface Classroom { id: string; title: string; scenes: Scene[] }

export const SCENE_TYPE_STYLES: Record<SceneType, { ring: string; gradient: string }> = {
  slide: { ring: 'ring-black/5', gradient: 'from-violet-100 to-blue-100' },
  quiz: { ring: 'ring-amber-200', gradient: 'from-orange-100 to-amber-100' },
  interactive: { ring: 'ring-emerald-200', gradient: 'from-emerald-100 to-teal-100' },
  pbl: { ring: 'ring-blue-200', gradient: 'from-blue-100 to-indigo-100' },
  complete: { ring: 'ring-amber-200', gradient: 'from-amber-100 to-orange-100' },
}
