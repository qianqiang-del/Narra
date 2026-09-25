# 任务

为一页课件生成学生可见的内容块。输入包含课程需求、完整大纲和当前场景。

# 输出

只输出一个包含 `blocks` 数组的 JSON 对象，不要解释或代码围栏。

`type` 只能是 `heading`、`paragraph`、`list-item`、`callout`、`code`、`quiz`、`browser`、`columns`。

# 规则

- blocks 最多 6 个、不得为空；key 在本页唯一且不超过 120 字符。
- 每块保持简洁：关键词、短语、短句。不要为了填满页面而堆砌内容，一页讲清一个要点即可。
- content 是纯文本，不写 HTML。
- 页面只放关键词、短句和必要代码，不写教师口语讲稿。
- 不出现教师姓名、身份或人设。
- 根据场景 type 选择合适的内容形式。

## 场景类型约束

- `slide` 只生成 heading、paragraph、list-item、callout、code，不生成 browser 或 interaction。
- `quiz` 必须生成一个 type 为 `quiz` 的 block，并提供结构化 `interaction`：`{"kind":"quiz","options":["..."],"answer":"..."}`。选项必须是独立字符串，不能把选项和答案埋在 content 文本中。
- `interactive` 场景必须生成至少一个 type 为 `browser` 或 `interactive` 的可操作 block，并包含 `interaction.kind`。不能只用文字描述操作。每个控件必须提供 name、type、default；range 控件还必须提供 min、max、step；select 控件必须提供 options。
- `interaction.kind` 必须使用能准确描述当前课程交互的稳定标识，不得依赖预先写死的学科或实验名称。
- 交互所需的组件、参数、状态和计算规则必须放在结构化配置中，由当前场景内容决定。
- `pbl` 优先使用 columns、list-item、callout，不生成 browser。
- `interaction` 是机器可执行配置，必须是合法 JSON 对象；content 只用于展示说明，不要把控件配置编码成自然语言。

## 内容块职责

- `heading` 只用于标题或小节标题，内容简短，不要写完整讲稿。
- `paragraph` 用于解释一个完整知识点；content 必须是纯文本，只表达一个核心观点。
- `list-item` 用于一个独立要点，不要把多个无关要点拼成一段长文本。
- `callout` 用于结论、规律、注意事项或重点提醒，内容应简洁明确。
- `code` 只放可执行或需要展示的代码，不要把代码说明和代码混在同一个 content 中。
- `columns` 用于对比、分类或分组信息，列与列之间必须有清晰的逻辑关系。
- `content` 只负责页面展示，不得包含 HTML、Markdown、JSON、教师讲稿或机器控制指令。
- `narration` 只负责教师讲解；`interaction` 只负责可执行交互配置，三者不能互相替代或混写。

## 统一 JSON 契约

每个 block 必须符合以下结构：

```json
{
  "key": "唯一稳定标识",
  "type": "heading | paragraph | list-item | callout | code | quiz | browser | columns | interactive",
  "content": "给学生看的纯文本",
  "interaction": {
    "kind": "描述交互性质的通用标识",
    "controls": [],
    "options": [],
    "answer": "可选的正确答案",
    "config": {}
  }
}
```

`interaction` 只在 block 需要用户操作、选择、输入或观察动态结果时提供。不要为了凑字段给普通文本 block 添加空的 interaction。

## 交互配置要求

- `interaction.kind` 必须是当前交互性质的稳定标识，不能使用依赖某个具体课程名称的前端组件名称。
- `controls` 中每个控件必须包含唯一的 `name`、`type` 和 `default`。
- `select` 控件必须提供非空 `options`。
- `range` 控件必须提供 `min`、`max`、`step`，且 default 必须在范围内。
- `number`、`text`、`checkbox`、`switch` 控件必须提供与类型匹配的 default。
- 需要联动、判断或结果反馈时，将规则放在 `config` 中，使用结构化 JSON，不要把规则藏在 content 里。
- 交互配置必须能让前端仅根据字段名称和 type 生成控件，不得要求前端猜测自然语言。

## 场景类型选择

- `slide`：以 heading、paragraph、list-item、callout、code 为主；不得生成交互控件。
- `quiz`：必须包含一个或多个 type 为 `quiz` 的 block；每个 quiz block 必须提供 interaction.options 和 interaction.answer。
- `interactive`：必须包含至少一个 type 为 `browser` 或 `interactive` 的 block，并提供完整 interaction.controls；其他普通 block 用于说明操作目标、步骤和结论。
- `pbl`：使用 columns、list-item、paragraph、callout 表达任务、分组、证据和结论；只有确实需要用户操作时才提供 interaction。
- 不要根据学科名称创造新的 scene type；所有页面必须使用上述固定类型。

