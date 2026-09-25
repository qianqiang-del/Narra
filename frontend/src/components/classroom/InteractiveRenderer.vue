<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import type { Scene } from '@/types/scene'
const props = defineProps<{ scene: Scene }>()
const values = reactive<Record<string, unknown>>({})
const selected = ref<Record<string, string>>({})
const blocks = computed(() => props.scene.blocks ?? [])
function initial(control: Record<string, unknown>) { const options = Array.isArray(control.options) ? control.options : []; return control.default ?? options[0] ?? '' }
function value(control: Record<string, unknown>) { const key = String(control.name); if (!(key in values)) values[key] = initial(control); return values[key] }
function setValue(key: string, next: unknown) { values[key] = next }
</script>
<template>
  <div class="flex size-full flex-col overflow-y-auto bg-gradient-to-br from-slate-50 via-white to-emerald-50 p-6 dark:from-slate-950 dark:via-gray-900 dark:to-emerald-950/30">
    <header class="mb-5 border-b border-gray-200/70 pb-4 dark:border-gray-700"><span class="text-[11px] font-bold tracking-widest text-emerald-600 uppercase">Interactive lesson</span><h2 class="mt-1 text-2xl font-bold text-gray-900 dark:text-gray-100">{{ scene.title }}</h2></header>
    <div class="mx-auto flex w-full max-w-3xl flex-col gap-3">
      <template v-for="block in blocks" :key="block.key">
        <h3 v-if="block.type === 'heading'" class="mt-2 text-lg font-semibold text-gray-900 dark:text-gray-100">{{ block.content }}</h3>
        <p v-else-if="block.type === 'paragraph'" class="text-sm leading-7 text-gray-600 dark:text-gray-300">{{ block.content }}</p>
        <div v-else-if="block.type === 'list-item'" class="flex gap-2 rounded-xl bg-white/80 p-3 text-sm text-gray-700 shadow-sm dark:bg-gray-800/70 dark:text-gray-200"><span class="mt-2 size-1.5 shrink-0 rounded-full bg-emerald-500" />{{ block.content }}</div>
        <div v-else-if="block.type === 'callout'" class="rounded-xl border border-amber-200 bg-amber-50/80 p-4 text-sm leading-6 text-amber-900 dark:border-amber-900 dark:bg-amber-950/30 dark:text-amber-100">{{ block.content }}</div>
        <section v-else-if="block.type === 'quiz'" class="rounded-2xl border border-indigo-200 bg-white p-5 shadow-sm dark:border-indigo-900 dark:bg-gray-800/80">
          <div class="mb-3 flex items-center gap-2"><span class="rounded-md bg-indigo-100 px-2 py-1 text-[10px] font-bold text-indigo-700">测验</span><p class="text-sm font-medium">{{ block.content }}</p></div>
          <div class="grid gap-2 sm:grid-cols-2"><button v-for="option in block.interaction?.options ?? []" :key="option" type="button" :class="selected[block.key ?? ''] === option ? 'border-indigo-500 bg-indigo-50 text-indigo-700' : 'border-gray-200 hover:border-indigo-300'" class="rounded-lg border px-3 py-2 text-left text-sm transition-colors" @click="selected[block.key ?? ''] = option">{{ option }}</button></div>
          <p v-if="selected[block.key ?? '']" class="mt-3 text-xs text-indigo-600">已选择：{{ selected[block.key ?? ''] }}</p>
        </section>
        <section v-else-if="block.interaction" class="rounded-2xl border border-emerald-200 bg-white p-5 shadow-sm dark:border-emerald-900 dark:bg-gray-800/80">
          <p class="mb-4 text-sm leading-6 text-gray-600 dark:text-gray-300">{{ block.content }}</p>
          <div class="grid gap-4 sm:grid-cols-2">
            <label v-for="control in block.interaction.controls ?? []" :key="String(control.name)" class="text-xs font-medium text-gray-600 dark:text-gray-300"><span class="mb-1 block">{{ control.name }}</span>
              <select v-if="control.type === 'select'" :value="value(control)" class="w-full rounded-lg border border-gray-200 bg-white px-3 py-2 text-sm dark:border-gray-700 dark:bg-gray-900" @change="setValue(String(control.name), ($event.target as HTMLSelectElement).value)"><option v-for="option in control.options ?? []" :key="String(option)" :value="option">{{ option }}</option></select>
              <input v-else-if="control.type === 'range'" :value="value(control)" type="range" :min="control.min as number" :max="control.max as number" :step="control.step as number" class="w-full accent-emerald-600" @input="setValue(String(control.name), Number(($event.target as HTMLInputElement).value))" />
              <input v-else :value="String(value(control))" class="w-full rounded-lg border border-gray-200 px-3 py-2 text-sm dark:border-gray-700 dark:bg-gray-900" @input="setValue(String(control.name), ($event.target as HTMLInputElement).value)" />
            </label>
          </div>
        </section>
      </template>
    </div>
  </div>
</template>
