import { request } from './client'

/**
 * 重排模型配置的接口封装。
 *
 * 契约与「大模型」栏目同形（/settings/llm/providers），只是每个配置对应一个重排模型：
 * 同一时间只能有一条启用（由后端保证），切换不需要重建知识库切片。
 */
interface RerankModelDTO {
  id: number
  name: string
  base_url: string
  timeout: string
  model: string
  api_key_configured: boolean
  test_status: 'untested' | 'success' | 'failed'
  last_test_error: string | null
  last_tested_at: string | null
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface RerankModel {
  id: number
  name: string
  baseUrl: string
  timeout: string
  model: string
  apiKeyConfigured: boolean
  testStatus: 'untested' | 'success' | 'failed'
  lastTestError: string | null
  lastTestedAt: string | null
  enabled: boolean
}

export interface RerankModelInput {
  name: string
  baseUrl: string
  apiKey: string
  clearApiKey: boolean
  timeout: string
  model: string
}

export interface RerankTestResult {
  success: boolean
  message: string
}

function toModel(d: RerankModelDTO): RerankModel {
  return {
    id: d.id, name: d.name, baseUrl: d.base_url, timeout: d.timeout, model: d.model,
    apiKeyConfigured: d.api_key_configured, testStatus: d.test_status,
    lastTestError: d.last_test_error, lastTestedAt: d.last_tested_at, enabled: d.enabled,
  }
}

function modelBody(input: RerankModelInput) {
  return {
    name: input.name, base_url: input.baseUrl, api_key: input.apiKey,
    clear_api_key: input.clearApiKey, timeout: input.timeout, model: input.model,
  }
}

export async function fetchRerankModels(): Promise<RerankModel[]> {
  return (await request<RerankModelDTO[]>('/settings/rerank/models')).map(toModel)
}

export async function createRerankModel(input: RerankModelInput): Promise<RerankModel> {
  return toModel(await request<RerankModelDTO>('/settings/rerank/models', {
    method: 'POST', body: JSON.stringify(modelBody(input)),
  }))
}

export async function updateRerankModel(id: number, input: RerankModelInput): Promise<RerankModel> {
  return toModel(await request<RerankModelDTO>(`/settings/rerank/models/${id}`, {
    method: 'PUT', body: JSON.stringify(modelBody(input)),
  }))
}

export async function deleteRerankModel(id: number): Promise<void> {
  await request<void>(`/settings/rerank/models/${id}`, { method: 'DELETE' })
}

/** 测试连接。后端会发一次真实重排请求（相关内容 + 无关内容），成功才算可用。 */
export async function testRerankModel(id: number): Promise<RerankTestResult> {
  return request<RerankTestResult>(`/settings/rerank/models/${id}/test`, { method: 'POST' })
}

export async function setRerankModelEnabled(id: number, enabled: boolean): Promise<RerankModel> {
  return toModel(await request<RerankModelDTO>(`/settings/rerank/models/${id}/enabled`, {
    method: 'PATCH', body: JSON.stringify({ enabled }),
  }))
}
