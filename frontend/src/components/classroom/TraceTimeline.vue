<script setup lang="ts">
import { Activity, Check, CircleAlert, CircleDot, Clock3, ChevronDown, RefreshCw } from 'lucide-vue-next'
import { onMounted, ref, watch } from 'vue'
import { cn } from '@/lib/utils'
import { fetchDiscussionRuns, fetchDiscussionTrace, type DiscussionRun, type DiscussionTrace } from '@/api/trace'
import type { DiscussionTrace as LiveTrace, TraceStep } from '@/lib/discussionTrace'

const props = defineProps<{ trace: LiveTrace; conversationId?: number | null }>()
const runs = ref<DiscussionRun[]>([])
const selectedRun = ref<number | null>(null)
const detail = ref<DiscussionTrace | null>(null)
const loading = ref(false)
const error = ref('')

async function loadRuns() {
  if (!props.conversationId) { runs.value = []; detail.value = null; return }
  loading.value = true; error.value = ''
  try {
    runs.value = await fetchDiscussionRuns(props.conversationId)
    if (selectedRun.value && runs.value.some((run) => run.id === selectedRun.value)) await selectRun(selectedRun.value)
    else if (runs.value[0]) await selectRun(runs.value[0].id)
    else detail.value = null
  } catch { error.value = '历史轨迹暂时无法加载' } finally { loading.value = false }
}
async function selectRun(id: number) {
  if (!props.conversationId) return
  selectedRun.value = id
  try { detail.value = await fetchDiscussionTrace(props.conversationId, id) } catch { error.value = '这次运行的详情暂时无法加载' }
}
onMounted(() => void loadRuns())
watch(() => [props.conversationId, props.trace.status], () => void loadRuns())

