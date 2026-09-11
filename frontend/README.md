# Narra Frontend

Narra 多智能体智能讲解平台的前端，**Vue 3 + Vite** 重构版。

> 原前端为 Next.js + TypeScript（项目名 `openmaic`），已做"保 UI、删代码"清理，
> UI 设计完整保留并提取为文档：**[`docs/UI-还原文档.md`](./docs/UI-还原文档.md)**。

## 技术栈

| 领域 | 选型 | 对应原项目 |
|---|---|---|
| 框架 | Vue 3.5（`<script setup>` + TS） | React 19 |
| 构建 | Vite 8 | Next.js 15 |
| 样式 | Tailwind CSS v4（CSS-first，无 `tailwind.config.js`） | 同 |
| 路由 | Vue Router 4 | App Router |
| 状态 | Pinia | React state / context |
| i18n | vue-i18n 11 | 自研 hook |
| 图标 | lucide-vue-next | lucide-react |
| 动画 | motion-v | motion/react |
| UI 基础件 | reka-ui（Radix Vue） | shadcn/ui (Radix) |
| Toast | vue-sonner | sonner |
| 字体 | @fontsource-variable/inter | 同 |

## 快速开始

```bash
npm install
npm run dev      # http://localhost:5173
npm run build    # 类型检查 + 构建
npm run preview  # 预览构建产物
```

> 开发期 `/api` 已代理到 `http://localhost:8080`（Narra 的 Go 后端），
> 见 `vite.config.ts` 的 `server.proxy`。

## 目录结构

```
frontend/
├── assets/                     # 静态资源（演示 gif、banner 等，24 个）
├── docs/
│   └── UI-还原文档.md           # ★ 重写界面的唯一依据（1200 行）
├── public/                     # 站点资源：avatars/ logos/ vendor/ 图标字体
├── src/
│   ├── components/             # 组件（待填充）
│   ├── composables/            # 组合式函数（含 useTheme）
│   ├── i18n/
│   │   ├── index.ts            # vue-i18n 实例 + 语言切换
│   │   └── locales/            # zh-CN.ts / en-US.ts
│   ├── lib/                    # 工具函数（待填充）
│   ├── router/index.ts         # 路由表
│   ├── stores/                 # Pinia stores（待填充）
│   ├── styles/
│   │   └── globals.css         # ★ 设计 token 全在这里
│   ├── views/                  # 页面
│   ├── App.vue
│   ├── main.ts
│   └── env.d.ts
├── index.html
├── tsconfig*.json
└── vite.config.ts
```

## 路由

| 路径 | 页面 | 对应原文件 |
|---|---|---|
| `/` | 首页 / 落地页 | `app/page.tsx` |
| `/workspace` | Pro 工作区（三栏） | `app/workspace/page.tsx` |
| `/classroom/:id` | 课堂播放页 | `app/classroom/[id]/page.tsx` |
| `/generation-preview` | 生成预览页 | `app/generation-preview/page.tsx` |
| `/workbench/new` | 旧链接兼容桥 → `/workspace` | `app/workbench/new/page.tsx` |

页面组件全部懒加载。路由标题写在 `meta.title`，`afterEach` 里同步 `document.title`。

## 设计系统

**品牌主色 `#722ed1`**（紫），所有 token 定义在 `src/styles/globals.css`：

- 颜色：OKLCH 色彩空间，亮/暗两套 CSS 变量
- 圆角：`--radius: 0.625rem`，派生 `--radius-sm` ~ `--radius-4xl`
- 暗色模式：`.dark` class 变体（**非** media query），三态 `light` / `dark` / `system`
- 字体：`--font-sans: 'Inter Variable', ...`

组件里直接使用语义类：`bg-background`、`text-muted-foreground`、`bg-primary`、
`border-border`、`rounded-2xl`、`rounded-full` 等。

### 主题切换

`src/composables/useTheme.ts`：

```ts
import { useTheme } from '@/composables/useTheme'

const { mode, isDark, setMode, setLight, setDark, setSystem } = useTheme()
```

持久化键 `narra-theme`；`index.html` 内有首帧内联脚本防闪白。

### 语言切换

`src/i18n/index.ts`：

```ts
import { setLocale } from '@/i18n'

setLocale('zh-CN')   // 或 'en-US'
```

持久化键 `narra-locale`。目前仅 `zh-CN` / `en-US` 两套，
原项目支持 12 种语言，按需补（见文档 §9 决策项）。

## 后续开发

按 `docs/UI-还原文档.md` 从首页开始：

- §5 首页 —— Composer、GreetingBar、AgentBar、GenerationToolbar、最近学习折叠区
- §6 课堂页 —— 三栏 + 硬编码尺寸（Header 80px、Roundtable 192px、侧栏 220px / 聊天 340px）
- §7 生成预览页 —— 4 分支 + 6 种 StepVisualizer + 大纲编辑器
- §8 Pro 工作区 —— 三栏折叠 + `--ws-*` token（需移植 `workspace-shell.css`）
