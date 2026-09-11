<script setup lang="ts">
/**
 * GreetingBar —— 文档 §5.4（Composer 左上：头像 + 昵称 + 个人简介）。
 *
 * 收起态是一枚胶囊；点击后展开面板，可编辑昵称、挑头像（内置 7 个 + 自传）、写简介。
 * 用户资料通过 localStorage 持久化（键 narra-profile），跨会话保留。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Check, ChevronDown, ChevronUp, ImagePlus, Pencil } from 'lucide-vue-next'

import UiTooltip from '@/components/ui/UiTooltip.vue'
import { cn } from '@/lib/utils'

const { t } = useI18n()

const AVATAR_OPTIONS = [
  'user',
  'teacher-2',
  'assist-2',
  'clown-2',
  'curious-2',
  'note-taker-2',
  'thinker-2',
].map((name) => `/avatars/${name}.png`)

const DEFAULT_NAME = '同学'
const MAX_UPLOAD_BYTES = 5 * 1024 * 1024
const STORAGE_KEY = 'narra-profile'

const rootRef = ref<HTMLElement | null>(null)
const fileInputRef = ref<HTMLInputElement | null>(null)
const nameInputRef = ref<HTMLInputElement | null>(null)

const open = ref(false)
const editingName = ref(false)
const nameDraft = ref('')

const profile = ref({
  name: DEFAULT_NAME,
  avatar: AVATAR_OPTIONS[0],
  bio: '',
})

/** 载入本地资料（首帧同步，避免闪烁） */
function loadProfile() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return
    const parsed = JSON.parse(raw) as Partial<typeof profile.value>
    if (typeof parsed.name === 'string' && parsed.name.trim()) profile.value.name = parsed.name
    if (typeof parsed.avatar === 'string' && parsed.avatar) profile.value.avatar = parsed.avatar
    if (typeof parsed.bio === 'string') profile.value.bio = parsed.bio
  } catch {
    /* 损坏的存档直接忽略 */
  }
}
loadProfile()

watch(
  profile,
  (value) => {
    try {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(value))
    } catch {
      /* 隐私模式下写入失败，忽略 */
    }
  },
  { deep: true },
)

const displayName = computed(() => profile.value.name.trim() || DEFAULT_NAME)
const greetingText = computed(() => t('home.greetingWithName', { name: displayName.value }))

function toggle() {
  open.value = !open.value
  if (!open.value) editingName.value = false
}

function startEditName() {
  editingName.value = true
  nameDraft.value = profile.value.name
  nextTick(() => {
    nameInputRef.value?.focus()
    nameInputRef.value?.select()
  })
}

function commitName() {
  const next = nameDraft.value.trim()
  if (next) profile.value.name = next.slice(0, 20)
  editingName.value = false
}

function pickAvatar(src: string) {
  profile.value.avatar = src
}

/** 选图 → canvas 压到 128×128 → data:image/jpeg（质量 0.85） */
function onFileChange(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (file.size > MAX_UPLOAD_BYTES) {
    window.alert('图片不能超过 5MB')
    return
  }

  const reader = new FileReader()
  reader.onload = () => {
    const img = new Image()
    img.onload = () => {
      const canvas = document.createElement('canvas')
      canvas.width = 128
      canvas.height = 128
      const ctx = canvas.getContext('2d')
      if (!ctx) return
      ctx.drawImage(img, 0, 0, 128, 128)
      profile.value.avatar = canvas.toDataURL('image/jpeg', 0.85)
    }
    img.src = reader.result as string
  }
  reader.readAsDataURL(file)
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
    <input
      ref="fileInputRef"
      type="file"
      accept="image/*"
      class="hidden"
      @change="onFileChange"
    />

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
            <img :src="profile.avatar" alt="" class="size-full object-cover" />
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
              <img :src="profile.avatar" alt="" class="size-full object-cover" />
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
              <span class="truncate text-[13px] font-semibold text-foreground">{{ displayName }}</span>
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
                  profile.avatar === src && 'ring-2 ring-violet-400',
                )
              "
              @click="pickAvatar(src)"
            >
              <img :src="src" alt="" class="size-full object-cover" />
            </button>

            <button
              type="button"
              class="flex size-7 items-center justify-center rounded-full border border-dashed border-border text-muted-foreground/60 transition-colors hover:border-violet-400 hover:text-violet-500"
              @click="fileInputRef?.click()"
            >
              <ImagePlus class="size-3" />
            </button>
          </div>

          <!-- 个人简介 -->
          <textarea
            v-model="profile.bio"
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
