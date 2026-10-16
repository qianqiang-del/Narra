<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowRight, Loader2, LockKeyhole, MessageSquareText, Phone } from 'lucide-vue-next'

import { loginCode, loginPassword, register, sendCode } from '@/api/auth'
import { saveSession } from '@/lib/authSession'
import { useRolesStore } from '@/stores/roles'

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
const fieldErrors = ref({ phone: '', password: '', code: '' })
const phoneInput = ref<HTMLInputElement | null>(null)
const passwordInput = ref<HTMLInputElement | null>(null)
const codeInput = ref<HTMLInputElement | null>(null)
let timer: ReturnType<typeof setInterval> | undefined

function validatePhone(): string {
  if (!phone.value) return '请输入手机号'
  if (!/^(?:\+86)?1[3-9]\d{9}$/.test(phone.value)) return '请输入正确的手机号'
  return ''
}

function validateForm(): boolean {
  fieldErrors.value = {
    phone: validatePhone(),
    password: mode.value === 'code' ? '' : !password.value ? '请输入密码'
      : mode.value === 'register' && (password.value.length < 8 || password.value.length > 72)
        ? '密码需为 8 到 72 个字符' : '',
    code: mode.value === 'password' ? '' : !code.value ? '请输入短信验证码'
      : !/^\d{6}$/.test(code.value) ? '请输入 6 位数字验证码' : '',
  }
  if (fieldErrors.value.phone) phoneInput.value?.focus()
  else if (fieldErrors.value.password) passwordInput.value?.focus()
  else if (fieldErrors.value.code) codeInput.value?.focus()
  return !Object.values(fieldErrors.value).some(Boolean)
}

function selectMode(next: Mode) {
  mode.value = next
  error.value = ''
  fieldErrors.value = { phone: '', password: '', code: '' }
  code.value = ''
}

async function requestCode() {
  if (sending.value || cooldown.value > 0) return
  error.value = ''
  fieldErrors.value.phone = validatePhone()
  if (fieldErrors.value.phone) {
    phoneInput.value?.focus()
    return
  }
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
  if (!validateForm()) return
  busy.value = true
  try {
    const session = mode.value === 'register'
      ? await register(phone.value, password.value, code.value)
      : mode.value === 'code'
        ? await loginCode(phone.value, code.value)
        : await loginPassword(phone.value, password.value)
    saveSession(session)
    useRolesStore().reset()
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
        <img src="/logo-horizontal.svg" alt="Narra" class="h-12 w-[208px] md:h-16 md:w-[277px]" />
      </div>

      <div class="rounded-lg border border-border bg-card p-6 shadow-sm sm:p-8">
        <h1 class="text-xl font-semibold">{{ mode === 'register' ? '注册账号' : '登录 Narra' }}</h1>
        <div v-if="mode !== 'register'" class="mt-6 grid grid-cols-2 rounded-md bg-muted p-1" role="tablist" aria-label="登录方式">
          <button type="button" role="tab" :aria-selected="mode === 'password'" class="h-9 rounded text-sm font-medium transition-colors" :class="mode === 'password' ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground'" @click="selectMode('password')">密码登录</button>
          <button type="button" role="tab" :aria-selected="mode === 'code'" class="h-9 rounded text-sm font-medium transition-colors" :class="mode === 'code' ? 'bg-card text-foreground shadow-sm' : 'text-muted-foreground'" @click="selectMode('code')">验证码登录</button>
        </div>

        <form class="mt-6 space-y-4" novalidate @submit.prevent="submit">
          <label class="block text-sm font-medium">
            手机号
            <span class="login-field mt-2 flex h-11 items-center gap-3 rounded-md border px-3" :class="fieldErrors.phone ? 'border-destructive focus-within:border-destructive' : 'border-border focus-within:border-brand-500'">
              <Phone class="size-4 shrink-0 text-muted-foreground" />
              <input ref="phoneInput" v-model.trim="phone" type="tel" inputmode="tel" autocomplete="tel" required placeholder="请输入手机号" :aria-invalid="!!fieldErrors.phone" :aria-describedby="fieldErrors.phone ? 'login-phone-error' : undefined" class="min-w-0 flex-1 bg-transparent text-sm outline-none" @input="fieldErrors.phone = ''" />
            </span>
            <span v-if="fieldErrors.phone" id="login-phone-error" class="mt-1 block text-xs font-normal text-destructive">{{ fieldErrors.phone }}</span>
          </label>

          <label v-if="mode !== 'code'" class="block text-sm font-medium">
            密码
            <span class="login-field mt-2 flex h-11 items-center gap-3 rounded-md border px-3" :class="fieldErrors.password ? 'border-destructive focus-within:border-destructive' : 'border-border focus-within:border-brand-500'">
              <LockKeyhole class="size-4 shrink-0 text-muted-foreground" />
              <input ref="passwordInput" v-model="password" type="password" :autocomplete="mode === 'register' ? 'new-password' : 'current-password'" required :minlength="mode === 'register' ? 8 : undefined" placeholder="请输入密码" :aria-invalid="!!fieldErrors.password" :aria-describedby="fieldErrors.password ? 'login-password-error' : undefined" class="min-w-0 flex-1 bg-transparent text-sm outline-none" @input="fieldErrors.password = ''" />
            </span>
            <span v-if="fieldErrors.password" id="login-password-error" class="mt-1 block text-xs font-normal text-destructive">{{ fieldErrors.password }}</span>
          </label>

          <label v-if="mode !== 'password'" class="block text-sm font-medium">
            短信验证码
            <span class="login-field mt-2 flex h-11 items-center gap-2 rounded-md border px-3" :class="fieldErrors.code ? 'border-destructive focus-within:border-destructive' : 'border-border focus-within:border-brand-500'">
              <MessageSquareText class="size-4 shrink-0 text-muted-foreground" />
              <input ref="codeInput" v-model.trim="code" type="text" inputmode="numeric" autocomplete="one-time-code" required maxlength="6" pattern="[0-9]{6}" placeholder="6 位验证码" :aria-invalid="!!fieldErrors.code" :aria-describedby="fieldErrors.code ? 'login-code-error' : undefined" class="min-w-0 flex-1 bg-transparent text-sm outline-none" @input="fieldErrors.code = ''" />
              <button type="button" :disabled="sending || cooldown > 0" class="shrink-0 text-xs font-semibold text-brand-700 disabled:opacity-50 dark:text-brand-300" @click="requestCode">{{ cooldown > 0 ? `${cooldown}s` : sending ? '发送中' : '发送验证码' }}</button>
            </span>
            <span v-if="fieldErrors.code" id="login-code-error" class="mt-1 block text-xs font-normal text-destructive">{{ fieldErrors.code }}</span>
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

<style scoped>
.login-field input:focus-visible {
  outline: none;
  box-shadow: none;
}
</style>
