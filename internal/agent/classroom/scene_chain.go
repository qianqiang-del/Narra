package classroom

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"narra/internal/agent"
	"narra/internal/model/entity"
)

type sceneChainInput struct {
	Classroom *entity.Classroom
	Scenes    []entity.Scene
	Current   entity.Scene
	Teacher   entity.PresetAgent
	Blocks    []contentBlock
}

type blocksOutput struct {
	Blocks []contentBlock `json:"blocks"`
}

func buildContentChain(ctx context.Context, rt *runtime) (compose.Runnable[*sceneChainInput, []contentBlock], error) {
	chain := compose.NewChain[*sceneChainInput, []contentBlock]()
	chain.AppendLambda(compose.InvokableLambda(func(_ context.Context, in *sceneChainInput) ([]*schema.Message, error) {
		system, ok := agent.BuildTaskPrompt(agent.TaskScene)
		if !ok {
			return nil, fmt.Errorf("场景内容提示词未注册")
		}
		return []*schema.Message{schema.SystemMessage(system), schema.UserMessage(sceneUserPrompt(in, false))}, nil
	})).AppendChatModel(rt.chatModel).AppendLambda(compose.InvokableLambda(func(_ context.Context, msg *schema.Message) ([]contentBlock, error) {
		var out blocksOutput
		if err := unmarshalLoose(msg.Content, &out); err != nil {
			return nil, err
		}
		return validateBlocks(out.Blocks)
	}))
	return chain.Compile(ctx)
}

func buildNarrationChain(ctx context.Context, rt *runtime) (compose.Runnable[*sceneChainInput, []narrationSegment], error) {
	chain := compose.NewChain[*sceneChainInput, []narrationSegment]()
	chain.AppendLambda(compose.InvokableLambda(func(_ context.Context, in *sceneChainInput) ([]*schema.Message, error) {
		system, ok := agent.BuildSystemPrompt(in.Teacher, agent.TaskNarration)
		if !ok {
			return nil, fmt.Errorf("教师讲稿提示词未注册")
		}
		return []*schema.Message{schema.SystemMessage(system), schema.UserMessage(sceneUserPrompt(in, true))}, nil
	})).AppendChatModel(rt.chatModel).AppendLambda(compose.InvokableLambda(func(_ context.Context, msg *schema.Message) ([]narrationSegment, error) {
		var items []narrationSegment
		if err := unmarshalArrayLoose(msg.Content, &items); err != nil {
			return nil, err
		}
		return items, nil
	}))
	return chain.Compile(ctx)
}

func unmarshalArrayLoose(raw string, target any) error {
	text := strings.TrimSpace(raw)
	if strings.HasPrefix(text, "```") {
		if newline := strings.IndexByte(text, '\n'); newline >= 0 {
			text = text[newline+1:]
		}
		if end := strings.LastIndex(text, "```"); end >= 0 {
			text = text[:end]
		}
	}
	start, end := strings.IndexByte(text, '['), strings.LastIndexByte(text, ']')
	if start < 0 || end <= start {
		return fmt.Errorf("没找到 JSON 数组")
	}
	return json.Unmarshal([]byte(text[start:end+1]), target)
}

func sceneUserPrompt(in *sceneChainInput, narration bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "课程需求：%s\n课堂模式：%s\n完整大纲：\n", in.Classroom.Requirement, in.Classroom.Mode)
	for i, scene := range in.Scenes {
		fmt.Fprintf(&b, "%d. [%s] %s：%s\n", i+1, scene.Type, scene.Title, scene.Brief)
	}
	fmt.Fprintf(&b, "\n当前场景：[%s] %s\n内容摘要：%s\n", in.Current.Type, in.Current.Title, in.Current.Brief)
	if narration {
		raw, _ := json.Marshal(in.Blocks)
		fmt.Fprintf(&b, "已校验内容块：%s\n每段不超过120字。", raw)
	}
	return b.String()
}

func validateBlocks(blocks []contentBlock) ([]contentBlock, error) {
	if len(blocks) == 0 {
		return nil, fmt.Errorf("这一页一个内容块都没有")
	}
	keys := make(map[string]struct{}, len(blocks))
	for i := range blocks {
		block := &blocks[i]
		block.Key = truncateRunes(strings.TrimSpace(block.Key), maxContentKeyRunes)
		block.Type = strings.TrimSpace(block.Type)
		block.Content = strings.TrimSpace(block.Content)
		if block.Key == "" || block.Content == "" {
			return nil, fmt.Errorf("第 %d 个内容块不完整", i+1)
		}
		if _, exists := keys[block.Key]; exists {
			return nil, fmt.Errorf("内容块 key %q 重复", block.Key)
		}
		if _, ok := allowedBlockTypes[block.Type]; !ok {
			return nil, fmt.Errorf("内容块类型 %q 无效", block.Type)
		}
		keys[block.Key] = struct{}{}
	}
	return blocks, nil
}

func validateNarration(items []narrationSegment, blocks []contentBlock) ([]narrationSegment, error) {
	byKey := make(map[string]string, len(items))
	for _, item := range items {
		if _, exists := byKey[item.ContentKey]; exists {
			return nil, fmt.Errorf("内容块 %q 有重复讲解", item.ContentKey)
		}
		if strings.TrimSpace(item.Text) == "" {
			return nil, fmt.Errorf("内容块 %q 的讲解为空", item.ContentKey)
		}
		byKey[item.ContentKey] = strings.TrimSpace(item.Text)
	}
	result := make([]narrationSegment, 0, len(blocks))
	for _, block := range blocks {
		text, ok := byKey[block.Key]
		if !ok {
			return nil, fmt.Errorf("内容块 %q 缺少讲解", block.Key)
		}
		result = append(result, narrationSegment{ContentKey: block.Key, Text: text})
	}
	return result, nil
}
