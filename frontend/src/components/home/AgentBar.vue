<script setup lang="ts">
/**
 * AgentBar —— 文档 §5.5（Composer 右上：课堂角色配置）。
 *
 * 触发按钮展示：标题 + 教师头像 + 学员头像组（preset）或 Shuffle（auto）+ TTS 状态。
 * 展开面板含：教师行（TeacherVoicePill）、预设/自动 Tab、预设角色列表、自动模式说明。
 *
 * 角色**不是常量**了，整个池子来自后端 `GET /api/v1/roles`（见 stores/roles.ts）。
 * 池子是异步来的，所以每个用到角色的地方都得有加载态。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { storeToRefs } from 'pinia'
import { CheckboxIndicator, CheckboxRoot } from 'reka-ui'
import { Check, Shuffle, Sparkles, Volume2, VolumeX } from 'lucide-vue-next'

import AgentVoicePill from '@/components/home/AgentVoicePill.vue'
import UiTooltip from '@/components/ui/UiTooltip.vue'
import { useRolesStore } from '@/stores/roles'
import type { AgentBarMode } from '@/types/role'
import { cn } from '@/lib/utils'

const { t } = useI18n()

const rolesStore = useRolesStore()
const { teacher, selectable, loading, error, loaded } = storeToRefs(rolesStore)

/** 与父级（HomeView）双向同步的配置，便于提交时读取 */
const mode = defineModel<AgentBarMode>('mode', { default: 'preset' })
const selectedIds = defineModel<string[]>('selectedIds', { default: () => ['assist', 'curious'] })
const ttsEnabled = defineModel<boolean>('tts', { default: true })

/**
 * 教师音色。默认值来自角色池，而池子是异步拉回来的，所以这里只能是空串——
 * 真实默认值由下面的 watch 在教师到位后回填。
 *
 * 不要写死音色 ID：改池子里那个教师的 voice_id，前端就不跟着变了。
 */
const teacherVoice = defineModel<string>('teacherVoice', { default: '' })

const rootRef = ref<HTMLElement | null>(null)
const open = ref(false)

/** 每个角色各自选的音色，等池子到位后补默认值 */
const roleVoices = ref<Record<string, string>>({})

const selectedRoles = computed(() => selectable.value.filter((r) => selectedIds.value.includes(r.id)))
const visibleAvatars = computed(() => selectedRoles.value.slice(0, 4))
const overflowCount = computed(() => Math.max(0, selectedRoles.value.length - 4))

const autoPreviewAvatars = computed(() => selectable.value.slice(0, 3))

// 回填默认音色。只填空值——用户已经改过的选择不能被覆盖
watch(
  teacher,
  (next) => {
    if (next && !teacherVoice.value) teacherVoice.value = next.voice
  },
  { immediate: true },
)

watch(
  selectable,
  (list) => {
    for (const r of list) {
      if (!(r.id in roleVoices.value)) roleVoices.value[r.id] = r.voice
    }
  },
  { immediate: true },
)

function toggleOpen() {
  open.value = !open.value
}

function toggleRole(id: string) {
  const next = selectedIds.value.slice()
  const i = next.indexOf(id)
  if (i >= 0) next.splice(i, 1)
  else next.push(id)
  selectedIds.value = next
}

function onDocMouseDown(e: MouseEvent) {
  if (rootRef.value && !rootRef.value.contains(e.target as Node)) open.value = false
}

watch(open, (v) => {
  if (!v) return
  // 展开时确保静止态（防止外部点击残留选中）
})

onMounted(() => {
  document.addEventListener('mousedown', onDocMouseDown)
  rolesStore.load()
})
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocMouseDown))

defineExpose({ roleVoices })
</script>