function formatTime(value?: string) { if (!value) return '--'; const date = new Date(value); return Number.isNaN(date.getTime()) ? '--' : date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', fractionalSecondDigits: 3 }) }
function formatDuration(value?: number) { if (value == null) return '--'; if (value < 1000) return `${value} ms`; return `${(value / 1000).toFixed(2)} s` }
function formatCost(run: DiscussionRun) { return run.estimatedCost == null ? '未配置价格' : `${run.currency || 'USD'} ${run.estimatedCost.toFixed(6)}` }
function statusLabel(status: string) { return ({ completed: '完成', failed: '失败', running: '运行中', queued: '排队中', cancelled: '已取消' } as Record<string, string>)[status] || status }
function iconFor(step: TraceStep) { if (step.status === 'error') return CircleAlert; if (step.status === 'waiting') return Clock3; return Check }
function timeOf(value: string) { const date = new Date(value); return Number.isNaN(date.getTime()) ? '' : date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', fractionalSecondDigits: 3 }) }
</script>

<template>
  <div class="discussion-trace flex min-h-0 flex-1 flex-col bg-[#f5f1e8] px-3 py-3 dark:bg-[#171614]">
    <div class="mb-3 flex items-center justify-between"><div class="flex items-center gap-2"><span class="flex size-7 items-center justify-center rounded-lg bg-[#eadbc7] text-[#8f5b38] dark:bg-[#3b2e24] dark:text-[#e6b184]"><Activity class="size-4" /></span><div><span class="block text-xs font-semibold text-[#4e4033] dark:text-[#f3e9d8]">历史运行轨迹</span><span class="block text-[10px] text-[#9a8f82]">公开的讨论阶段与耗时</span></div></div><button type="button" class="rounded-lg p-1.5 text-[#9a8f82] hover:bg-[#fffdf8] hover:text-[#8f5b38] dark:hover:bg-[#29251f]" title="刷新" @click="loadRuns"><RefreshCw class="size-3.5" /></button></div>
    <p v-if="error" class="mb-2 rounded-md border border-rose-200 bg-rose-50 px-2 py-1.5 text-[11px] text-rose-700">{{ error }}</p>
    <div v-if="loading && !runs.length" class="py-8 text-center text-xs text-zinc-400">正在读取历史轨迹…</div>
    <div v-else-if="!runs.length" class="flex flex-1 flex-col items-center justify-center text-center text-zinc-400"><CircleDot class="size-7 text-zinc-300" /><p class="mt-3 text-xs">还没有可查看的运行记录</p></div>
    <template v-else>
      <div class="mb-3 flex gap-1 overflow-x-auto pb-1"><button v-for="run in runs" :key="run.id" type="button" :class="cn('min-w-[142px] rounded-xl border px-2.5 py-2 text-left transition', selectedRun === run.id ? 'border-amber-300 bg-[#fff5df] shadow-sm dark:border-amber-800 dark:bg-amber-950/30' : 'border-[#ded6c8] bg-[#fffdf8]/75 hover:border-amber-200 dark:border-[#453b31] dark:bg-[#24211d]/75')" @click="selectRun(run.id)"><div class="flex items-center justify-between gap-2"><span class="text-[11px] font-semibold text-[#5a4b3d] dark:text-[#f3e9d8]">运行 #{{ run.id }}</span><span :class="run.status === 'failed' ? 'text-rose-600' : run.status === 'completed' ? 'text-emerald-700' : 'text-amber-700'" class="text-[10px]">{{ statusLabel(run.status) }}</span></div><div class="mt-1 text-[10px] tabular-nums text-[#9a8f82]">{{ formatTime(run.createdAt) }} · {{ formatDuration(run.durationMs) }}</div></button></div>
      <div v-if="detail" class="min-h-0 flex-1 overflow-y-auto pr-1"><div class="grid grid-cols-2 gap-1.5 sm:grid-cols-4"><div class="rounded-xl border border-[#ded6c8] bg-[#fffdf8]/85 p-2 dark:border-[#453b31] dark:bg-[#24211d]/85"><p class="text-[10px] text-[#9a8f82]">Token</p><p class="mt-1 text-xs font-semibold tabular-nums text-[#4e4033] dark:text-[#f3e9d8]">{{ detail.run.totalTokens.toLocaleString() }}</p></div><div class="rounded-xl border border-[#ded6c8] bg-[#fffdf8]/85 p-2 dark:border-[#453b31] dark:bg-[#24211d]/85"><p class="text-[10px] text-[#9a8f82]">耗时</p><p class="mt-1 text-xs font-semibold tabular-nums text-[#4e4033] dark:text-[#f3e9d8]">{{ formatDuration(detail.run.durationMs) }}</p></div><div class="rounded-xl border border-[#ded6c8] bg-[#fffdf8]/85 p-2 dark:border-[#453b31] dark:bg-[#24211d]/85"><p class="text-[10px] text-[#9a8f82]">模型</p><p class="mt-1 truncate text-xs font-semibold text-[#4e4033] dark:text-[#f3e9d8]" :title="detail.run.modelId">{{ detail.run.modelId || '未记录' }}</p></div><div class="rounded-xl border border-[#ded6c8] bg-[#fffdf8]/85 p-2 dark:border-[#453b31] dark:bg-[#24211d]/85"><p class="text-[10px] text-[#9a8f82]">预计费用</p><p class="mt-1 truncate text-xs font-semibold tabular-nums text-[#4e4033] dark:text-[#f3e9d8]" :title="formatCost(detail.run)">{{ formatCost(detail.run) }}</p></div></div><div class="mt-3 space-y-1.5"><details v-for="span in detail.spans" :key="span.spanId" class="group rounded-xl border border-[#ded6c8] bg-[#fffdf8]/85 dark:border-[#453b31] dark:bg-[#24211d]/85"><summary class="flex cursor-pointer list-none items-center gap-2 px-2.5 py-2 text-[11px]"><ChevronDown class="size-3 text-[#9a8f82] transition group-open:rotate-180" /><span class="rounded bg-[#f2e8d9] px-1.5 py-0.5 font-mono text-[10px] text-[#8f5b38] dark:bg-[#3b2e24] dark:text-[#e6b184]">{{ span.kind }}</span><span class="min-w-0 flex-1 truncate font-medium text-[#5a4b3d] dark:text-[#eadfce]">{{ span.name }}</span><span class="tabular-nums text-[#9a8f82]">{{ formatDuration(span.durationMs) }}</span><span :class="span.status === 'error' ? 'text-rose-600' : 'text-emerald-700'">{{ span.status === 'error' ? '失败' : '完成' }}</span></summary><div class="space-y-1 border-t border-[#eee5d8] px-3 py-2 text-[10px] leading-5 text-[#807568] dark:border-[#3b342c] dark:text-[#b9ad9d]"><p v-if="span.inputSummary">输入摘要：{{ span.inputSummary }}</p><p v-if="span.outputSummary">输出摘要：{{ span.outputSummary }}</p><p v-if="span.inputTokens || span.outputTokens">Token：{{ span.inputTokens || 0 }} 输入 / {{ span.outputTokens || 0 }} 输出<span v-if="span.attempt"> · 第 {{ span.attempt }} 次尝试</span></p><p v-if="span.errorMessage" class="text-rose-600">{{ span.errorMessage }}</p></div></details></div></div>
    </template>
    <div class="mt-3 border-t border-zinc-200 pt-3 dark:border-zinc-800"><p class="mb-2 text-[10px] font-semibold uppercase tracking-[0.16em] text-zinc-400">本次实时过程</p><ol v-if="trace.steps.length" class="max-h-44 space-y-2 overflow-y-auto pr-1"><li v-for="(step, index) in trace.steps" :key="step.id" class="relative flex gap-2"><div v-if="index < trace.steps.length - 1" class="absolute top-5 bottom-[-8px] left-2 w-px bg-zinc-200 dark:bg-zinc-800" /><span class="relative z-10 flex size-4 shrink-0 items-center justify-center rounded-full border border-zinc-200 bg-white text-zinc-500 dark:border-zinc-700 dark:bg-zinc-900"><component :is="iconFor(step)" class="size-2.5" /></span><div class="min-w-0 flex-1"><div class="flex justify-between gap-2"><p class="truncate text-[10px] font-medium text-zinc-700 dark:text-zinc-200">{{ step.title }}</p><time class="shrink-0 text-[9px] text-zinc-400">{{ timeOf(step.timestamp) }}</time></div><p v-if="step.detail" class="truncate text-[10px] text-zinc-400">{{ step.detail }}</p></div></li></ol><p v-else class="text-[10px] text-zinc-400">开始讨论后，这里会显示实时事件。</p></div>
  </div>
</template>
