# Narra

> **Generative Learning in Multi-Agent Interactive Classroom**
> 把一句学习需求，变成一堂有人讲、能互动、可回看的课。

Narra 是一个多智能体「生成式学习」平台。你只需要用一句话描述想学什么，可选地附上自己的学习资料，Narra 就会自动完成 **课程规划 → 页面生成 → 讲解配音**，并在一个可播放的互动课堂里把这门课讲给你听。

## 为什么做 Narra

- 普通 AI 问答只给一段回答：没有课程结构，没有讲解节奏，也不能回看；
- 自己啃长文档、整理课件又太费时间；
- 每个人的背景不同，却只能看同一套通用内容。

Narra 把「一次回答」变成「一堂课」：有课程结构、有讲解稿、有语音、有测验与互动页，还能根据你的背景和上传的资料个性化生成。

## 核心能力

| 能力 | 说明 |
|---|---|
| 课程生成 | 一句话/一段描述 → 完整课程计划：分几页、每页讲什么、先修关系、顺序，最多 15 页 |
| 页面类型 | 知识讲解页、随堂测验、深度互动页（模拟操作、参数调节、数据探索） |
| 多智能体课堂 | 教师、学生、助教等角色 Agent 同场：老师逐页讲解，学生提问讨论，系统调度发言节奏 |
| 讲解与配音 | 每页生成讲解稿并 TTS 合成语音，可选音色；播放、暂停、翻页由学习者控制 |
| 个人知识库 | 上传 PDF / Word / PPT / 图片，解析为 Markdown 并向量化（RAG），讲解时按需检索 |
| 联网搜索 | 生成前可开启联网，把版本号、政策、数据等事实查准 |
| Pro 工作台 | 对话式 Agent，可对已有课程继续修改、扩展与再创作 |
| 可观测 | Agent 生成链路接入 Langfuse，全流程可追踪 |

## 技术架构

```
前端  Vue 3 + Vite + Tailwind CSS v4 + Pinia
        │  REST / SSE
后端  Go + Gin ── Agent 编排（CloudWeGo Eino）── MCP / Tool Registry
        │                        │
   PostgreSQL                 Asynq 异步任务（生成 / 解析 / 向量化）
   Redis                      web_search · doc_extract · tts · asr · rag_retrieve
```

- **后端**：Go 1.25 · Gin · CloudWeGo Eino · Asynq · GORM/PostgreSQL · Redis · Zap · JWT
- **前端**：Vue 3.5 · Vite · Tailwind CSS v4 · Reka UI · Pinia · vue-i18n（中文 / English）
- **文档解析**：本地优先（RapidOCR，默认不外发文档内容），可切换外部 OCR；Office / PDF / 图片 → Markdown
- **向量化**：OpenAI 兼容 Embedding 接口
- **语音合成**：TTS（千问音色，支持语气控制）

## 快速开始

**准备依赖**：Go 1.25+、Node.js 20+、PostgreSQL、Redis

```bash
# 1. 后端
cp .env.example .env          # 按需填写 Embedding / TTS / 搜索等密钥
# 修改 configs/config.yaml 中的数据库、Redis 连接
make run                      # 等价于 go run cmd/server/main.go -c configs/config.yaml
# 服务默认监听 http://localhost:8080

# 2. 前端
cd frontend
npm install
npm run dev                   # http://localhost:5173（/api 已代理到 8080）
```

常用命令：`make build / test / fmt / vet / lint`，详见 `make help`。

## 目录结构

```
cmd/server/        # 程序入口
internal/
  agent/           # 课堂生成 Agent、多智能体讨论、提示词
  api/             # HTTP 路由与接口
  service/         # 业务服务
  worker/          # 异步任务
  rag/             # 知识库检索与向量化
  mcp/ toolcall/   # MCP 协议与工具调用层
  repository/      # 数据访问
  model/           # 领域模型
frontend/          # Vue 3 前端
configs/           # 配置文件
migrations/        # 数据库迁移
docs/              # 设计文档与品牌资料
```

## 文档

- 品牌与 Logo：`docs/brand/logo-design.md`
- 模块设计：`docs/modules/agent-mcp-tools.md`
- 数据库设计：`docs/database-design-v1.md`、`docs/rag-database.md`