<template>
  <div ref="rootRef" class="relative w-96">
    <!-- 触发按钮 -->
    <UiTooltip :content="t('agentBar.configure')">
      <button
        type="button"
        class="group flex w-full cursor-pointer items-center gap-2 rounded-full border border-border/50 px-2.5 py-2 text-muted-foreground/70 transition-all hover:bg-muted/60 hover:text-foreground"
        @click="toggleOpen"
      >
        <span class="hidden flex-1 truncate text-left text-xs font-medium text-muted-foreground/60 sm:block">
          {{ open ? t('agentBar.expandedTitle') : t('agentBar.readyToLearn') }}
        </span>

        <div class="flex shrink-0 items-center gap-1.5">
          <!-- 教师 -->
          <div class="size-8 overflow-hidden rounded-full ring-2 ring-blue-400/40">
            <img v-if="teacher" :src="teacher.avatar" alt="" class="size-full object-cover" />
            <div v-else class="size-full animate-pulse bg-muted" />
          </div>

          <!-- auto：叠头像 + Shuffle -->
          <template v-if="mode === 'auto'">
            <div class="flex -space-x-2">
              <template v-if="loaded">
                <div
                  v-for="r in autoPreviewAvatars"
                  :key="r.id"
                  class="size-6 overflow-hidden rounded-full ring-[1.5px] ring-background"
                >
                  <img :src="r.avatar" alt="" class="size-full object-cover" />
                </div>
              </template>
              <template v-else>
                <div
                  v-for="i in 3"
                  :key="i"
                  class="size-6 animate-pulse rounded-full bg-muted ring-[1.5px] ring-background"
                />
              </template>
            </div>
            <Shuffle class="size-4 text-violet-400" />
          </template>

          <!-- preset：已选头像 + 溢出计数 -->
          <template v-else>
            <template v-if="loaded">
              <div
                v-for="r in visibleAvatars"
                :key="r.id"
                class="size-6 overflow-hidden rounded-full ring-[1.5px] ring-background"
              >
                <img :src="r.avatar" alt="" class="size-full object-cover" />
              </div>
              <div
                v-if="overflowCount > 0"
                class="flex size-6 items-center justify-center rounded-full bg-muted text-[10px] font-medium text-muted-foreground"
              >
                +{{ overflowCount }}
              </div>
            </template>
            <template v-else>
              <div
                v-for="i in 2"
                :key="i"
                class="size-6 animate-pulse rounded-full bg-muted ring-[1.5px] ring-background"
              />
            </template>
          </template>

          <!-- TTS 状态 -->
          <Volume2 v-if="ttsEnabled" class="size-3.5 text-muted-foreground/40" />
          <VolumeX v-else class="size-3.5 text-muted-foreground/30" />
        </div>
      </button>
    </UiTooltip>

    <!-- 展开面板 -->
    <Transition
      enter-active-class="transition duration-200 ease-out"
      enter-from-class="-translate-y-1 scale-[0.97] opacity-0"
      leave-active-class="transition duration-150 ease-in"
      leave-to-class="-translate-y-1 scale-[0.97] opacity-0"
    >
      <div
        v-if="open"
        class="absolute top-full right-0 z-50 mt-1 w-96 rounded-2xl bg-white/95 px-2 py-1.5 ring-1 ring-black/[0.04] shadow-[0_1px_8px_-2px_rgba(0,0,0,0.06)] backdrop-blur-sm dark:bg-slate-800/95"
      >
        <!-- 教师行 -->
        <div class="mb-2 flex items-center gap-2 rounded-lg bg-primary/5 px-2.5 py-1.5">
          <div class="size-7 shrink-0 overflow-hidden rounded-full">
            <img v-if="teacher" :src="teacher.avatar" alt="" class="size-full object-cover" />
            <div v-else class="size-full animate-pulse bg-muted" />
          </div>
          <span v-if="teacher" class="text-[13px] font-medium">{{ teacher.name }}</span>
          <div v-else-if="loading" class="h-3 w-14 animate-pulse rounded bg-muted" />
          <span v-else class="text-[13px] font-medium text-muted-foreground/40">—</span>
          <div v-if="teacher" class="ml-auto">
            <AgentVoicePill v-model="teacherVoice" />
          </div>
        </div>

        <!-- 模式 Tab -->
        <div class="mb-2 flex rounded-lg border bg-muted/30 p-0.5">
          <button
            type="button"
            :class="
              cn(
                'flex-1 rounded-md py-1.5 text-center text-xs font-medium transition-all',
                mode === 'preset'
                  ? 'bg-background text-foreground shadow-sm'
                  : 'text-muted-foreground hover:text-foreground',
              )
            "
            @click="mode = 'preset'"
          >
            {{ t('agentBar.preset') }}
          </button>
          <button
            type="button"
            :class="
              cn(
                'flex flex-1 items-center justify-center gap-1 rounded-md py-1.5 text-center text-xs font-medium transition-all',
                mode === 'auto'
                  ? 'bg-background text-foreground shadow-sm'
                  : 'text-muted-foreground hover:text-foreground',
              )
            "
            @click="mode = 'auto'"
          >
            <Sparkles class="h-3 w-3" />
            {{ t('agentBar.auto') }}
          </button>
        </div>

        <!-- 预设角色列表 -->
        <div v-if="mode === 'preset'" class="max-h-56 overflow-y-auto">
          <!-- 加载中 -->
          <div v-if="loading" class="flex flex-col gap-1 px-2.5 py-1">
            <div v-for="i in 3" :key="i" class="flex items-center gap-2 py-1.5">
              <div class="size-3.5 shrink-0 animate-pulse rounded bg-muted" />
              <div class="size-7 shrink-0 animate-pulse rounded-full bg-muted" />
              <div class="h-3 flex-1 animate-pulse rounded bg-muted" />
            </div>
          </div>

          <!-- 加载失败：给一条重试路径，别让面板空着 -->
          <div v-else-if="error" class="flex flex-col items-center gap-2 px-4 py-6">
            <p class="text-[11px] text-destructive/80">{{ t('agentBar.loadRolesFailed') }}</p>
            <p class="w-full truncate text-center text-[10px] text-muted-foreground/50">
              {{ error.message }}
            </p>
            <button
              type="button"
              class="mt-1 rounded-md border border-border/60 px-2.5 py-1 text-[11px] transition-colors hover:bg-muted"
              @click="rolesStore.load()"
            >
              {{ t('common.retry') }}
            </button>
          </div>

          <!-- 池子是空的（拉到过，但一条都没有） -->
          <p v-else-if="!loaded" class="px-4 py-6 text-center text-[11px] text-muted-foreground/50">
            {{ t('agentBar.emptyRoles') }}
          </p>

          <!-- 正常列表 -->
          <template v-else>
            <div
              v-for="r in selectable"
              :key="r.id"
              :class="
                cn(
                  'flex w-full items-center gap-2 rounded-lg px-2.5 py-1.5 transition-colors',
                  selectedIds.includes(r.id) ? 'bg-primary/5' : 'hover:bg-muted/50',
                )
              "
            >
              <CheckboxRoot
                :model-value="selectedIds.includes(r.id)"
                class="flex size-3.5 shrink-0 items-center justify-center rounded border border-border data-[state=checked]:border-violet-500 data-[state=checked]:bg-violet-500 data-[state=checked]:text-white"
                @update:model-value="toggleRole(r.id)"
              >
                <CheckboxIndicator>
                  <Check class="size-3" />
                </CheckboxIndicator>
              </CheckboxRoot>
              <div
                class="size-7 shrink-0 overflow-hidden rounded-full ring-1 ring-border/40"
                :style="
                  selectedIds.includes(r.id) ? { boxShadow: `0 0 0 2px ${r.color}30` } : undefined
                "
              >
                <img :src="r.avatar" alt="" class="size-full object-cover" />
              </div>
              <span class="min-w-0 flex-1 truncate text-[13px] font-medium">{{ r.name }}</span>
              <span class="w-[52px] shrink-0 text-right text-[10px] text-muted-foreground/50">
                {{ r.role }}
              </span>
              <AgentVoicePill v-model="roleVoices[r.id]" />
            </div>
          </template>
        </div>

        <!-- 自动模式说明 -->
        <div v-else class="flex flex-col items-center gap-4 pt-6 pb-3">
          <div class="relative flex items-center justify-center">
            <span
              class="absolute size-10 animate-ping rounded-full bg-violet-400/20 [animation-duration:3s]"
            />
            <span
              class="absolute size-12 animate-pulse rounded-full bg-violet-400/10 [animation-duration:2.5s]"
            />
            <Shuffle class="relative size-5 text-violet-400" />
          </div>
          <p class="text-[11px] text-muted-foreground/60">{{ t('agentBar.autoHint') }}</p>
          <p class="text-[10px] text-muted-foreground/40">{{ t('agentBar.autoVoiceHint') }}</p>
        </div>
      </div>
    </Transition>
  </div>
</template>
