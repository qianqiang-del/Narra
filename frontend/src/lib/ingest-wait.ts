/**
 * 收录等待的共享逻辑：解析环境状态 + 单篇文档的收敛轮询。
 *
 * 知识库页（stores/knowledge.ts）与首页课程材料（views/HomeView.vue）共用同一套口径：
 * 首次上传 PDF / Office / 图片时后端要现场准备解析环境（分钟级），这段时间不该算成
 * "这份文档处理超时" —— 准备期间只看进度有没有推进，准备结束后超时窗口从头算。
 * 两边各写一份的话阈值与判据迟早会漂（首页此前写死的 10 分钟就是这么漂出来的）。
 */
import { ref } from 'vue'

import {
  fetchKnowledgeDocument,
  fetchKnowledgeParserStatus,
  type KnowledgeDocument,
  type KnowledgeParserStatus,
} from '@/api/knowledge'

/**
 * 准备结束后的收录等待上限。一份大文档解析几分钟很正常，但不能无限等：
 * 一份卡在 pending 的文档（例如 worker 没起来）会把界面永远锁在"处理中"。
 */
export const INGEST_POLL_TIMEOUT_MS = 15 * 60 * 1000

/**
 * 环境准备连续多久没有进展才算卡住。
 *
 * 判据不是"正在准备"就无限等，而是"进度还在变"；但阈值必须盖过准备过程中最长的
 * 静默步骤：安装依赖时后端只在开始时上报一次进度（见 documentparser 的 resolve），
 * uv pip install 实测要十几分钟，而后端自己的上限是 prepare_timeout = 20m。
 * 阈值短于它就会把正常安装误报成卡住 —— 后端 20m 到点会把文档落成 failed，
 * 这里的 25m 只用来兜底"进程死了、永远没人写终态"。
 *
 * 与后端配置的耦合是已知的：以后若把 prepare_timeout 调得更大，这里要同步。
 */
export const INGEST_PREPARE_STALL_MS = 25 * 60 * 1000

/** 文档是否已经到终态（ready / failed）。终态之后不会再有变化。 */
export function isIngestSettled(document: KnowledgeDocument): boolean {
  return document.status === 'ready' || document.status === 'failed'
}

/**
 * 解析环境状态（进程内共享）。
 *
 * 失败静默：它只是等待口径与提示的依据，探测不到就当不知道 —— 不该因为一次状态
 * 探测失败把上传或等待拦住。环境备好之后 ready 为真，提示自然消失，前端不需要
 * 自己记"是不是第一次"。
 */
const parserStatus = ref<KnowledgeParserStatus | null>(null)

/** 重新探测一次解析环境状态；失败保留旧值。并发调用合并成一次请求。 */
let refreshInFlight: Promise<void> | null = null

export function refreshParserStatus(): Promise<void> {
  if (refreshInFlight) return refreshInFlight
  refreshInFlight = fetchKnowledgeParserStatus()
    .then((status) => {
      parserStatus.value = status
    })
    .catch(() => {
      /* 探测失败不影响等待：按"未在准备"处理 */
    })
    .finally(() => {
      refreshInFlight = null
    })
  return refreshInFlight
}

/** 当前解析环境状态；还没探测过时为 null。 */
export function currentParserStatus(): KnowledgeParserStatus | null {
  return parserStatus.value
}

/**
 * 是否需要再探测一次解析环境状态：还没问过，或能力开着但环境还没备好。
 * 环境备好之后（或压根没开解析能力）这个接口就没必要再打了。
 */
export function parserStatusNeedsRefresh(status: KnowledgeParserStatus | null): boolean {
  return status === null || (status.enabled && !status.ready)
}

/**
 * 供组件与仓库读取共享状态。
 * 知识库页的"首次上传需要准备环境"提示与首页材料的等待判据读的是同一份。
 */
export function useIngestParserStatus() {
  return { parserStatus, refreshParserStatus }
}

/** 收录等待的时钟：到点或准备卡住时抛出。 */
export interface SettleClock {
  /**
   * 到点或卡住时抛出。SSE 路每秒调一次（没有事件也要查），轮询路每轮调一次。
   */
  check(): void
}

export interface SettleClockOptions {
  /** 解析环境状态读取；默认走本模块的共享状态，测试可注入替身 */
  status?: () => KnowledgeParserStatus | null
  /** 准备结束后的等待上限 */
  timeoutMs?: number
  /** 环境准备无进展的判据 */
  prepareStallMs?: number
}

/**
 * "文档处理超时"与"环境准备卡住"的判据，SSE 与轮询两条路共用一份。
 *
 * 首次上传时后端可能在准备解析环境（分钟级），这段等待不该吃掉"文档处理超时"
 * 的窗口：准备期间只看进度有没有在动，准备结束后窗口从头算（见 INGEST_PREPARE_STALL_MS）。
 * 上限本身是必须的：一份卡在 pending 的文档（例如 worker 没起来）会把界面永远
 * 锁在"处理中"，用户连下一份都传不了。
 */
export function createSettleClock(options: SettleClockOptions = {}): SettleClock {
  const readStatus = options.status ?? currentParserStatus
  const timeoutMs = options.timeoutMs ?? INGEST_POLL_TIMEOUT_MS
  const prepareStallMs = options.prepareStallMs ?? INGEST_PREPARE_STALL_MS

  let deadline = Date.now() + timeoutMs
  let lastProgress = ''
  let progressChangedAt = Date.now()
  let wasPreparing = false

  return {
    check() {
      const status = readStatus()
      if (status?.preparing) {
        const progress = status.progress
        if (progress !== lastProgress) {
          lastProgress = progress
          progressChangedAt = Date.now()
        }
        if (Date.now() - progressChangedAt >= prepareStallMs) {
          throw new Error('解析环境准备似乎卡住了，请稍后刷新查看状态')
        }
        wasPreparing = true
        return
      }
      if (wasPreparing) {
        // 准备刚结束：之前那段时间是环境准备，不是这一份文档的处理时长
        wasPreparing = false
        deadline = Date.now() + timeoutMs
      }
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
  /** 准备结束后的等待上限；仅在未传 clock 时生效 */
  timeoutMs?: number
  /** 环境准备无进展的判据；仅在未传 clock 时生效 */
  prepareStallMs?: number
  /** 查询单篇文档的实现；测试可注入替身 */
  fetchDocument?: (id: number) => Promise<KnowledgeDocument>
  /** 环境状态读取/刷新；默认走本模块的共享状态 */
  status?: () => KnowledgeParserStatus | null
  refreshStatus?: () => Promise<void>
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
  const readStatus = options.status ?? currentParserStatus
  const refresh = options.refreshStatus ?? refreshParserStatus
  const intervalMs = options.intervalMs ?? 1000
  const clock =
    options.clock ??
    createSettleClock({
      status: readStatus,
      timeoutMs: options.timeoutMs,
      prepareStallMs: options.prepareStallMs,
    })

  let current: KnowledgeDocument | null = typeof target === 'number' ? null : target
  for (;;) {
    if (current !== null && isIngestSettled(current)) return current

    // 只在"还没问过"或"能力开着但环境没好"时问：环境备好之后（或压根没开
    // 解析能力）这个接口就没必要再打了。
    if (parserStatusNeedsRefresh(readStatus())) {
      await refresh()
    }
    clock.check()

    if (current !== null) {
      await new Promise((resolve) => setTimeout(resolve, intervalMs))
    }
    current = await fetchDocument(id)
  }
}
