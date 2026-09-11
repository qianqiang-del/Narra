<script setup lang="ts">
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'

const router = useRouter()
const { t } = useI18n()
</script>

<template>
  <div
    class="flex min-h-[100dvh] w-full flex-col items-center justify-center gap-4 bg-background px-6 text-center"
  >
    <h1 class="text-2xl font-bold tracking-tight">{{ t('notFound.title') }}</h1>
    <p class="text-sm text-muted-foreground">{{ t('notFound.desc') }}</p>
    <button
      type="button"
      class="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90"
      @click="router.push({ name: 'home' })"
    >
      {{ t('common.backToHome') }}
    </button>
  </div>
</template>
