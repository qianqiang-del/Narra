<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowRight, Loader2, LockKeyhole, MessageSquareText, Phone } from 'lucide-vue-next'

import { loginCode, loginPassword, register, sendCode } from '@/api/auth'
import { saveSession } from '@/lib/authSession'

type Mode = 'password' | 'code' | 'register'

const route = useRoute()
const router = useRouter()
const mode = ref<Mode>('password')
const phone = ref('')
const password = ref('')
const code = ref('')
const busy = ref(false)
const sending = ref(false)
const cooldown = ref(0)
const error = ref('')
let timer: ReturnType<typeof setInterval> | undefined

function selectMode(next: Mode) {
  mode.value = next
  error.value = ''
  code.value = ''
}

async function requestCode() {
  if (sending.value || cooldown.value > 0) return
  error.value = ''
  sending.value = true
  try {
    await sendCode(phone.value, mode.value === 'register' ? 'register' : 'login')
    cooldown.value = 60
    clearInterval(timer)
    timer = setInterval(() => {
      cooldown.value -= 1
      if (cooldown.value <= 0) clearInterval(timer)
    }, 1000)
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '验证码发送失败'
  } finally {
    sending.value = false
  }
}

async function submit() {
  if (busy.value) return
  error.value = ''
  busy.value = true
  try {
    const session = mode.value === 'register'
      ? await register(phone.value, password.value, code.value)
      : mode.value === 'code'
        ? await loginCode(phone.value, code.value)
        : await loginPassword(phone.value, password.value)
    saveSession(session)
    const redirect = route.query.redirect
    await router.replace(typeof redirect === 'string' && redirect.startsWith('/') && !redirect.startsWith('//') ? redirect : '/')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : '登录失败'
  } finally {
    busy.value = false
  }
}

onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <main class="flex min-h-screen items-center justify-center bg-background px-4 py-12 text-foreground">
    <div class="w-full max-w-[390px]">
      <div class="mb-9 flex items-center justify-center">
        <img src="/logo-horizontal.svg" alt="Narra" class="h-9 w-auto max-w-[180px]" />
      </div>

      <div class="rounded-lg border border-border bg-card p-6 shadow-sm sm:p-8">
        <h1 class="text-xl font-semibold">{{ mode === 'register' ? '注册账号' : '登录 Narra' }}</h1>
        <div v-if="mode !== 'register'" class="mt-6 grid grid-cols-2 rounded-md bg-muted p-1" role="tablist" aria-label="登录方式">
          <button type="button" role="tab" :aria-selected="mode === 'password'" class="h-9 rounded text-sm font-medium transition-colors" :class="mode === 'password' ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground'" @click="selectMode('password')">密码登录</button>
          <button type="button" role="tab" :aria-selected="mode === 'code'" class="h-9 rounded text-sm font-medium transition-colors" :class="mode === 'code' ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground'" @click="selectMode('code')">验证码登录</button>
        </div>

        <form class="mt-6 space-y-4" @submit.prevent="submit">
          <label class="block text-sm font-medium">
            手机号
            <span class="mt-2 flex h-11 items-center gap-3 rounded-md border border-border px-3 focus-within:border-brand-500">
              <Phone class="size-4 shrink-0 text-muted-foreground" />
              <input v-model.trim="phone" type="tel" inputmode="tel" autocomplete="tel" required placeholder="请输入手机号" class="min-w-0 flex-1 bg-transparent text-sm outline-none" />
            </span>
          </label>

          <label v-if="mode !== 'code'" class="block text-sm font-medium">
            密码
            <span class="mt-2 flex h-11 items-center gap-3 rounded-md border border-border px-3 focus-within:border-brand-500">
              <LockKeyhole class="size-4 shrink-0 text-muted-foreground" />
              <input v-model="password" type="password" :autocomplete="mode === 'register' ? 'new-password' : 'current-password'" required :minlength="mode === 'register' ? 8 : undefined" placeholder="请输入密码" class="min-w-0 flex-1 bg-transparent text-sm outline-none" />
            </span>
          </label>

          <label v-if="mode !== 'password'" class="block text-sm font-medium">
            短信验证码
            <span class="mt-2 flex h-11 items-center gap-2 rounded-md border border-border px-3 focus-within:border-brand-500">
              <MessageSquareText class="size-4 shrink-0 text-muted-foreground" />
              <input v-model.trim="code" type="text" inputmode="numeric" autocomplete="one-time-code" required maxlength="6" pattern="[0-9]{6}" placeholder="6 位验证码" class="min-w-0 flex-1 bg-transparent text-sm outline-none" />
              <button type="button" :disabled="sending || cooldown > 0 || !phone" class="shrink-0 text-xs font-semibold text-brand-700 disabled:opacity-50 dark:text-brand-300" @click="requestCode">{{ cooldown > 0 ? `${cooldown}s` : sending ? '发送中' : '发送验证码' }}</button>
            </span>
          </label>

          <p v-if="error" role="alert" class="text-sm text-destructive">{{ error }}</p>

          <button type="submit" :disabled="busy" class="flex h-11 w-full items-center justify-center gap-2 rounded-md bg-brand-700 text-sm font-semibold text-white transition-colors hover:bg-brand-800 disabled:opacity-60">
            <Loader2 v-if="busy" class="size-4 animate-spin" />
            <template v-else><span>{{ mode === 'register' ? '注册并登录' : '登录' }}</span><ArrowRight class="size-4" /></template>
          </button>
        </form>

        <div class="mt-6 border-t border-border pt-5 text-center text-sm text-muted-foreground">
          <template v-if="mode === 'register'">已有账号？<button type="button" class="font-semibold text-brand-700 dark:text-brand-300" @click="selectMode('password')">去登录</button></template>
          <template v-else>还没有账号？<button type="button" class="font-semibold text-brand-700 dark:text-brand-300" @click="selectMode('register')">手机号注册</button></template>
        </div>
      </div>
    </div>
  </main>
</template>
