package classroom

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
	"go.uber.org/zap"

	"narra/internal/agent"
	"narra/internal/toolcall"
	"narra/pkg/logger"
)

// toolNameEmitEvidence 是调研交卷用的工具名。
const toolNameEmitEvidence = "emit_evidence_bundle"

// maxEvidenceItems 是一页最多带回的证据条数。
const maxEvidenceItems = 8

// maxEvidenceClaimRunes 是单条证据的长度上限。
const maxEvidenceClaimRunes = 200

// maxEvidenceSummaryRunes 是证据摘要的长度上限。
const maxEvidenceSummaryRunes = 600

// maxResearchNoteRunes 是落进页面记录的调研降级原因长度上限。
const maxResearchNoteRunes = 200

// EvidenceBundle 是一次调研压缩后的证据包，是内容专家唯一的外部事实来源。
type EvidenceBundle struct {
	Items     []EvidenceItem `json:"items"`
	Conflicts []string       `json:"conflicts"`
	Summary   string         `json:"summary"`
}

// EvidenceItem 是证据包里的一条事实。
type EvidenceItem struct {
	Claim       string `json:"claim"`
	Source      string `json:"source"`
	RetrievedAt string `json:"retrieved_at,omitempty"`
	Confidence  string `json:"confidence,omitempty"`
}

// researchInput 是调研专家的输入：本页上下文与规划出的工具步骤。
type researchInput struct {
	Page  PageContext
	Steps []ToolStep
}

// researchEvidence 按页执行计划取证据并压成证据包。
//
// 第二个返回值是降级原因，非空表示这一页没有拿到外部资料：工具来自 runtime 的白名单，
// Agent 拿不到未授权工具；工具失败不再让整趟调研作废——失败原因作为工具结果交回模型，
// 模型可以换一个查询词重试，也可以按提示交回空证据包继续生成。因此"这一页为什么没有资料"
// 有两条来源：Agent 整体失败用 err，工具调用失败用记录器里的原因。
func researchEvidence(ctx context.Context, rt *runtime, in *researchInput) (*EvidenceBundle, string) {
	if len(rt.tools) == 0 {
		return &EvidenceBundle{}, ""
	}
	agentInstance, err := rt.researchAgent(ctx)
	if err != nil {
		logger.Warn("调研 Agent 构造失败，跳过资料检索", zap.Error(err))
		return &EvidenceBundle{}, "调研 Agent 构造失败，这一页没有外部资料"
	}
	system, ok := agent.BuildTaskPrompt(agent.TaskResearch)
	if !ok {
		logger.Warn("调研提示词未注册，跳过资料检索")
		return &EvidenceBundle{}, "调研提示词未注册，这一页没有外部资料"
	}
	messages := []*schema.Message{schema.SystemMessage(system), schema.UserMessage(researchPrompt(in))}

	recorder := toolcall.NewRecorder()
	ctx = rt.retrievalContext(ctx)
	message, err := agentInstance.Generate(toolcall.WithRecorder(ctx, recorder), messages)
	if err != nil {
		logger.Warn("资料调研失败，改用空证据继续", zap.Error(err))
		return &EvidenceBundle{}, truncateRunes("资料调研失败，这一页没有外部资料："+err.Error(), maxResearchNoteRunes)
	}
	bundle, parseErr := parseEvidenceBundle(message)
	if parseErr != nil {
		logger.Warn("证据包解析失败，改用空证据继续", zap.Error(parseErr))
		return &EvidenceBundle{}, truncateRunes("证据包解析失败，这一页没有外部资料："+parseErr.Error(), maxResearchNoteRunes)
	}
	if len(bundle.Items) == 0 {
		if cause := recorder.Cause(); cause != "" {
			return bundle, truncateRunes("资料调研失败，这一页没有外部资料："+cause, maxResearchNoteRunes)
		}
	}
	return bundle, ""
}

// parseEvidenceBundle 从交卷工具的返回里取出并裁剪证据包。
func parseEvidenceBundle(message *schema.Message) (*EvidenceBundle, error) {
	var bundle EvidenceBundle
	if err := unmarshalLoose(message.Content, &bundle); err != nil {
		return nil, err
	}
	items := make([]EvidenceItem, 0, len(bundle.Items))
	for _, item := range bundle.Items {
		item.Claim = truncateRunes(strings.TrimSpace(item.Claim), maxEvidenceClaimRunes)
		if item.Claim == "" {
			continue
		}
		item.Source = strings.TrimSpace(item.Source)
		item.RetrievedAt = strings.TrimSpace(item.RetrievedAt)
		item.Confidence = strings.TrimSpace(item.Confidence)
		items = append(items, item)
		if len(items) >= maxEvidenceItems {
			break
		}
	}
	bundle.Items = items
	bundle.Conflicts = normalizeTexts(bundle.Conflicts)
	bundle.Summary = truncateRunes(strings.TrimSpace(bundle.Summary), maxEvidenceSummaryRunes)
	return &bundle, nil
}

