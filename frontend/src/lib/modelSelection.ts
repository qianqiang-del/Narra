import type { AvailableLlmModel } from '@/api/llm'

export interface ModelSelection {
  providerId: number | null
  modelId: string
}

export function isSelectedModel(model: AvailableLlmModel, selection: ModelSelection): boolean {
  return model.providerId === selection.providerId && model.modelId === selection.modelId
}

export function uniqueModels(models: AvailableLlmModel[]): AvailableLlmModel[] {
  const seen = new Set<string>()
  return models.filter((model) => {
    const key = JSON.stringify([model.providerId, model.modelId])
    if (seen.has(key)) return false
    seen.add(key)
    return true
  })
}
