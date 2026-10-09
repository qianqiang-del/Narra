import { defineStore } from 'pinia'
import { ref } from 'vue'
import {
  createVLMModel, deleteVLMModel, fetchVLMModels, probeVLMModel,
  setVLMModelEnabled, testVLMModel, updateVLMModel,
  type VLMModel, type VLMModelInput,
} from '@/api/vlm'

export const useVlmStore = defineStore('vlm', () => {
  const models = ref<VLMModel[]>([])
  const loading = ref(false)

  async function loadModels() {
    loading.value = true
    try { models.value = await fetchVLMModels() } finally { loading.value = false }
  }

  async function save(input: VLMModelInput, id?: number) {
    if (id) await updateVLMModel(id, input)
    else await createVLMModel(input)
    await loadModels()
  }

  async function remove(id: number) {
    await deleteVLMModel(id)
    await loadModels()
  }

  async function test(id: number) {
    const result = await testVLMModel(id)
    await loadModels()
    return result
  }

  /** 表单保存前的试跑：用输入值探测，不落库、不改列表。 */
  async function probe(input: VLMModelInput, id?: number) {
    return probeVLMModel(input, id)
  }

  async function setEnabled(id: number, enabled: boolean) {
    await setVLMModelEnabled(id, enabled)
    await loadModels()
  }

  return { models, loading, loadModels, save, remove, test, probe, setEnabled }
})