// researchPrompt 拼调研专家的用户提示词。
func researchPrompt(in *researchInput) string {
	var builder strings.Builder
	in.Page.writePrompt(&builder)
	builder.WriteString("\n## 这一页定下的调研步骤\n")
	if len(in.Steps) == 0 {
		builder.WriteString("规划阶段没有给出具体步骤，请自行判断需要查什么。\n")
	}
	for index, step := range in.Steps {
		fmt.Fprintf(&builder, "%d. 用 %s 查「%s」，目的是：%s\n", index+1, step.Tool, step.Query, step.Purpose)
	}
	builder.WriteString("\n先用工具把事实查准，再调用 ")
	builder.WriteString(toolNameEmitEvidence)
	builder.WriteString(" 交卷。资料查不到就交回空证据，不要编造。\n")
	return builder.String()
}

// emitEvidenceTool 是调研交卷用的工具：把参数原样回传，由调用方解析成证据包。
type emitEvidenceTool struct{ info *schema.ToolInfo }

// Info 返回工具的元信息供调研 Agent 使用。
func (t *emitEvidenceTool) Info(context.Context) (*schema.ToolInfo, error) { return t.info, nil }

// InvokableRun 校验参数是合法 JSON 后原样返回。
func (t *emitEvidenceTool) InvokableRun(_ context.Context, arguments string, _ ...tool.Option) (string, error) {
	if !json.Valid([]byte(arguments)) {
		return "", fmt.Errorf("证据包不是合法 JSON")
	}
	return arguments, nil
}

// newEmitEvidenceTool 建调研交卷工具。
func newEmitEvidenceTool() tool.BaseTool {
	return &emitEvidenceTool{info: &schema.ToolInfo{
		Name: toolNameEmitEvidence,
		Desc: "提交整理好的证据包。资料查完之后调用本工具交卷，每次调研只调用一次。",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(objectSchema("一次性调研的证据包",
			[]string{"items", "summary"},
			schemaField{"items", arraySchema(evidenceItemSchema(), "压缩后的事实条目，最多 8 条")},
			schemaField{"conflicts", arraySchema(valueSchema("string", "资料之间互相矛盾的地方"), "资料冲突点，没有就留空")},
			schemaField{"summary", valueSchema("string", "给内容专家用的证据摘要，不超过 600 字")},
		)),
	}}
}

// evidenceItemSchema 返回一条证据的参数 schema。
func evidenceItemSchema() *jsonschema.Schema {
	return objectSchema("一条压缩后的事实", []string{"claim", "source"},
		schemaField{"claim", valueSchema("string", "经过压缩的事实，一句话，不超过 200 字")},
		schemaField{"source", valueSchema("string", "来源标识，引用时写它")},
		schemaField{"retrieved_at", valueSchema("string", "这条事实的适用时间，不知道就留空")},
		schemaField{"confidence", valueSchema("string", "high / medium / low")},
	)
}

// researchAgent 建调研 Agent：挂白名单工具，交卷工具进 ToolReturnDirectly。
func (r *runtime) researchAgent(ctx context.Context) (*react.Agent, error) {
	tools := make([]tool.BaseTool, 0, len(r.tools)+1)
	tools = append(tools, r.tools...)
	tools = append(tools, newEmitEvidenceTool())

	agentInstance, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel:   r.chatModel,
		MaxStep:            maxResearchStep,
		ToolsConfig:        compose.ToolsNodeConfig{Tools: tools},
		ToolReturnDirectly: map[string]struct{}{toolNameEmitEvidence: {}},
	})
	if err != nil {
		return nil, fmt.Errorf("构造调研 Agent 失败: %w", err)
	}
	return agentInstance, nil
}

// toolNames 列出本次生成可用的工具名，供提示词与页规划使用。
func (r *runtime) toolNames(ctx context.Context) []string {
	names := make([]string, 0, len(r.tools))
	for _, item := range r.tools {
		info, err := item.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		names = append(names, info.Name)
	}
	return names
}
