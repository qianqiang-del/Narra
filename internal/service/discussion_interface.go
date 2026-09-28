package service

import (
	"context"

	responsedto "narra/internal/model/dto/response"
)

// DiscussionService 是"用户发一句话 → 跑一趟多 Agent 讨论"的入口。
//
// 它补的是整条链路缺的那一节：编排器（internal/agent/discussion）本身写得再全，
// 也得有人**把它叫起来**。在那之前，全仓没有任何生产代码引用它 ——
// 表建好了、推送写好了、事件也会写了，但真实环境里不会有任何一行数据，
// 因为没有任何东西会发起一场讨论。
//
// 职责边界（一句话）：**把用户那句话变成一次可执行的编排输入，然后把编排交出去。**
//   - 往上：不认识 HTTP（那是 controller 的事）
//   - 往下：不认识"谁该说话、上下文怎么拼"（那是编排器的事）
//   - 自己只做三件：校验这次请求能不能开、把这堂课的角色与模型凑齐、把用户消息落库
type DiscussionService interface {
	// Start 受理一次讨论：写入用户消息，随即在后台开跑，立刻返回。
	//
	// 它**不等讨论跑完**：一趟讨论要按角色数调用好几次大模型，等到跑完再返回，
	// 用户会对着一个转圈的请求等上几十秒，而且中途看不到任何进展。
	// 过程与结果都落在事件表里，由 SSE 那条流带给前端。
	Start(ctx context.Context, conversationID uint64, content string) (*responsedto.DiscussionStart, error)
}
