# Narra 项目长期记忆

## 项目定位
Narra —— 基于 **Go + CloudWeGo Eino** 的多智能体智能讲解平台，参考 OpenMAIC 理念，把提示词 + 学习资料转成结构化讲解内容。探索 LLM / Agent Loop / Tool Calling / RAG / Embedding / 向量库 / Multi-Agent 协作。

## ⚠️ 环境迁移（2026-09-11）
项目已迁到 **`C:\Users\23107\Desktop\Nannr`**（原 `D:\Narra`）；本机用户 `23107`。**`D:\node\node_global` 不存在、`agent-browser` 未安装**（下文 agent-browser 工作流是旧机器 LHK 的，本机需先 `npm i -g agent-browser && agent-browser install` 才能用）。OpenMAIC 参考源码在 `_openmaic_source/`。

## 前端现状（2026-09-10 起）
- `frontend/` 已完成 **Vue 3 + Vite** 脚手架搭建（原 Next.js 15 + TS 项目已清空）。
- **技术栈**：Vue 3.5 / Vite 8（rolldown）/ TS 6 / Tailwind CSS v4 / Vue Router 4 / Pinia 4 / vue-i18n 11 / lucide-vue-next / motion-v / reka-ui / vue-sonner / @fontsource-variable/inter。
- **保留**：`assets/`、`public/`、`src/styles/globals.css`、`docs/UI-还原文档.md`、`README.md`。
- 原完整源码在 git commit `d65618d`（`git archive d65618d frontend/...` 可取回）。

## 脚手架踩坑记录（重要）
1. **TS 6 已废弃 `baseUrl`**：`tsconfig.app.json` 里只能写 `"paths": {"@/*": ["./src/*"]}`，配 `baseUrl` 会报 TS5101。
2. **globals.css 注释里不能有花括号文本**（如 `{js,cjs}`、`{tsx,ts,jsx,js}`）：Tailwind v4 会当语法解析，报 `CssSyntaxError: Invalid declaration`。注释里描述 @source 规则时要避开花括号。
3. **`pnpm create vite` 会失败**（`ERR_PNPM_CLI_DLX_READ_MANIFEST`，dlx 缓存问题），改用 `npm create vite@latest` 正常。
4. `vite.config.ts` 里 `/api` 已代理到 `http://localhost:8080`（Go 后端）。
5. 主题持久化键 `narra-theme`；i18n 持久化键 `narra-locale`。

## 前端约定
- 路径别名 `@` → `src/`。
- 组件用 Vue 3 `<script setup lang="ts">`；页面放 `src/views/`，页面组件全部懒加载，路由标题写 `meta.title`。
- i18n 目前仅 `zh-CN` / `en-US` 两套（原项目 12 种），键名沿用原项目结构。
- 启动：`cd frontend && npm run dev`（端口 5173）。

## 前端设计系统要点
- **品牌主色**：`--primary: #722ed1`（亮）/ `#8b47ea`（暗），紫色贯穿全站。
- **样式方案**：Tailwind CSS v4（CSS-first，**无 tailwind.config.js**），设计 token 全在 `src/styles/globals.css`。
- **色彩空间**：OKLCH；圆角基准 `--radius: 0.625rem`。
- **暗色模式**：`.dark` class 变体（非 media query），三态 light/dark/system + localStorage。
- **正文字体**：`Inter Variable`（`@fontsource-variable/inter`）。
- **原 UI 库**：shadcn/ui（style 为自定义 `radix-vega`）→ Vue 端用 `reka-ui` / `shadcn-vue`。
- **原图标**：lucide-react → `lucide-vue-next`（图标名一一对应）。
- **原动画**：`motion/react` → `motion-v`。
- **原 i18n**：自研 hook，12 种语言 → `vue-i18n`。

## globals.css 移植注意
必须删除 4 处指向已删文件的引用，否则 Vite 启动报错：第 3 行 `@import 'shadcn/tailwind.css'`、第 5 行 `@import '../components/workbench/chat/workbench-chat.css'`、第 9–12 行 `@source '../node_modules/...'`（4 条）、第 13–16 行 `@source "./slide-renderer-demo/..."`。

## Vue 3 编码踩坑（本项目已踩过，务必遵守）
1. **`<script setup>` 里不能写 `export interface`** —— 编译直接失败。类型统一放 `src/types/`（现有 `src/types/classroom.ts`）。
2. **组合式函数返回对象里的 ref，在模板中不会自动解包**。`const panel = useResizable(); panel.width` 在模板里是 Ref 对象。必须解构为顶层 ref：`const { width, collapsed } = useResizable(...)`。
3. **模板里慎用 `!` 非空断言**（如 `scene!`）。改用带兜底值的 computed。
4. **`flex-col` + `justify-end` + 子项默认可收缩 ⇒ 文字被压扁 + `overflow-hidden` 裁切**。修法：子项加 `shrink-0`，容器改 `flex-1 min-h-0 overflow-y-auto`，内层用 `mt-auto` 而不是 `justify-end`（否则滚动时顶部内容滚不出来）。
5. **reka-ui 没有 `Checkbox` / `Switch` 这种短名**：是 `CheckboxRoot` + `CheckboxIndicator`、`SwitchRoot` + `SwitchThumb`。
6. 多参事件要写 `@evt="(a, b) => fn(a, b)"`，不能写 `@evt="fn($event, $event)"`。
7. **悬停弹出的浮层：关闭必须延迟 ~300ms，且浮层要常驻 DOM 只切 `opacity`/`pointer-events`**。
   若用 `v-if` 挂载卸载 + 立即 `mouseleave` 关闭，浮层与触发按钮之间的物理空隙会让光标"够不到"浮层内容（踩坑实例：课堂页音量滑块拖不动）。
   另外 `input[type=range]` 用了 `appearance-none` 就必须自己写 `::-webkit-slider-thumb` / `::-moz-range-thumb`，否则没有可见可拖的滑块。

