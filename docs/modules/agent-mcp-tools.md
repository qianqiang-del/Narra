# Narra 模块设计文档：MCP + Tool + Agent 系统

> 负责人：li  
> 最后更新：2026-09-11

---

## 一、模块总览

我负责 Narra 平台的**智能核心层**，包括：

| 子模块 | 一句话说明 |
|--------|-----------|
| MCP 协议 | Tool 调用的标准协议，连接 Agent 与外部能力 |
| Tool 注册与执行引擎 | 管理所有工具的注册、发现、调用、错误处理 |
| 课堂 Agent（生成课件） | **单 Agent**，接收用户需求，调用 Tool，LLM 生成结构化课件 |
| 工作台 Agent | Pro 工作台的对话式 Agent，深度交互 |
| 上下文记忆 | Agent 会话记忆持久化（Redis + PostgreSQL） |
| 上下文管理 | 多轮对话的上下文窗口裁剪与压缩 |
| 各种工具 | web_search、doc_extract、tts、asr 等具体 Tool 实现 |

---

## 二、MCP 协议层

### 2.1 定位

MCP（Model Context Protocol）是 Agent 与 Tool 之间的**标准通信协议**。所有 Tool 通过 MCP 暴露统一接口，Agent 通过 MCP 调用 Tool，双方解耦。

### 2.2 架构图

```
┌───────────────────────────────────────────┐
│              Agent Layer                  │
│  ┌────────────┐  ┌────────────┐          │
│  │ 课堂Agent   │  │ 工作台Agent │          │
│  │ (生成课件)   │  │ (对话交互)  │          │
│  └─────┬──────┘  └─────┬──────┘          │
│        └────────┬──────┘                  │
│                 ▼                         │
│       ┌─────────────────┐                 │
│       │   MCP Protocol  │  ← 我负责       │
│       └────────┬────────┘                 │
│                ▼                          │
│  ┌──────────────────────────┐             │
│  │      Tool Registry       │             │
│  ├────────┬────────┬────────┤             │
│  │web_search│doc_extract│tts│ ...         │
│  └────────┴────────┴────────┘             │
└───────────────────────────────────────────┘
```

### 2.3 协议规范

```go
// Tool 定义 — 注册到 Registry 时使用
type Tool struct {
    ID          string            `json:"id"`          // 唯一标识，如 "web_search"
    Name        string            `json:"name"`        // 人类可读名称
    Description string            `json:"description"` // 功能描述，供 LLM 理解何时调用
    Parameters  map[string]Param  `json:"parameters"`  // 输入参数 schema
    Returns     Param             `json:"returns"`     // 输出格式
}

// Tool 调用请求
type ToolCallRequest struct {
    ToolID     string                 `json:"tool_id"`
    Parameters map[string]interface{} `json:"parameters"`
    CallID     string                 `json:"call_id"` // 幂等标识
    Timeout    time.Duration          `json:"timeout"`
}

// Tool 调用响应
type ToolCallResponse struct {
    CallID  string      `json:"call_id"`
    Success bool        `json:"success"`
    Data    interface{} `json:"data,omitempty"`
    Error   *ToolError  `json:"error,omitempty"`
}
```

### 2.4 与 Eino 集成

Narra 后端使用 **CloudWeGo Eino** 做 Agent 编排。MCP 层需要：

- 实现 Eino 的 `Tool` 接口，让 Agent 可以直接调用
- 支持同步调用（查询类）和流式调用（搜索结果流）
- 并发控制 + 超时管理

---

## 三、Tool 注册与执行引擎

### 3.1 ToolRegistry

```go
type ToolRegistry struct {
    tools map[string]Tool
    mu    sync.RWMutex
}

func (r *ToolRegistry) Register(tool Tool) error          // 注册工具
func (r *ToolRegistry) Get(id string) (Tool, bool)        // 按 ID 查找
func (r *ToolRegistry) List() []ToolInfo                  // 列出所有（供 Agent 选择）
func (r *ToolRegistry) Execute(ctx context.Context, req ToolCallRequest) (*ToolCallResponse, error)
```

### 3.2 Tool 清单

| Tool ID | 名称 | 功能 | 依赖 |
|---------|------|------|------|
| `web_search` | 网络搜索 | 搜索引擎查询 | Tavily / Bocha / Brave API |
| `doc_extract` | 文档提取 | 解析 PDF/Word/PPT/MD | MinerU / unpdf |
| `tts` | 语音合成 | 文字转语音 | VoxCPM（6 种中文音色） |
| `asr` | 语音识别 | 语音转文字 | OpenAI Whisper / Qwen ASR |
| `pptx_export` | PPT 导出 | 课堂内容 → PPTX | 待定 |
| `script_export` | 讲稿导出 | 课堂内容 → MD/DOCX | 待定 |

