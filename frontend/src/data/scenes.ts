/**
 * 课堂场景数据。
 *
 * 原项目由后端生成并流式下发（`@openmaic/renderer` 包已被删除），
 * 这里用 mock 数据还原结构。接入 Go 后端后改为：
 *   GET /api/classrooms/:id        → { title, scenes }
 *   GET /api/classrooms/:id/stream → SSE 逐场景推送
 */

export type SceneType = 'slide' | 'quiz' | 'interactive' | 'pbl' | 'complete'

export type SceneStatus = 'pending' | 'generating' | 'ready' | 'failed' | 'complete'

export interface SlideContent {
  heading: string
  bullets: string[]
  /** 右侧装饰用的强调短语 */
  accent?: string
}

export interface QuizContent {
  question: string
  options: string[]
  /** 正确选项下标 */
  answer: number
}

export interface InteractiveContent {
  /** 伪浏览器地址栏文案 */
  url: string
  heading: string
  note: string
}

export interface PblContent {
  heading: string
  columns: { title: string; items: string[] }[]
}

export interface Scene {
  id: string
  title: string
  type: SceneType
  status: SceneStatus
  slide?: SlideContent
  quiz?: QuizContent
  interactive?: InteractiveContent
  pbl?: PblContent
}

export interface Classroom {
  id: string
  title: string
  scenes: Scene[]
}

/** 场景类型 → 侧栏缩略图配色（文档 §6.4） */
export const SCENE_TYPE_STYLES: Record<
  SceneType,
  { ring: string; gradient: string }
> = {
  slide: { ring: 'ring-black/5', gradient: 'from-violet-100 to-blue-100' },
  quiz: { ring: 'ring-amber-200', gradient: 'from-orange-100 to-amber-100' },
  interactive: { ring: 'ring-emerald-200', gradient: 'from-emerald-100 to-teal-100' },
  pbl: { ring: 'ring-blue-200', gradient: 'from-blue-100 to-indigo-100' },
  complete: { ring: 'ring-amber-200', gradient: 'from-amber-100 to-orange-100' },
}

const CLASSROOM_MOCK: Classroom = {
  id: 'demo',
  title: '从零学 Python：30 分钟写出第一个程序',
  scenes: [
    {
      id: 's1',
      title: '课程导览：今天要学什么',
      type: 'slide',
      status: 'ready',
      slide: {
        heading: '从零学 Python',
        bullets: [
          '为什么 Python 适合作为第一门编程语言',
          '搭建你的运行环境（只要 3 分钟）',
          '写出第一个程序：Hello, World',
          '常见报错与排查思路',
        ],
        accent: '30 分钟上手',
      },
    },
    {
      id: 's2',
      title: '变量与数据类型',
      type: 'slide',
      status: 'ready',
      slide: {
        heading: '变量：给数据起个名字',
        bullets: [
          '变量是内存中的一块空间，用名字来访问',
          'Python 是动态类型：赋值时才决定类型',
          '整数 int / 浮点 float / 字符串 str / 布尔 bool',
          '用 type() 查看任意值的类型',
        ],
        accent: 'name = "Narra"',
      },
    },
    {
      id: 's3',
      title: '小测验：下面哪个是合法变量名？',
      type: 'quiz',
      status: 'ready',
      quiz: {
        question: '下面哪个是合法的 Python 变量名？',
        options: ['2name', 'my-name', 'my_name', 'class'],
        answer: 2,
      },
    },
    {
      id: 's4',
      title: '动手试试：在线解释器',
      type: 'interactive',
      status: 'ready',
      interactive: {
        url: 'https://python.narra.dev/playground',
        heading: '在浏览器里直接跑 Python',
        note: '试着把 name 改成你自己的名字，再点运行。',
      },
    },
    {
      id: 's5',
      title: '项目实战：做一个待办清单',
      type: 'pbl',
      status: 'ready',
      pbl: {
        heading: '项目：命令行待办清单',
        columns: [
          { title: '待开始', items: ['设计数据结构', '确定交互方式'] },
          { title: '进行中', items: ['实现添加功能'] },
          { title: '已完成', items: ['打印欢迎语'] },
        ],
      },
    },
    {
      id: 's6',
      title: '课程完成',
      type: 'complete',
      status: 'pending',
    },
  ],
}

export function getClassroom(id: string): Classroom {
  // 当前只有一份 mock；接入后端后按 id 拉取
  return { ...CLASSROOM_MOCK, id }
}
