package classroom

import (
	"context"
	"sync"

	"go.uber.org/zap"

	"narra/pkg/logger"
)

// 运行中任务的取消登记表。
//
// 取消要能落到正在跑的那一趟上，就得有人在进程内记着它的 cancel。asynq 能把排队中的任务删掉，
// 但对**正在执行**的任务只能等它自己结束，所以这一层必须自己留痕。
//
// 只登记当前进行中的课（进程内、用后即删）：它要对付的是「删课堂」这一个动作，
// 不是跨进程的任务状态——那种状态该落库（如 scenes.lease_owner），不该放这里。
var runningCancels sync.Map // map[uint64]context.CancelFunc

// registerCancel 登记一堂课的取消入口，返回注销函数。
func registerCancel(classroomID uint64, cancel context.CancelFunc) func() {
	runningCancels.Store(classroomID, cancel)
	var once sync.Once
	return func() {
		once.Do(func() { runningCancels.Delete(classroomID) })
	}
}

// CancelGeneration 停掉正在生成的这堂课，返回是否真的停到了一个在跑的任务。
//
// 返回 false 是正常的：这堂课可能已经跑完、可能还没开始、也可能在别的进程里跑。
// 调用方据此决定要不要记日志，不该据此判失败。
func CancelGeneration(classroomID uint64) bool {
	value, ok := runningCancels.Load(classroomID)
	if !ok {
		return false
	}
	cancel, ok := value.(context.CancelFunc)
	if !ok {
		return false
	}
	cancel()
	logger.Info("已取消正在生成的课堂", zap.Uint64("classroom_id", classroomID))
	return true
}
