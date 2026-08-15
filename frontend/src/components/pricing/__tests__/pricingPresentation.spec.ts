import { describe, expect, it } from 'vitest'
import type { PricingItem } from '@/api/pricing'
import {
  bestSavingsPercent,
  pricingCurrencySymbol,
  pricingItemBaseKey,
  pricingItemQualifier,
  pricingProviderInitial,
  representativePricingItems,
} from '../pricingPresentation'

function item(key: string, savingsPercent: string | null = null): PricingItem {
  return {
    key,
    unit: '1M_tokens',
    official_price: '1',
    official_cny_price: '6.8',
    system_base_price: '1',
    system_base_currency: 'USD',
    effective_multiplier: '0.1',
    multiplier_source: 'default',
    actual_cny_price: '0.1',
    savings_cny: savingsPercent ? '6.7' : null,
    savings_percent: savingsPercent,
    comparison_status: 'exact',
  }
}

describe('pricingPresentation', () => {
  it('prefers standard input, output, and cache prices over long-context rows', () => {
    const items = [
      item('input'),
      item('input:long_context'),
      item('output'),
      item('cache_read'),
      item('cache_write'),
    ]

    expect(representativePricingItems(items).map((entry) => entry.key)).toEqual([
      'input',
      'output',
      'cache_read',
    ])
  })

  it('uses the API-provided savings values without recalculating prices', () => {
    expect(bestSavingsPercent([item('input', '98.53'), item('output', '97.1')])).toBe(98.53)
    expect(bestSavingsPercent([item('input')])).toBeNull()
  })

  it('normalizes item keys and presentation-only currency/provider labels', () => {
    expect(pricingItemBaseKey(item('input:long_context'))).toBe('input')
    expect(pricingCurrencySymbol('CNY')).toBe('¥')
    expect(pricingCurrencySymbol('USD')).toBe('$')
    expect(pricingProviderInitial('anthropic')).toBe('C')
    expect(pricingProviderInitial('unknown')).toBe('AI')
  })

  it('renders bounded and long-context token ranges with settlement boundaries', () => {
    expect(pricingItemQualifier({
      ...item('input:interval:0-128000'),
      min_context_tokens: 0,
      max_context_tokens: 128000,
    }, 'en-US')).toBe('≤128,000 tokens')
    expect(pricingItemQualifier({
      ...item('input:interval:128000-inf'),
      min_context_tokens: 128000,
      max_context_tokens: null,
    }, 'en-US')).toBe('>128,000 tokens')
    expect(pricingItemQualifier({
      ...item('input:long_context'),
      min_context_tokens: 128001,
      max_context_tokens: null,
    }, 'en-US')).toBe('≥128,001 tokens')
  })
})
