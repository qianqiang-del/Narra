import type { ModelPricing } from '@/api/llm'

export function copyModelPricing(pricing?: Record<string, ModelPricing>): Record<string, ModelPricing> {
  return Object.fromEntries(Object.entries(pricing ?? {})
    .filter(([, price]) => price.source !== 'catalog')
    .map(([model, price]) => [model, { ...price }]))
}

export function shouldAutoLookupPricing(pricing?: ModelPricing): boolean {
  return !pricing || pricing.source === 'catalog' ||
    (pricing.source !== 'user' && (pricing.inputPerMillion === null || pricing.outputPerMillion === null))
}
