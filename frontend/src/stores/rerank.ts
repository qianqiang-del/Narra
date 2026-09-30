import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  createRerankModel, deleteRerankModel, fetchRerankModels,
  setRerankModelEnabled, testRerankModel, updateRerankModel,
  type RerankModel, type RerankModelInput,
} from '@/api/rerank'

export const useRerankStore = defineStore('rerank', () => {
  const models = ref<RerankModel[]>([])
  const loading = ref(false)

  async function loadModels() {
    loading.value = true
    try { models.value = await fetchRerankModels() } finally { loading.value = false }
  }

  async function save(input: RerankModelInput, id?: number) {
    if (id) await updateRerankModel(id, input)
    else await createRerankModel(input)
    await loadModels()
  }

  async function remove(id: number) {
    await deleteRerankModel(id)
    await loadModels()
  }

  async function test(id: number) {
    const result = await testRerankModel(id)
    await loadModels()
    return result
  }

  async function setEnabled(id: number, enabled: boolean) {
    await setRerankModelEnabled(id, enabled)
    await loadModels()
  }

  return { models, loading, loadModels, save, remove, test, setEnabled }
})
