<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

const props = defineProps<{ src: string; alt: string }>()
const { t } = useI18n()
const failed = ref(false)

watch(() => props.src, () => { failed.value = false })
</script>

<template>
  <a
    v-if="failed"
    :href="src"
    target="_blank"
    rel="noopener noreferrer"
    class="text-primary underline underline-offset-2 hover:text-primary/80"
  >{{ t('knowledge.preview.imageUnavailable') }}<template v-if="alt"> ({{ alt }})</template></a>
  <img
    v-else
    :src="src"
    :alt="alt"
    loading="lazy"
    decoding="async"
    referrerpolicy="no-referrer"
    class="my-2 h-auto max-w-full rounded-md"
    @error="failed = true"
  />
</template>