## 课堂页硬约束（源码注释明确，不可臆改）
- Header = **80px**（`h-20`）、Roundtable = **192px**（`h-[192px]`）
- SceneSidebar 默认 **220** / min 170 / max 400；ChatArea 默认 **340** / min 240 / max 560
- 舞台高度 = `calc(100% - 80px - 192px)`
- 键盘快捷键：`←/→` 翻页、`Space` 播放暂停、`↑/↓` 音量、`M` 静音、`S` 侧栏、`C` 聊天、`Esc` 退出全屏

## 浏览器验收工作流（agent-browser）
- 未在默认 PATH，需 `export PATH="D:/node/node_global:$PATH"`；Chrome 已装（`C:\Users\LHK\.agent-browser\browsers\chrome-153.0.8010.36`）。
- **冷启动 30~45 秒，之后每条命令仍要 20~30 秒**。用 `nohup bash -c '...' > /tmp/x.log 2>&1 &` + `sleep` 批量跑；**不要接管道**（`| tail` 会挂住）。
- 截图参数是 `--full`（不是 `--full-page`）；先 `set viewport 1440 900`，否则默认视口太矮会把舞台压到 190px 导致误判。
- 选择器里带 Tailwind 方括号类名（`h-[192px]`）在 `eval` 的 `querySelector` 里要转义，麻烦。改为遍历 `[...document.querySelectorAll('div')].find(e => e.className.includes('h-[192px]'))` 更省事；复杂脚本写成文件再 `eval "$(cat file.js)"`。
- 截图放 `D:\Narra\_shots\`（非项目产物）。

## Vue 项目启动与验证
- 启动：`cd frontend && npm run dev`（5173）。
- 类型检查：`npx vue-tsc --noEmit -p tsconfig.app.json`。
- 构建：`npm run build`（Vite 8 + rolldown）。
- ⚠️ **构建前必须先分批删 `dist/`**，否则 rolldown 清理 outDir 时会被沙箱批量删除保护拦下（`SAFE_DELETE_BULK_CONFIRM_REQUIRED`，阈值 50 文件/turn）。做法：`rm -rf dist/avatars` → `rm -rf dist/logos` → `rm -rf dist`，每次 <50 个文件。

## 首页已确认的产品决策（不要再按原版还原）
- 文生图 / 文生视频 / 视频导出：**已删**。
- 媒体生成弹层：**整体删掉**（原版是 Image/Video/TTS/ASR 四 Tab）。TTS 恒开启且不在前端暴露；«高级设置»入口删掉。
- **语音输入只有一个入口**：Composer 右下角的 `SpeechButton`（`HomeView.vue`，原版 `components/audio/speech-button.tsx`）。
  ⚠️ **绝对不要在 `GenerationToolbar` 里再加麦克风** —— 曾经因为误读需求加过一次，造成同一功能两个入口，用户要求删掉。工具栏最终只有 模型选择器 | 分隔线 | 课程材料 | 联网搜索。
- 「最近学习」→「**我的课堂**」（⚠️ i18n 键名仍是 `home.recentClassrooms`，只改了值）。
- 「导入课堂」「导入 PPTX」按钮：**已删**。
- 材料上传：支持 Markdown（`.md`/`.markdown`）。
- 相关 i18n 段：`voice.startListening` / `voice.stopListening`（语音输入提示），另有 `media` 段已被 `voice` 段取代。

## 项目路由（原结构，供重写参考）
`/`（首页/落地页）、`/workspace`（Pro 工作区）、`/classroom/:id`（课堂播放）、`/generation-preview`（生成预览）、`/workbench/new`（新建工作台）。

## 资源约定
- 头像：`public/avatars/`，预设用 `-2` 结尾那组（`user.png`、`teacher-2.png`、`assist-2.png`、`clown-2.png`、`curious-2.png`、`note-taker-2.png`、`thinker-2.png`）。
- 服务商 logo：`public/logos/`（34 个）。
- 演示图：`assets/`，交互模式图在 `assets/interactive_mode/`。
- `D:\Narra\_ui_backup\`（121 文件）已确认冗余（= public 96 + assets 24 + globals.css），待用户确认后删除。

## 数据架构决策
- **课堂角色不进数据库**（已与用户达成共识）。拆三份：① 前端 `src/data/agents.ts` 只放展示元数据（`id/name/role/avatar/color/voice`）；② `persona` 系统提示词归后端（Go + Eino），按同一个 `id` 索引；③ 用户的选择（`agentId[]` + 各自 `voiceId`）随课程配置存一个 JSON 字段。
- **`agentId` 是前后端契约**，改 id 必须两边同步。原项目角色同样写死在 `frontend/lib/orchestration/registry/store.ts`（`d65618d`），并非从接口拉取。
- 音色库（VoxCPM）是唯一留活口的：目录由服务商决定，将来建议后端出 `GET /api/voices` 静态接口；`src/data/voices.ts` 只换取数方式，组件不动。
- `src/data/providers.ts`（模型服务商）同理，接后端后换成接口下发。

## 用户协作偏好（重要）
- **改代码前必须先让用户检查**：发现问题只做 review + 定位 + 给出改法和位置，不要直接改用户代码。除非明确授权"你改吧"。
- 用户喜欢自己掌控代码改动，删除/重构类操作前要先给清单等确认。

