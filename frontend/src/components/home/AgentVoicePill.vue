<script setup lang="ts">
/**
 * AgentVoicePill —— 音色选择 pill（文档 §5.5）。
 * v-model 绑定当前音色 id；下拉是**平铺列表**，支持搜索、试听。
 *
 * 不分组：只有一家 TTS 厂商，没有可分的轴；音色也就 6 个，一屏看完。性别作为一项
 * 数据跟在名字后面显示，不额外加分组标题。
 *
 * 下拉用 reka-ui 的 Popover 并挂到 body 上（PopoverPortal），不是就地 absolute：
 * 这个 pill 会出现在两个位置——面板顶部的教师行，和 `max-h-56 overflow-y-auto` 的
 * 角色列表里。就地定位的话，列表里那几行的弹层会被滚动容器裁掉，挂到 body 上就没有
 * 这个问题，顺带还能自动避让视口边缘。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { PopoverContent, PopoverPortal, PopoverRoot, PopoverTrigger } from 'reka-ui'
import { Check, ChevronDown, Loader2, Play, Search, Square, Volume2 } from 'lucide-vue-next'
import { toast } from 'vue-sonner'

import { useVoicesStore } from '@/stores/voices'
import { cn } from '@/lib/utils'

const { t } = useI18n()

const voicesStore = useVoicesStore()

const model = defineModel<string>({ required: true })
const popOpen = ref(false)
const keyword = ref('')

const label = computed(() => voicesStore.nameOf(model.value))
const disabled = computed(() => voicesStore.voices.length === 0)

const options = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return voicesStore.voices
  return voicesStore.voices.filter(
    (v) => v.name.toLowerCase().includes(kw) || v.id.toLowerCase().includes(kw),
  )
})

const isEmpty = computed(() => options.value.length === 0)

function pick(id: string) {
  model.value = id
  popOpen.value = false
}

// 试听失败要有反馈，否则点了没反应像是卡住了
async function preview(id: string) {
  try {
    await voicesStore.togglePreview(id)
  } catch (e) {
    toast.error(t('agentBar.previewFailed'), {
      description: e instanceof Error ? e.message : String(e),
    })
  }
}

// 关掉就清空搜索词，下次打开是完整列表
watch(popOpen, (open) => {
  if (!open) keyword.value = ''
})

// 目录由后端提供，load 幂等——这个 pill 每个角色行渲染一个，都调也只会发一个请求
onMounted(() => {
  voicesStore.load()
})

// 只在自己这个音色正是那一个时才停
onBeforeUnmount(() => voicesStore.stopPreview(model.value))
</script>

<template>
  <PopoverRoot v-model:open="popOpen">
    <PopoverTrigger as-child>
      <button
        type="button"
        :class="
          cn(
            'flex h-6 w-[100px] shrink-0 items-center gap-1.5 rounded-full bg-primary/10 px-2.5 text-[11px] text-primary/80 transition-colors hover:bg-primary/20',
            disabled && 'cursor-not-allowed bg-muted/40 text-muted-foreground/30',
          )
        "
        :disabled="disabled"
      >
        <Volume2 class="size-3 shrink-0" />
        <span class="min-w-0 flex-1 truncate text-left">
          {{ disabled ? t('agentBar.noVoice') : label }}
        </span>
        <ChevronDown class="size-3 shrink-0 opacity-60" />
      </button>
    </PopoverTrigger>

    <PopoverPortal>
      <!--
        必须 stop 掉 mousedown：内容是 portal 到 body 的，不拦就会被 AgentBar 的
        「点面板外面就关掉」判成外部点击，按下那一刻整块面板先没了，click 轮不到触发。
      -->
      <PopoverContent
        side="bottom"
        align="end"
        :side-offset="6"
        :collision-padding="12"
        class="z-[60] w-80 overflow-hidden rounded-xl border border-border bg-popover shadow-lg outline-none sm:w-96"
        @mousedown.stop
      >
        <div class="border-b border-border/60 p-2">
          <div class="relative">
            <Search
              class="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground/50"
            />
            <input
              v-model="keyword"
              type="text"
              :placeholder="t('agentBar.searchVoice')"
              class="h-8 w-full rounded-md border border-input pr-3 pl-8 text-sm outline-none focus:ring-1 focus:ring-violet-400/40"
            />
          </div>
        </div>

        <div class="max-h-64 overflow-y-auto p-1">
          <!--
            一行拆成两个 button，不是嵌套：选中行和试听是两个动作，而 button 里套
            button 是非法 HTML，浏览器会把它拆开，点击行为跟着乱。
          -->
          <div
            v-for="v in options"
            :key="v.id"
            class="flex w-full items-center gap-1 rounded-md pr-1 transition-colors hover:bg-muted/60"
          >
            <button
              type="button"
              class="flex min-w-0 flex-1 items-center gap-2 py-1.5 pl-2 text-left"
              @click="pick(v.id)"
            >
              <span class="min-w-0 flex-1 truncate text-[13px]">{{ v.name }}</span>
              <span class="shrink-0 text-[10px] text-muted-foreground/40">{{ v.gender }}</span>
            </button>

            <!--
              用原生 title 而不是 UiTooltip：后者 portal 到 body，落在 AgentBar
              那个「点面板外面就关掉」的判定范围里，鼠标滑到气泡上再点会把整个面板关掉。
            -->
            <button
              type="button"
              class="flex size-5 shrink-0 items-center justify-center rounded transition-colors hover:bg-muted"
              :title="t('agentBar.tryVoice')"
              :aria-label="t('agentBar.tryVoice')"
              @click="preview(v.id)"
            >
              <Loader2
                v-if="voicesStore.previewLoadingId === v.id"
                class="size-3 animate-spin text-muted-foreground/60"
              />
              <Square v-else-if="voicesStore.playingId === v.id" class="size-3 text-violet-500" />
              <Play v-else class="size-3 text-muted-foreground/40" />
            </button>

            <Check v-if="model === v.id" class="size-3.5 shrink-0 text-violet-500" />
            <span v-else class="size-3.5 shrink-0" />
          </div>

          <div v-if="isEmpty" class="px-2 py-6 text-center text-[13px] text-muted-foreground/50">
            {{ t('agentBar.noVoice') }}
          </div>
        </div>
      </PopoverContent>
    </PopoverPortal>
  </PopoverRoot>
</template>
