/**
 * Pro 工作区 mock 数据。
 *
 * 数据形态即未来的前后端契约：
 *   GET /api/workspace/sessions          → SessionItem[]
 *   GET /api/workspace/courses           → CourseItem[]
 *   GET /api/workspace/sessions/:id      → { session, messages }
 *   GET /api/courses/:id/pages           → CoursewarePage[]
 *
 * 课件页 = 完整 HTML 字符串（1280×720 基准，内联样式），
 * 由 SlideFrame 通过 sandboxed iframe srcDoc 渲染 —— 与 OpenMAIC
 * 互动场景的承载方式一致（见 _openmaic_source/components/scene-renderers）。
 */

export interface SessionItem {
  id: string
  title: string
  /** 相对时间文案，如 "2 小时前"；后端接入后换成时间戳 */
  updatedAt: string
}

export interface CourseItem {
  id: string
  title: string
  pageCount: number
}

/** 单页课件：完整 HTML 文档字符串 */
export interface CoursewarePage {
  id: string
  title: string
  html: string
}

export interface WorkspaceCourse extends CourseItem {
  pages: CoursewarePage[]
}

export interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  text: string
  /** AI 回复里附带的课程卡片（点击打开课件区对应课程） */
  courseCard?: { courseId: string; title: string; pageCount: number }
}

/* ── 课件 HTML 公共骨架 ─────────────────────────────────────────────── */

