/**
 * 收录等待的共享逻辑：单篇文档的收敛轮询。
 *
 * 知识库页（stores/knowledge.ts）与首页课程材料（views/HomeView.vue）共用同一套口径：
 * 一份卡在 pending 的文档（例如 worker 没起来）不能把界面永远锁在"处理中"。
 * 两边各写一份的话阈值与判据迟早会漂（首页此前写死的 10 分钟就是这么漂出来的）。
 */
import {
  fetchKnowledgeDocument,
  type KnowledgeDocument,
} from '@/api/knowledge'

/**
 * 收录等待上限。一份大文档解析几分钟很正常，但不能无限等：
 * 一份卡在 pending 的文档（例如 worker 没起来）会把界面永远锁在"处理中"。
 */
export const INGEST_POLL_TIMEOUT_MS = 15 * 60 * 1000

/** 文档是否已经到终态（ready / failed）。终态之后不会再有变化。 */
export function isIngestSettled(document: KnowledgeDocument): boolean {
  return document.status === 'ready' || document.status === 'failed'
}

/** 收录等待的时钟：到点抛出。 */
export interface SettleClock {
  /**
   * 到点时抛出。SSE 路每秒调一次（没有事件也要查），轮询路每轮调一次。
   */
  check(): void
}

export interface SettleClockOptions {
  /** 等待上限 */
  timeoutMs?: number
}

/**
 * "文档处理超时"的判据，SSE 与轮询两条路共用一份。
 *
 * 上限本身是必须的：一份卡在 pending 的文档（例如 worker 没起来）会把界面永远
 * 锁在"处理中"，用户连下一份都传不了。
 */
export function createSettleClock(options: SettleClockOptions = {}): SettleClock {
  const timeoutMs = options.timeoutMs ?? INGEST_POLL_TIMEOUT_MS
  const deadline = Date.now() + timeoutMs

  return {
    check() {
      if (Date.now() >= deadline) {
        throw new Error('文档处理超时，请稍后刷新查看状态')
      }
    },
  }
}

export interface PollDocumentOptions {
  /** 复用已有时钟（SSE 降级到轮询时用，保证两条路的超时口径连续） */
  clock?: SettleClock
  /** 两次查询之间的间隔 */
  intervalMs?: number
  /** 等待上限；仅在未传 clock 时生效 */
  timeoutMs?: number
  /** 查询单篇文档的实现；测试可注入替身 */
  fetchDocument?: (id: number) => Promise<KnowledgeDocument>
}

/**
 * 逐次轮询单篇文档直到终态，返回最后那一帧。
 *
 * 传入文档对象时假定它是当前快照（先等一个间隔再查，与 SSE 兜底路径一致）；
 * 传入文档 ID 时先立刻查一次（首页材料上传后只有 ID）。
 */
export async function pollDocumentUntilSettled(
  target: number | KnowledgeDocument,
  options: PollDocumentOptions = {},
): Promise<KnowledgeDocument> {
  const id = typeof target === 'number' ? target : target.id
  const fetchDocument = options.fetchDocument ?? fetchKnowledgeDocument
  const intervalMs = options.intervalMs ?? 1000
  const clock = options.clock ?? createSettleClock({ timeoutMs: options.timeoutMs })

  let current: KnowledgeDocument | null = typeof target === 'number' ? null : target
  for (;;) {
    if (current !== null && isIngestSettled(current)) return current

    clock.check()

    if (current !== null) {
      await new Promise((resolve) => setTimeout(resolve, intervalMs))
    }
    current = await fetchDocument(id)
  }
}
