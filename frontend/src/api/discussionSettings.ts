import { request } from './client'
import type { ModelSelection } from '@/lib/modelSelection'

export interface DiscussionSettings extends ModelSelection {
  providerName: string
  source: 'generation' | 'classroom'
  available: boolean
}
interface SettingsDTO {
  llm_provider_id: number
  llm_model_id: string
  provider_name: string
  source: 'generation' | 'classroom'
  available: boolean
}
function toSettings(item: SettingsDTO): DiscussionSettings {
  return { providerId: item.llm_provider_id || null, modelId: item.llm_model_id, providerName: item.provider_name, source: item.source, available: item.available }
}
export function fetchDiscussionSettings(classroomId: number): Promise<DiscussionSettings> {
  return request<SettingsDTO>(`/classrooms/${classroomId}/discussion-settings`).then(toSettings)
}
export function updateDiscussionSettings(classroomId: number, selection: ModelSelection): Promise<DiscussionSettings> {
  return request<SettingsDTO>(`/classrooms/${classroomId}/discussion-settings`, {
    method: 'PATCH', body: JSON.stringify({ llm_provider_id: selection.providerId, llm_model_id: selection.modelId }),
  }).then(toSettings)
}
