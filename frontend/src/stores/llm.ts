import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  createLlmProvider, deleteLlmProvider, fetchAvailableLlmModels, fetchLlmProviders,
  setLlmProviderEnabled, testLlmProvider, updateLlmProvider,
  type AvailableLlmModel, type LlmProvider, type LlmProviderInput,
} from '@/api/llm'

export const useLlmStore = defineStore('llm', () => {
  const providers = ref<LlmProvider[]>([])
  const availableModels = ref<AvailableLlmModel[]>([])
  const loading = ref(false)

  async function loadProviders() {
    loading.value = true
    try { providers.value = await fetchLlmProviders() } finally { loading.value = false }
  }

  async function loadAvailableModels() {
    availableModels.value = await fetchAvailableLlmModels()
  }

  async function refreshAll() {
    await Promise.all([loadProviders(), loadAvailableModels()])
  }

  async function save(input: LlmProviderInput, id?: number) {
    if (id) await updateLlmProvider(id, input)
    else await createLlmProvider(input)
    await refreshAll()
  }

  async function remove(id: number) {
    await deleteLlmProvider(id)
    await refreshAll()
  }

  async function test(id: number) {
    const result = await testLlmProvider(id)
    await refreshAll()
    return result
  }

  async function setEnabled(id: number, enabled: boolean) {
    await setLlmProviderEnabled(id, enabled)
    await refreshAll()
  }

  return { providers, availableModels, loading, loadProviders, loadAvailableModels, refreshAll, save, remove, test, setEnabled }
})
