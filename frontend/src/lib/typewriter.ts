/**
 * 计算一次屏幕更新应该显示到哪里。
 *
 * current 和 target 都是已经由 SSE 收到的正文；这里不改变网络数据，只控制视觉追赶速度。
 * 长回答积压太多时按 2/4/8 个字符推进，避免一条回答播放几十秒。
 */
export function nextVisibleText(current: string, target: string, _intervalMs = 24): string {
  if (target.length <= current.length) return target
  const backlog = target.length - current.length
  const step = backlog > 260 ? 8 : backlog > 120 ? 4 : backlog > 60 ? 2 : 1
  return target.slice(0, current.length + Math.min(step, backlog))
}