### 3.3 执行流程

```
Agent 决策调用 Tool
      │
      ▼
MCP 协议层序列化请求
      │
      ▼
Tool Registry 查找 → 参数校验
      │
      ▼
执行 Tool（可能调外部 API）
      │
      ▼
返回结果 / 错误处理
      │
      ▼
Agent 继续推理
```

---

## 四、课堂 Agent（生成课件）

### 4.1 核心定位

这是一个**单 Agent**，不是多 Agent。它的任务是：

> 接收用户需求 + 可选素材 → 调用 Tool 辅助 → LLM 生成结构化课件

### 4.2 生成流程

```
用户输入
  ├─ 需求描述（如"30分钟学Python"）
  ├─ 可选素材（PDF/Word/PPT/MD）
  ├─ 模型选择（10 家供应商可选）
  └─ Agent 选择（预设/自动）
      │
      ▼
┌─────────────────────────────────────┐
│ Step 1: 大纲生成                     │
│   - 解析用户需求                      │
│   - 调用 doc_extract 处理上传素材     │
│   - 可选调用 web_search 补充信息      │
│   - LLM 生成场景大纲                  │
│   - 输出: Scene[] 结构（类型+标题）    │
└──────────────┬──────────────────────┘
               ▼
┌─────────────────────────────────────┐
│ Step 2: 大纲审阅（Generation Preview）│
│   - 用户查看、编辑、重排场景          │
│   - 支持增删场景                      │
│   - 确认后进入生成                    │
└──────────────┬──────────────────────┘
               ▼
┌─────────────────────────────────────┐
│ Step 3: 逐场景内容生成                │
│   - 按顺序为每个场景填充内容          │
│   - 每完成一个场景，交给 B 的 SSE 层推送│
│   - 状态: pending → generating → ready│
└──────────────┬──────────────────────┘
               ▼
┌─────────────────────────────────────┐
│ Step 4: 课堂就绪                     │
│   - 跳转到课堂播放页                  │
│   - 多 Agent 开始讨论（另一阶段）      │
└─────────────────────────────────────┘
```

### 4.3 场景类型

| 类型 | 用途 | 生成内容 |
|------|------|---------|
| `slide` | 知识讲解 | heading + bullets + accent |
| `quiz` | 互动测验 | question + 4选项 + 正确答案 |
| `interactive` | 动手实践 | 模拟浏览器 + 操作指引 |
| `pbl` | 项目实战 | 看板式任务列表 |
| `complete` | 课程结束 | 统计 + 成就展示 |

### 4.4 输出方式

Agent 产出内容后，**不直接处理 SSE 流式推送**，而是交给 B 的 SSE 基础设施完成前端推送。

我只需提供：
- 场景内容数据（Scene 结构体）
- 场景状态变更通知（pending → generating → ready/failed）

B 的 SSE 层负责序列化和推送到前端。

---

## 五、工作台 Agent

### 5.1 与课堂 Agent 的区别

| 维度 | 课堂 Agent | 工作台 Agent |
|------|-----------|-------------|
| 模式 | 单 Agent 生成课件 | 单 Agent 对话交互 |
| 输入 | 用户需求 → 生成结构化内容 | 用户消息 → 自由对话 |
| 输出 | Scene[]（幻灯片/测验等） | 文本回复 + Tool 调用结果 |
| 交互 | 一次性生成，用户审阅 | 多轮对话，实时响应（流式输出复用 B 的 SSE 层） |
| 适用 | 学习、听课 | 研究、创作、深度探索 |

### 5.2 工作台 Agent 能力

- 多轮对话，理解上下文
- 按需调用 Tool（搜索、提取文档等）
- 记忆当前会话和历史会话
- 关联用户的课堂内容
- 支持动态加载技能（Skills）

### 5.3 API 设计

```
POST /api/workbench/sessions          → 创建新会话
GET  /api/workbench/sessions           → 列出会话
GET  /api/workbench/sessions/:id       → 获取会话详情
POST /api/workbench/sessions/:id/msg   → 发送消息（流式输出复用 B 的 SSE 层）
DELETE /api/workbench/sessions/:id     → 删除会话
```

---

## 六、上下文记忆系统

### 6.1 三层记忆