### `slide` 详细规则

- 用于讲解概念、规律、步骤、定义、对比和总结。
- 至少包含一个 `heading` 或清晰的标题内容。
- 正文知识点使用 `paragraph`，并列要点使用 `list-item`。
- 结论、公式、易错点和记忆规则使用 `callout`。
- 需要展示程序时才使用 `code`。
- 不得包含 `browser`、`interactive` 或带 controls 的 interaction。
- 不要把教师完整讲稿放进 content。

### `quiz` 详细规则

- 用于需要学生选择答案并获得判断反馈的页面。
- 至少包含一个 `type: "quiz"` 的 block。
- quiz block 的 `content` 只写题干，不写选项列表和答案解释。
- 选项必须放在 `interaction.options` 中，至少两个选项且不能重复。
- 正确答案必须放在 `interaction.answer` 中，且必须与 options 中某一项完全一致。
- 需要解析时放入 `interaction.config.explanation`，不要拼进题干。
- 每个题目必须有独立 key。

示例：

```json
{
  "key": "question-1",
  "type": "quiz",
  "content": "下列说法中正确的是？",
  "interaction": {
    "kind": "choice",
    "options": ["选项 A", "选项 B", "选项 C"],
    "answer": "选项 B",
    "config": {"explanation": "因为……"}
  }
}
```

### `interactive` 详细规则

- 用于学生主动调整参数、选择对象、输入数值、切换状态或观察结果的页面。
- 至少包含一个 `type: "browser"` 或 `type: "interactive"` 的 block。
- 可操作 block 必须带 interaction，且 interaction 必须包含 controls。
- 每个 control 必须有唯一 name、type、default。
- control 的 label、单位和说明放入 control.config 或 block.content，不能让前端猜测。
- 选择控件使用 `select`，数值范围使用 `range`，数值输入使用 `number`，文本输入使用 `text`，布尔开关使用 `checkbox` 或 `switch`。
- 需要动态反馈时，在 interaction.config 中提供 result、rules、formula 或 options 等结构化字段。
- 普通说明、操作步骤和结论可以使用 paragraph、list-item、callout，但不能代替 controls。
- 不要把某个学科、实验或课程名称写成前端必须支持的固定类型；kind 只描述当前交互的性质。

示例：

```json
{
  "key": "operation-1",
  "type": "browser",
  "content": "调整参数并观察结果变化。",
  "interaction": {
    "kind": "parameter-operation",
    "controls": [
      {
        "name": "object",
        "type": "select",
        "default": "对象 A",
        "options": ["对象 A", "对象 B"],
        "config": {"label": "对象", "unit": ""}
      },
      {
        "name": "value",
        "type": "range",
        "default": 1,
        "min": 0,
        "max": 10,
        "step": 1,
        "config": {"label": "参数", "unit": "单位"}
      }
    ],
    "config": {
      "rules": [],
      "result": {"type": "text", "template": "结果：{{value}}"}
    }
  }
}
```

### `pbl` 详细规则

- 用于任务驱动、项目实践、分组协作、证据整理和方案评价。
- 使用 `columns` 表达任务阶段、角色分工、证据和结论等分组信息。
- 使用 `list-item` 表达待完成任务或检查清单。
- 使用 `callout` 表达评价标准、关键约束和最终结论。
- 如果确实需要学生操作，可以增加 interaction，但不能把整个 pbl 页面变成普通 quiz。
- columns 中每一组必须有明确标题，不能只返回一大段用分隔符拼接的文本。

### 完成页规则

- `complete` 不允许模型生成。
- `complete` 由后端在保存大纲时自动追加。
- 模型输出中出现 `complete` 必须视为非法结果。

## 质量要求

- 内容块必须按学生实际阅读和操作顺序排列。
- 每个可播放讲解块都必须有清晰、独立的 key，便于讲稿逐段对应。
- 交互页面必须同时提供操作目标、可操作控件和操作后的观察/反馈信息。
- quiz 必须提供足够信息让前端显示选项和判断答案，不能只返回一道没有选项的自然语言问题。
- 输出只能是合法 JSON，不要输出解释、Markdown 代码围栏或额外文本。
