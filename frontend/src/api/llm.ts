import { request } from './client'

interface LlmProviderDTO {
  id: number
  name: string
  protocol: string
  base_url: string
  timeout: string
  models: string[]
  pricing?: Record<string, ModelPricingDTO>
  api_key_configured: boolean
  test_status: 'untested' | 'success' | 'failed'
  last_test_model: string | null
  last_test_error: string | null
  last_tested_at: string | null
  enabled: boolean
  created_at: string
  updated_at: string
}

interface ModelPricingDTO {
  input_per_million?: number | null
  output_per_million?: number | null
  currency?: string
  source?: string
  pricing_mode?: string
  billing_note?: string
  confirmed_at?: string | null
  source_url?: string
  checked_at?: string | null
}

export interface ModelPricing {
  inputPerMillion: number | null
  outputPerMillion: number | null
  currency: string
  source: 'catalog' | 'user' | 'unknown' | string
  pricingMode?: 'token_price' | 'multiplier' | 'unknown' | string
  billingNote?: string
  confirmedAt?: string | null
  sourceUrl?: string
  checkedAt?: string | null
}

export interface LlmPriceSuggestion {
  modelId: string
  found: boolean
  reason: string
  candidates: LlmPriceCandidate[]
  pricing: ModelPricing | null
}

export interface LlmPriceCandidate {
  modelId: string
  pricingMode: 'token_price' | 'multiplier' | 'unknown' | string
  inputPerMillion: number | null
  outputPerMillion: number | null
  currency: string
  source: string
  sourceUrl?: string
  billingNote?: string
  group?: string
  modelRatio?: number | null
  completionRatio?: number | null
  groupRatio?: number | null
  confidence?: string
}

export interface LlmProvider {
  id: number
  name: string
  protocol: string
  baseUrl: string
  timeout: string
  models: string[]
  pricing: Record<string, ModelPricing>
  apiKeyConfigured: boolean
  testStatus: 'untested' | 'success' | 'failed'
  lastTestModel: string | null
  lastTestError: string | null
  lastTestedAt: string | null
  enabled: boolean
}

export interface LlmProviderInput {
  name: string
  baseUrl: string
  apiKey: string
  clearApiKey: boolean
  timeout: string
  models: string[]
  pricing: Record<string, ModelPricing>
}

export interface AvailableLlmModel {
  providerId: number
  providerName: string
  modelId: string
}

function toProvider(d: LlmProviderDTO): LlmProvider {
  return {
    id: d.id, name: d.name, protocol: d.protocol, baseUrl: d.base_url,
    timeout: d.timeout, models: d.models, apiKeyConfigured: d.api_key_configured,
    pricing: Object.fromEntries(Object.entries(d.pricing ?? {}).map(([model, price]) => [model, toPricing(price)])),
    testStatus: d.test_status, lastTestModel: d.last_test_model,
    lastTestError: d.last_test_error, lastTestedAt: d.last_tested_at, enabled: d.enabled,
  }
}

function toPricing(price: ModelPricingDTO): ModelPricing {
  return {
    inputPerMillion: price.input_per_million ?? null,
    outputPerMillion: price.output_per_million ?? null,
    currency: price.currency || 'USD', source: price.source || 'unknown',
    pricingMode: price.pricing_mode, billingNote: price.billing_note, confirmedAt: price.confirmed_at,
    sourceUrl: price.source_url, checkedAt: price.checked_at,
  }
}

function providerBody(input: LlmProviderInput) {
  return {
    name: input.name, base_url: input.baseUrl, api_key: input.apiKey,
    clear_api_key: input.clearApiKey, timeout: input.timeout, models: input.models,
    pricing: Object.fromEntries(Object.entries(input.pricing).map(([model, price]) => [model, {
      input_per_million: price.inputPerMillion, output_per_million: price.outputPerMillion,
      currency: price.currency, source: price.source,
      pricing_mode: price.pricingMode, billing_note: price.billingNote, confirmed_at: price.confirmedAt,
      source_url: price.sourceUrl, checked_at: price.checkedAt,
    }])),
  }
}

export async function fetchLlmProviders(): Promise<LlmProvider[]> {
  return (await request<LlmProviderDTO[]>('/settings/llm/providers')).map(toProvider)
}

export async function createLlmProvider(input: LlmProviderInput): Promise<LlmProvider> {
  return toProvider(await request<LlmProviderDTO>('/settings/llm/providers', {
    method: 'POST', body: JSON.stringify(providerBody(input)),
  }))
}

export async function updateLlmProvider(id: number, input: LlmProviderInput): Promise<LlmProvider> {
  return toProvider(await request<LlmProviderDTO>(`/settings/llm/providers/${id}`, {
    method: 'PUT', body: JSON.stringify(providerBody(input)),
  }))
}

export async function deleteLlmProvider(id: number): Promise<void> {
  await request<void>(`/settings/llm/providers/${id}`, { method: 'DELETE' })
}

export interface LlmTestResult {
  success: boolean
  message: string
}

/** 测试连接。后端会逐个测配置里的每个模型，全部通过才算成功。 */
export async function testLlmProvider(id: number): Promise<LlmTestResult> {
  return request<LlmTestResult>(`/settings/llm/providers/${id}/test`, { method: 'POST' })
}

export async function suggestLlmModelPricing(id: number, modelId: string): Promise<LlmPriceSuggestion> {
  const result = await request<{ model_id: string; found: boolean; reason?: string; candidates?: LlmPriceCandidateDTO[]; pricing?: ModelPricingDTO }>(
    `/settings/llm/providers/${id}/pricing/suggestions`,
    { method: 'POST', body: JSON.stringify({ model_id: modelId }) },
  )
  return {
    modelId: result.model_id, found: result.found, reason: result.reason || '',
    candidates: (result.candidates ?? []).map(toPriceCandidate),
    pricing: result.pricing ? toPricing(result.pricing) : null,
  }
}

interface LlmPriceCandidateDTO {
  model_id: string
  pricing_mode: string
  input_per_million?: number | null
  output_per_million?: number | null
  currency?: string
  source?: string
  source_url?: string
  billing_note?: string
  group?: string
  model_ratio?: number | null
  completion_ratio?: number | null
  group_ratio?: number | null
  confidence?: string
}

function toPriceCandidate(candidate: LlmPriceCandidateDTO): LlmPriceCandidate {
  return {
    modelId: candidate.model_id, pricingMode: candidate.pricing_mode,
    inputPerMillion: candidate.input_per_million ?? null, outputPerMillion: candidate.output_per_million ?? null,
    currency: candidate.currency || '', source: candidate.source || 'search', sourceUrl: candidate.source_url,
    billingNote: candidate.billing_note, group: candidate.group,
    modelRatio: candidate.model_ratio ?? null, completionRatio: candidate.completion_ratio ?? null,
    groupRatio: candidate.group_ratio ?? null, confidence: candidate.confidence,
  }
}

export async function setLlmProviderEnabled(id: number, enabled: boolean): Promise<LlmProvider> {
  return toProvider(await request<LlmProviderDTO>(`/settings/llm/providers/${id}/enabled`, {
    method: 'PATCH', body: JSON.stringify({ enabled }),
  }))
}

interface AvailableDTO {
  provider_id: number
  provider_name: string
  model_id: string
}

export async function fetchAvailableLlmModels(): Promise<AvailableLlmModel[]> {
  const rows = await request<AvailableDTO[]>('/llm/models/available')
  return rows.map((row) => ({
    providerId: row.provider_id, providerName: row.provider_name, modelId: row.model_id,
  }))
}
