import { request } from './client'

/**
 * 视觉模型（VLM）配置的接口封装。
 *
 * 契约与「重排模型」同形（/settings/rerank/models）：每个配置对应一个视觉模型，
 * 同一时间只能有一条启用（由后端保证）。启用后，文档收录会在本地 OCR 之外
 * 额外生成图片描述；停用只是退回纯 OCR，不需要重建任何存量数据。
 */
interface VLMModelDTO {
  id: number
  name: string
  base_url: string
  timeout: string
  model: string
  api_key_configured: boolean
  test_status: 'untested' | 'success' | 'failed'
  last_test_error: string | null
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface VLMModel {
  id: number
  name: string
  baseUrl: string
  timeout: string
  model: string
  apiKeyConfigured: boolean
  testStatus: 'untested' | 'success' | 'failed'
  lastTestError: string | null
  enabled: boolean
}

export interface VLMModelInput {
  name: string
  baseUrl: string
  apiKey: string
  clearApiKey: boolean
  timeout: string
  model: string
}

export interface VLMTestResult {
  success: boolean
  message: string
}

function toModel(d: VLMModelDTO): VLMModel {
  return {
    id: d.id, name: d.name, baseUrl: d.base_url, timeout: d.timeout, model: d.model,
    apiKeyConfigured: d.api_key_configured, testStatus: d.test_status,
    lastTestError: d.last_test_error, enabled: d.enabled,
  }
}

function modelBody(input: VLMModelInput) {
  return {
    name: input.name, base_url: input.baseUrl, api_key: input.apiKey,
    clear_api_key: input.clearApiKey, timeout: input.timeout, model: input.model,
  }
}

export async function fetchVLMModels(): Promise<VLMModel[]> {
  return (await request<VLMModelDTO[]>('/settings/vlm/models')).map(toModel)
}

export async function createVLMModel(input: VLMModelInput): Promise<VLMModel> {
  return toModel(await request<VLMModelDTO>('/settings/vlm/models', {
    method: 'POST', body: JSON.stringify(modelBody(input)),
  }))
}

export async function updateVLMModel(id: number, input: VLMModelInput): Promise<VLMModel> {
  return toModel(await request<VLMModelDTO>(`/settings/vlm/models/${id}`, {
    method: 'PUT', body: JSON.stringify(modelBody(input)),
  }))
}

export async function deleteVLMModel(id: number): Promise<void> {
  await request<void>(`/settings/vlm/models/${id}`, { method: 'DELETE' })
}

/** 测试连接。后端会发一张 64x64 图片并等待模型回复，成功才算可用。 */
export async function testVLMModel(id: number): Promise<VLMTestResult> {
  return request<VLMTestResult>(`/settings/vlm/models/${id}/test`, { method: 'POST' })
}

/**
 * 用表单里还没保存的值测试连接（不落库）。
 * 编辑既有配置时传 id：API Key 留空会沿用已保存的那把。
 */
export async function probeVLMModel(input: VLMModelInput, id?: number): Promise<VLMTestResult> {
  return request<VLMTestResult>('/settings/vlm/test', {
    method: 'POST',
    body: JSON.stringify({ id: id ?? 0, ...modelBody(input) }),
  })
}

export async function setVLMModelEnabled(id: number, enabled: boolean): Promise<VLMModel> {
  return toModel(await request<VLMModelDTO>(`/settings/vlm/models/${id}/enabled`, {
    method: 'PATCH', body: JSON.stringify({ enabled }),
  }))
}