| 层级 | 内容 | 存储 | 生命周期 |
|------|------|------|---------|
| 工作记忆 | 当前会话完整对话 | 内存（Go slice） | 会话期间 |
| 短期记忆 | 最近 N 轮摘要 | Redis | 7 天过期 |
| 长期记忆 | 用户偏好、学习历史 | PostgreSQL | 永久 |

### 6.2 记忆数据结构

```go
// 用户记忆
type UserMemory struct {
    UserID      string                 `json:"user_id"`
    Preferences map[string]interface{} `json:"preferences"` // 偏好设置
    History     []SessionSummary       `json:"history"`     // 会话摘要
    Knowledge   []KnowledgeEntry       `json:"knowledge"`   // 学到的知识点
    UpdatedAt   time.Time              `json:"updated_at"`
}

// 课堂 Agent 记忆
type ClassroomMemory struct {
    ClassroomID string   `json:"classroom_id"`
    Discussed   []string `json:"discussed"`   // 已讨论的话题
    UserNotes   []string `json:"user_notes"`  // 用户笔记
}
```

### 6.3 Redis 缓存

```go
// 会话上下文缓存
func (s *SessionStore) SaveContext(ctx context.Context, sessionID string, msgs []Message) error {
    data, _ := json.Marshal(msgs)
    return s.redis.Set(ctx, "session:"+sessionID+":context", data, 7*24*time.Hour)
}
```

---

## 七、上下文管理

### 7.1 问题

当对话历史超过 LLM 上下文窗口时，需要裁剪。

### 7.2 策略

```go
type ContextManager struct {
    maxTokens    int     // 上下文窗口大小
    summaryRatio float64 // 摘要保留比例
}

func (cm *ContextManager) Manage(messages []Message) []Message {
    if countTokens(messages) <= cm.maxTokens {
        return messages
    }
    // 策略1: 滑动窗口 — 保留最近 N 轮
    // 策略2: 摘要压缩 — 旧对话压缩为摘要
    // 策略3: 重要性排序 — 保留重要消息，丢弃闲聊
    return cm.compress(messages)
}
```

### 7.3 工作台 Agent 的上下文结构

```
[系统提示词]
[早期对话摘要]    ← 每 10 轮自动生成
[最近 10 轮完整对话]
[当前用户输入]
```

---

## 八、与其他模块的接口

### 8.1 与 B（RAG 知识管线）

```
Tool: rag_retrieve
请求: { query: string, classroom_id?: string, top_k?: number }
响应: { results: [{ content, source, score }] }
```

B 负责 RAG 的具体实现，我这边只通过 MCP 调用这个 Tool。

### 8.2 与 B（SSE 流式输出）

B 负责 SSE 基础设施，我负责产出内容：
- 课堂 Agent 生成的场景数据 → 交给 B 推送
- 工作台 Agent 的对话回复 → 交给 B 推送
- 我不直接处理 SSE 连接管理和序列化

### 8.3 与 C（语音 + MCP 宿主）

```
语音:
- TTS: { text, voice_id } → 音频流
- ASR: 音频流 → { text, confidence }
```

C 提供 MCP 宿主框架和语音流处理，我提供 Tool 的业务逻辑实现。

### 8.4 共享契约

| 契约 | 说明 |
|------|------|
| agentId | 前后端一致（teacher, assist, clown, curious, note-taker, thinker） |
| 课程配置 JSON | 用户选择的 Agent、模型、搜索引擎等 |
| MCP Tool 协议 | Tool 注册/调用/响应格式 |
| 错误码 | 复用 `pkg/errors/code.go` |

---

## 九、实现路线

### Phase 1: MCP 基础框架
- [ ] 定义 Tool 接口和协议
- [ ] 实现 Tool Registry
- [ ] MCP 请求/响应序列化
- [ ] 与 Eino 集成

### Phase 2: 核心 Tool
- [ ] web_search（Tavily/Bocha/Brave）
- [ ] doc_extract（PDF/Word/PPT/MD）
- [ ] tts（VoxCPM）
- [ ] asr（Whisper/Qwen）

### Phase 3: 课堂 Agent
- [ ] 大纲生成逻辑
- [ ] 逐场景内容生成
- [ ] 对接 B 的 SSE 层输出

### Phase 4: 上下文系统
- [ ] Redis 缓存层
- [ ] 上下文窗口管理
- [ ] 记忆持久化

### Phase 5: 工作台 Agent
- [ ] 会话管理 API
- [ ] 对话交互
- [ ] 技能加载机制
- [ ] 历史会话检索
