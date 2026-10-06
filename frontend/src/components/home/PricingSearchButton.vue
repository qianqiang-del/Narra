<script setup lang="ts">
import { Search } from 'lucide-vue-next'
defineProps<{ loading: boolean }>()
defineEmits<{ search: [] }>()
</script>

<template>
  <button type="button" :disabled="loading" :aria-busy="loading" class="inline-flex shrink-0 items-center gap-2 rounded-md border px-3 py-2 text-xs transition-colors" :class="loading ? 'border-brand-300 bg-brand-50 text-brand-800 dark:bg-brand-950 dark:text-brand-200' : 'border-border text-muted-foreground hover:bg-muted'" @click="$emit('search')">
    <Search class="size-4 shrink-0" :class="{ 'price-search-motion': loading }" aria-hidden="true" />
    <span role="status" aria-live="polite">{{ loading ? '正在查找价格…' : '联网查价' }}</span>
  </button>
</template>

<style scoped>
.price-search-motion { animation: search-sweep 1.4s ease-in-out infinite; }
@keyframes search-sweep {
  0%, 100% { transform: translate(-2px, 1px) rotate(-12deg); }
  50% { transform: translate(2px, -1px) rotate(12deg); }
}
@media (prefers-reduced-motion: reduce) { .price-search-motion { animation: none; } }
</style>
