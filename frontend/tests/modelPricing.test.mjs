import test from 'node:test'
import assert from 'node:assert/strict'
import { reactive } from 'vue'
import { copyModelPricing, shouldAutoLookupPricing } from '../src/lib/modelPricing.ts'

test('editing a stored provider copies reactive pricing without changing the original', () => {
  const storedPricing = reactive({
    'custom-model': { inputPerMillion: 1.25, outputPerMillion: 3.5, currency: 'USD', source: 'user' },
  })

  const formPricing = copyModelPricing(storedPricing)
  assert.deepEqual(formPricing, {
    'custom-model': { inputPerMillion: 1.25, outputPerMillion: 3.5, currency: 'USD', source: 'user' },
  })
  formPricing['custom-model'].inputPerMillion = 9
  assert.equal(storedPricing['custom-model'].inputPerMillion, 1.25)
})

test('editing a provider without stored pricing starts with an empty price form', () => {
  assert.deepEqual(copyModelPricing(undefined), {})
})

test('old catalog prices are excluded from the editable form', () => {
  assert.deepEqual(copyModelPricing({
    old: { inputPerMillion: 2, outputPerMillion: 8, currency: 'CNY', source: 'catalog' },
  }), {})
})

test('automatic lookup only runs for missing prices, never over a manual price', () => {
  assert.equal(shouldAutoLookupPricing(undefined), true)
  assert.equal(shouldAutoLookupPricing({ inputPerMillion: 1, outputPerMillion: 2, currency: 'USD', source: 'user' }), false)
  assert.equal(shouldAutoLookupPricing({ inputPerMillion: 1, outputPerMillion: 2, currency: 'USD', source: 'search' }), false)
  assert.equal(shouldAutoLookupPricing({ inputPerMillion: null, outputPerMillion: 2, currency: 'USD', source: 'search' }), true)
})
