<script setup lang="ts">
/**
 * GreetingBar —— 文档 §5.4（Composer 左上：头像 + 昵称 + 个人简介）。
 *
 * 收起态是一枚胶囊；点击后展开面板，可编辑昵称、挑头像、写简介。
 * 资料存在 profile store 里（并同步 localStorage），首页提交与课堂圆桌共用同一份。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { Check, ChevronDown, ChevronUp, Pencil } from 'lucide-vue-next'

import UiTooltip from '@/components/ui/UiTooltip.vue'
import { cn } from '@/lib/utils'
import { AVATAR_OPTIONS, useProfileStore } from '@/stores/profile'

const { t } = useI18n()
const profileStore = useProfileStore()

const rootRef = ref<HTMLElement | null>(null)
const nameInputRef = ref<HTMLInputElement | null>(null)

const open = ref(false)
const editingName = ref(false)
const nameDraft = ref('')

const greetingText = computed(() => t('home.greetingWithName', { name: profileStore.displayName }))

function toggle() {
  open.value = !open.value
  if (!open.value) editingName.value = false
}

function startEditName() {
  editingName.value = true
  nameDraft.value = profileStore.profile.name
  nextTick(() => {
    nameInputRef.value?.focus()
    nameInputRef.value?.select()
  })
}

function commitName() {
  const next = nameDraft.value.trim()
  if (next) profileStore.profile.name = next.slice(0, 20)
  editingName.value = false
}

function pickAvatar(src: string) {
  profileStore.profile.avatar = src
}

function onDocMouseDown(e: MouseEvent) {
  if (rootRef.value && !rootRef.value.contains(e.target as Node)) {
    open.value = false
    editingName.value = false
  }
}

onMounted(() => document.addEventListener('mousedown', onDocMouseDown))
onBeforeUnmount(() => document.removeEventListener('mousedown', onDocMouseDown))
</script>

<template>
  <div ref="rootRef" class="relative w-auto py-1 pl-4 pr-2 pt-3.5">
    <!-- 收起态胶囊 -->
    <UiTooltip content="点击编辑个人资料">
      <div
        class="group flex cursor-pointer items-center gap-2.5 rounded-full border border-border/50 px-2.5 py-1.5 text-muted-foreground/70 transition-all duration-200 hover:bg-muted/60 hover:text-foreground active:scale-[0.97]"
        @click="toggle"
      >
        <div class="relative shrink-0">
          <div
            class="size-8 overflow-hidden rounded-full ring-[1.5px] ring-border/30 transition-shadow group-hover:ring-violet-400/60"
          >
            <img :src="profileStore.profile.avatar" alt="" class="size-full object-cover" />
          </div>
          <span
            class="absolute -right-0.5 -bottom-0.5 flex size-3.5 items-center justify-center rounded-full bg-white shadow-sm dark:bg-slate-700"
          >
            <Pencil class="size-[7px] text-muted-foreground" />
          </span>
        </div>
        <span class="flex items-center gap-1 text-[13px] font-semibold text-foreground/85">
          {{ greetingText }}
          <ChevronDown class="size-3 text-muted-foreground/30" />
        </span>
      </div>
    </UiTooltip>

    <!-- 展开面板 -->
    <Transition
      enter-active-class="transition duration-200 ease-out"
      enter-from-class="-translate-y-1 scale-[0.97] opacity-0"
      leave-active-class="transition duration-150 ease-in"
      leave-to-class="-translate-y-1 scale-[0.97] opacity-0"
    >
      <div v-if="open" class="absolute left-4 top-3.5 z-50 w-64">
        <div
          class="rounded-2xl bg-white/95 px-2.5 py-2 ring-1 ring-black/[0.04] shadow-[0_1px_8px_-2px_rgba(0,0,0,0.06)] backdrop-blur-sm dark:bg-slate-800/95"
        >
          <!-- 头像 + 昵称行 -->
          <div class="flex items-center gap-2">
            <div class="size-8 shrink-0 overflow-hidden rounded-full ring-[1.5px] ring-violet-300/70">
              <img :src="profileStore.profile.avatar" alt="" class="size-full object-cover" />
            </div>

            <input
              v-if="editingName"
              ref="nameInputRef"
              v-model="nameDraft"
              type="text"
              maxlength="20"
              :placeholder="t('home.namePlaceholder')"
              class="h-6 min-w-0 flex-1 border-b border-border/80 bg-transparent text-[13px] font-semibold text-foreground outline-none placeholder:text-muted-foreground/40"
              @keydown.enter.prevent="commitName"
              @blur="commitName"
            />
            <button
              v-else
              type="button"
              class="group/name flex min-w-0 flex-1 items-center gap-1.5 text-left"
              @click="startEditName"
            >
              <span class="truncate text-[13px] font-semibold text-foreground">{{ profileStore.displayName }}</span>
              <Pencil class="size-3 shrink-0 text-muted-foreground/40 group-hover/name:text-violet-500" />
            </button>

            <button
              v-if="editingName"
              type="button"
              class="flex size-5 shrink-0 items-center justify-center rounded text-violet-500 hover:bg-violet-100"
              @click="commitName"
            >
              <Check class="size-3.5" />
            </button>
            <button
              v-else
              type="button"
              class="flex size-5 shrink-0 items-center justify-center rounded text-muted-foreground/50 hover:bg-muted"
              @click="open = false"
            >
              <ChevronUp class="size-3.5" />
            </button>
          </div>

          <!-- 头像选择器 -->
          <div class="mt-2.5 flex items-center gap-1.5 px-0.5">
            <button
              v-for="src in AVATAR_OPTIONS"
              :key="src"
              type="button"
              :class="
                cn(
                  'size-7 overflow-hidden rounded-full bg-gray-50 transition-transform hover:scale-110 active:scale-95 dark:bg-gray-800',
                  profileStore.profile.avatar === src && 'ring-2 ring-violet-400',
                )
              "
              @click="pickAvatar(src)"
            >
              <img :src="src" alt="" class="size-full object-cover" />
            </button>
          </div>

          <!-- 个人简介 -->
          <textarea
            v-model="profileStore.profile.bio"
            rows="2"
            maxlength="200"
            :placeholder="t('home.bioPlaceholder')"
            class="mt-2.5 min-h-[72px] w-full resize-none rounded-lg border border-border/40 bg-transparent px-2.5 py-2 !text-[13px] !leading-relaxed outline-none placeholder:!text-[11px] focus-visible:ring-1 focus-visible:ring-border/60"
          />
        </div>
      </div>
    </Transition>
  </div>
</template>