const PAGE_BASE = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8" />
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  html, body { width: 1280px; height: 720px; overflow: hidden; }
  body {
    font-family: "PingFang SC", "Microsoft YaHei", "Segoe UI", sans-serif;
    background: #ffffff; color: #1f2329;
  }
  .page { width: 1280px; height: 720px; padding: 64px 80px; display: flex; flex-direction: column; position: relative; }
  .kicker { font-size: 15px; font-weight: 600; letter-spacing: .12em; color: #722ed1; text-transform: uppercase; }
  h1 { font-size: 44px; font-weight: 800; color: #171a1f; margin-top: 10px; }
  .sub { font-size: 19px; color: #646a73; margin-top: 10px; }
  .footer-band {
    margin-top: auto; background: linear-gradient(90deg, #4f6ef7, #722ed1);
    color: #fff; font-size: 19px; font-weight: 600; text-align: center;
    padding: 18px 32px; border-radius: 10px;
  }
  .card-grid { display: grid; gap: 18px; margin-top: 36px; }
  .card {
    background: #f7f8fa; border: 1px solid #eef0f3; border-radius: 12px; padding: 22px 26px;
  }
  .card h3 { font-size: 21px; color: #2b55d4; font-weight: 700; }
  .card p { font-size: 16px; color: #4e5560; margin-top: 8px; line-height: 1.6; }
  pre {
    background: #14181f; color: #d6e2ff; border-radius: 12px; padding: 24px 28px;
    font-family: "JetBrains Mono", Consolas, monospace; font-size: 16px; line-height: 1.7;
  }
  pre .k { color: #7dd3fc; } pre .s { color: #a5d6a7; }
</style>
</head>
<body>
`

const PAGE_END = `
</body>
</html>`

/* ── 课程一：Agent Tool 概念与使用入门（6 页） ─────────────────────── */

const AGENT_TOOL_PAGES: CoursewarePage[] = [
  {
    id: 'at-1',
    title: '课程导览',
    html:
      PAGE_BASE +
      `<div class="page" style="background: linear-gradient(135deg, #f6f2ff 0%, #eef4ff 100%); justify-content: center;">
  <span class="kicker">Agent Tool 概念与使用入门</span>
  <h1 style="font-size: 56px; line-height: 1.25;">让 Agent 学会<br />使用工具</h1>
  <p class="sub" style="max-width: 620px;">从 Tool 的组成结构讲起，理解 Function Calling 机制，最后亲手完成一次完整的工具调用。</p>
  <div style="display: flex; gap: 12px; margin-top: 40px;">
    <span style="background:#722ed1; color:#fff; font-size:15px; font-weight:600; padding:10px 22px; border-radius:999px;">6 个章节</span>
    <span style="background:#fff; color:#722ed1; border:1px solid #d6bef5; font-size:15px; font-weight:600; padding:10px 22px; border-radius:999px;">约 25 分钟</span>
    <span style="background:#fff; color:#722ed1; border:1px solid #d6bef5; font-size:15px; font-weight:600; padding:10px 22px; border-radius:999px;">含随堂检测</span>
  </div>
</div>` +
      PAGE_END,
  },
  {
    id: 'at-2',
    title: '什么是 Agent 的 Tool',
    html:
      PAGE_BASE +
      `<div class="page">
  <span class="kicker">第一章</span>
  <h1>什么是 Agent 的 Tool</h1>
  <p class="sub">工具是 Agent 伸向真实世界的手</p>
  <div class="card-grid" style="grid-template-columns: 1fr 1fr;">
    <div class="card">
      <h3>模型只会"说"</h3>
      <p>LLM 本身只能生成文本：它知道今天天气该怎么查，却没办法真的去查。没有工具的 Agent，就像一个被关在房间里博学的顾问。</p>
    </div>
    <div class="card">
      <h3>Tool 让它"做"</h3>
      <p>Tool 是一段可被模型调用的函数 / API：查天气、搜资料、读写文件、执行代码……模型决定"何时调、传什么参"，程序负责真正执行。</p>
    </div>
    <div class="card">
      <h3>调用是一个循环</h3>
      <p>模型提出调用请求 → 宿主程序执行 → 把结果喂回模型 → 模型继续推理。这个 Agent Loop 可以反复多轮，直到任务完成。</p>
    </div>
    <div class="card">
      <h3>本课聚焦三件事</h3>
      <p>Tool 由哪几部分组成、Function Calling 的协议长什么样、以及一次完整调用在系统里是怎么流转的。</p>
    </div>
  </div>
  <div class="footer-band">Tool = 模型决策 + 程序执行 的契约接口</div>
</div>` +
      PAGE_END,
  },
  {
    id: 'at-3',
    title: 'Tool 的组成结构',
    html:
      PAGE_BASE +
      `<div class="page">
  <span class="kicker">第二章</span>
  <h1>Tool 的组成结构</h1>
  <p class="sub">解析 Agent 工具背后的核心组件</p>
  <div style="display: flex; gap: 28px; margin-top: 30px; flex: 1; min-height: 0;">
    <div style="flex: 1; display: flex; flex-direction: column; gap: 16px;">
      <div class="card">
        <h3>名称 (Name)</h3>
        <p>工具的唯一标识符，用于 Agent 调用。</p>
      </div>
      <div class="card">
        <h3>描述 (Description)</h3>
        <p>关键：清晰的描述让 Agent 知道何时使用该工具，决定调用的准确性。</p>
      </div>
      <div class="card">
        <h3>参数 Schema (Parameters)</h3>
        <p>定义工具接受的输入格式，包括字段名、类型及必填项。</p>
      </div>
    </div>
    <pre style="flex: 1; display: flex; align-items: center;">{
  <span class="k">"name"</span>: <span class="s">"get_weather"</span>,
  <span class="k">"description"</span>: <span class="s">"获取指定城市的实时天气"</span>,
  <span class="k">"parameters"</span>: {
    <span class="k">"type"</span>: <span class="s">"object"</span>,
    <span class="k">"properties"</span>: {
      <span class="k">"city"</span>: { <span class="k">"type"</span>: <span class="s">"string"</span> }
    }
  }
}</pre>
  </div>
  <div class="footer-band">名称用于识别，描述用于决策，Schema 用于规范输入</div>
</div>` +
      PAGE_END,
  },
  {
    id: 'at-4',
    title: 'Tool 基础理解检测',
    html:
      PAGE_BASE +
      `<div class="page" style="background: linear-gradient(135deg, #fff8f0 0%, #fff3e8 100%);">
  <span class="kicker" style="color:#d48806;">随堂检测</span>
  <h1>Tool 基础理解检测</h1>
  <p class="sub">下面哪个部分最影响 Agent「何时」选择调用一个工具？</p>
  <div class="card-grid" style="grid-template-columns: 1fr 1fr; max-width: 880px;">
    <div class="card" style="background:#fff;"><h3 style="color:#171a1f;">A. 工具名称的长度</h3><p>名字长短对调用决策几乎没有影响。</p></div>
    <div class="card" style="background:#fff; border: 2px solid #fa8c16;"><h3 style="color:#d46b08;">B. 工具描述 (Description) ✓</h3><p>描述是模型判断"什么时候该用我"的唯一依据。</p></div>
    <div class="card" style="background:#fff;"><h3 style="color:#171a1f;">C. 参数的数量</h3><p>参数多少影响的是调用成本，不是触发时机。</p></div>
    <div class="card" style="background:#fff;"><h3 style="color:#171a1f;">D. 返回值的格式</h3><p>返回格式影响的是结果如何被理解。</p></div>
  </div>
  <div class="footer-band" style="background: linear-gradient(90deg, #fa8c16, #f5222d);">写描述时，把"什么时候该用它"说清楚</div>
</div>` +
      PAGE_END,
  },
  {
    id: 'at-5',
    title: 'Function Calling 机制',
    html:
      PAGE_BASE +
      `<div class="page">
  <span class="kicker">第三章</span>
  <h1>Function Calling 机制</h1>
  <p class="sub">模型与宿主程序之间的标准化握手协议</p>
  <div style="display: flex; align-items: stretch; gap: 14px; margin-top: 40px;">
    <div class="card" style="flex:1; text-align:center;"><h3>① 用户提问</h3><p>"北京今天天气怎么样？"</p></div>
    <div style="align-self:center; font-size:26px; color:#722ed1;">→</div>
    <div class="card" style="flex:1; text-align:center;"><h3>② 模型决策</h3><p>输出 tool_call：<br />get_weather(city="北京")</p></div>
    <div style="align-self:center; font-size:26px; color:#722ed1;">→</div>
    <div class="card" style="flex:1; text-align:center;"><h3>③ 宿主执行</h3><p>真正调用天气 API，拿到 25°C 晴</p></div>
    <div style="align-self:center; font-size:26px; color:#722ed1;">→</div>
    <div class="card" style="flex:1; text-align:center;"><h3>④ 结果回填</h3><p>模型基于结果生成自然语言回答</p></div>
  </div>
  <div class="card" style="margin-top: 28px;">
    <h3>关键点</h3>
    <p>模型从不"执行"函数——它只生成一段结构化的调用请求（JSON）。执不执行、怎么执行，永远由宿主程序决定，这也是安全边界所在。</p>
  </div>
  <div class="footer-band">模型产出意图，宿主掌控执行</div>
</div>` +
      PAGE_END,
  },
  {
    id: 'at-6',
    title: 'Tool 调用流程演示',
    html:
      PAGE_BASE +
      `<div class="page" style="background: linear-gradient(135deg, #14181f 0%, #1f2430 100%); color:#e6e9ef;">
  <span class="kicker" style="color:#b37feb;">实战演示</span>
  <h1 style="color:#ffffff;">一次完整的 Tool 调用</h1>
  <p class="sub" style="color:#9aa1ad;">多轮 Agent Loop 的真实消息序列</p>
  <pre style="margin-top: 30px; background:#0d1117; flex: 1; font-size: 15px;">
<span style="color:#8b949e;">// Round 1 —— 模型请求调用</span>
assistant → tool_call: get_weather({ "city": "北京" })

<span style="color:#8b949e;">// Round 1 —— 宿主执行并回填</span>
tool      → { "temp": 25, "condition": "晴", "humidity": 40 }

<span style="color:#8b949e;">// Round 2 —— 模型基于结果作答</span>
assistant → "北京今天晴，气温 25°C，湿度 40%，适合出行。"
  </pre>
  <div class="footer-band">循环往复，直到模型认为任务完成</div>
</div>` +
      PAGE_END,
  },
]

/* ── 课程二：RAG 检索增强生成实战（3 页） ──────────────────────────── */

const RAG_PAGES: CoursewarePage[] = [
  {
    id: 'rag-1',
    title: '课程导览',
    html:
      PAGE_BASE +
      `<div class="page" style="background: linear-gradient(135deg, #eefcf3 0%, #e8f7ff 100%); justify-content: center;">
  <span class="kicker" style="color:#0e9f6e;">RAG 检索增强生成实战</span>
  <h1 style="font-size: 56px; line-height: 1.25;">让模型回答<br />它没学过的问题</h1>
  <p class="sub" style="max-width: 620px;">Embedding → 向量库 → 检索 → 生成，一条链路走完 RAG 的核心闭环。</p>
</div>` +
      PAGE_END,
  },
  {
    id: 'rag-2',
    title: 'RAG 的核心链路',
    html:
      PAGE_BASE +
      `<div class="page">
  <span class="kicker" style="color:#0e9f6e;">第一章</span>
  <h1>RAG 的核心链路</h1>
  <p class="sub">先检索，再生成：把"开卷考试"引入大模型</p>
  <div style="display: flex; align-items: stretch; gap: 14px; margin-top: 40px;">
    <div class="card" style="flex:1; text-align:center;"><h3 style="color:#0e9f6e;">① 切分入库</h3><p>文档切块，Embedding 成向量写入向量库</p></div>
    <div style="align-self:center; font-size:26px; color:#0e9f6e;">→</div>
    <div class="card" style="flex:1; text-align:center;"><h3 style="color:#0e9f6e;">② 相似检索</h3><p>问题向量化，召回 Top-K 相关片段</p></div>
    <div style="align-self:center; font-size:26px; color:#0e9f6e;">→</div>
    <div class="card" style="flex:1; text-align:center;"><h3 style="color:#0e9f6e;">③ 拼装上下文</h3><p>片段 + 问题组成 Prompt</p></div>
    <div style="align-self:center; font-size:26px; color:#0e9f6e;">→</div>
    <div class="card" style="flex:1; text-align:center;"><h3 style="color:#0e9f6e;">④ 生成答案</h3><p>模型基于检索内容作答，可附引用</p></div>
  </div>
  <div class="footer-band" style="background: linear-gradient(90deg, #0e9f6e, #0ea5e9);">检索决定下限，生成决定上限</div>
</div>` +
      PAGE_END,
  },
  {
    id: 'rag-3',
    title: 'Embedding 与向量检索',
    html:
      PAGE_BASE +
      `<div class="page">
  <span class="kicker" style="color:#0e9f6e;">第二章</span>
  <h1>Embedding 与向量检索</h1>
  <p class="sub">把语义翻译成距离</p>
  <div class="card-grid" style="grid-template-columns: 1fr 1fr;">
    <div class="card"><h3 style="color:#0e9f6e;">Embedding 是什么</h3><p>把一段文本映射成高维向量；语义相近的文本，向量在空间中的距离也近。</p></div>
    <div class="card"><h3 style="color:#0e9f6e;">相似度怎么算</h3><p>常用余弦相似度：越接近 1 越相关。检索 = 在向量库里找距离问题最近的 K 个片段。</p></div>
    <div class="card"><h3 style="color:#0e9f6e;"> chunk 切分的讲究</h3><p>太大：噪声多、召回不准；太小：语义不完整。常见做法 300~500 tokens + 重叠窗口。</p></div>
    <div class="card"><h3 style="color:#0e9f6e;">为什么需要向量库</h3><p>暴力比对百万级向量太慢，向量数据库用 ANN 索引（如 HNSW）把检索压到毫秒级。</p></div>
  </div>
  <div class="footer-band" style="background: linear-gradient(90deg, #0e9f6e, #0ea5e9);">语义检索的精度，从切分那一刻就注定了</div>
</div>` +
      PAGE_END,
  },
]

/* ── 对外数据 ───────────────────────────────────────────────────────── */

export const WORKSPACE_COURSES: WorkspaceCourse[] = [
  {
    id: 'course-agent-tool',
    title: 'Agent Tool 概念与使用入门',
    pageCount: AGENT_TOOL_PAGES.length,
    pages: AGENT_TOOL_PAGES,
  },
  {
    id: 'course-rag',
    title: 'RAG 检索增强生成实战',
    pageCount: RAG_PAGES.length,
    pages: RAG_PAGES,
  },
]

export const WORKSPACE_SESSIONS: SessionItem[] = [
  { id: 'session-agent-tool', title: 'Agent Tool 概念与使用入门', updatedAt: '刚刚' },
  { id: 'session-rag', title: 'RAG 检索增强生成实战', updatedAt: '2 小时前' },
]

export const WORKSPACE_MESSAGES: Record<string, ChatMessage[]> = {
  'session-agent-tool': [
    {
      id: 'm1',
      role: 'user',
      text: '帮我做一门课，讲清楚 Agent 的 Tool 是什么，要有概念讲解、随堂检测和一个完整的调用流程演示。',
    },
    {
      id: 'm2',
      role: 'assistant',
      text: '好的，我已经为你生成了一门 6 页的课程，结构如下：\n1. 课程导览 —— 课程目标与章节安排\n2. 什么是 Agent 的 Tool —— 概念与 Agent Loop\n3. Tool 的组成结构 —— 名称 / 描述 / 参数 Schema\n4. Tool 基础理解检测 —— 随堂选择题\n5. Function Calling 机制 —— 四步握手协议\n6. Tool 调用流程演示 —— 多轮消息序列实录\n\n点击下面的卡片即可在右侧打开课件，也可以继续告诉我你想调整的地方。',
      courseCard: { courseId: 'course-agent-tool', title: 'Agent Tool 概念与使用入门', pageCount: 6 },
    },
    {
      id: 'm3',
      role: 'user',
      text: '第 3 页的代码块能换成 TypeScript 类型定义的写法吗？',
    },
    {
      id: 'm4',
      role: 'assistant',
      text: '可以，我把第 3 页「Tool 的组成结构」右侧的示例从 JSON 换成了 TypeScript interface 的写法，并保留了三要素的对应关系。你看看效果，如果需要我也可以改成 Python 的 TypedDict 风格。',
    },
  ],
  'session-rag': [
    {
      id: 'm1',
      role: 'user',
      text: '做一门 RAG 入门课，重点讲检索链路。',
    },
    {
      id: 'm2',
      role: 'assistant',
      text: '已生成 3 页课程：导览、RAG 核心链路四步图、Embedding 与向量检索要点。右侧可直接查看。',
      courseCard: { courseId: 'course-rag', title: 'RAG 检索增强生成实战', pageCount: 3 },
    },
  ],
}

export function getCourse(id: string): WorkspaceCourse | undefined {
  return WORKSPACE_COURSES.find((c) => c.id === id)
}

export function getSessionMessages(sessionId: string): ChatMessage[] {
  return WORKSPACE_MESSAGES[sessionId] ?? []
}
