package discussion

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino-ext/callbacks/langfuse"

	"narra/internal/model/entity"
)

// observationContext 把数据库中已经生成的稳定链路 ID 交给 Eino/Langfuse。
// 它只补充观测元数据，不参与讨论的状态判断；未启用 Langfuse 时 context 仍可正常传递。
func (o *Orchestrator) observationContext(ctx context.Context, run *entity.OrchestrationRun, turn *entity.AgentTurn) context.Context {
	metadata := map[string]string{
		"run_id":          fmt.Sprintf("%d", run.ID),
		"conversation_id": fmt.Sprintf("%d", run.ConversationID),
	}
	if turn != nil {
		metadata["turn_id"] = fmt.Sprintf("%d", turn.ID)
		metadata["turn_no"] = fmt.Sprintf("%d", turn.TurnNo)
	}
	return langfuse.SetTrace(ctx,
		langfuse.WithID(run.TraceID),
		langfuse.WithName("discussion-run"),
		langfuse.WithSessionID(fmt.Sprintf("%d", run.ConversationID)),
		langfuse.WithTags("discussion", "member-c"),
		langfuse.WithMetadata(metadata),
	)
}
