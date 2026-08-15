import type { PricingGroupPrice, PricingItem, PricingModel } from '@/api/pricing'

export interface PricingRow {
  model: PricingModel
  price: PricingGroupPrice
}

const summaryPriority = [
  'input',
  'output',
  'cache_read',
  'cache_write',
  'cache_write_5m',
  'cache_write_1h',
  'request',
]

export function pricingItemBaseKey(item: PricingItem): string {
  return item.key.split(':')[0]
}

export function pricingItemQualifier(item: PricingItem, locale: string): string {
  const parts: string[] = []
  if (item.tier_label) parts.push(item.tier_label)

  const min = item.min_context_tokens ?? 0
  const max = item.max_context_tokens
  const intervalBoundary = item.key.includes(':interval:') || item.key.startsWith('request:')
  const format = (value: number) => new Intl.NumberFormat(locale).format(value)

  if (max !== null && max !== undefined) {
    parts.push(min > 0
      ? `${intervalBoundary ? '>' : '≥'}${format(min)} – ≤${format(max)} tokens`
      : `≤${format(max)} tokens`)
  } else if (min > 0) {
    parts.push(`${intervalBoundary ? '>' : '≥'}${format(min)} tokens`)
  }

  return parts.join(' · ')
}

export function representativePricingItems(items: PricingItem[], limit = 3): PricingItem[] {
  return [...items]
    .sort((left, right) => {
      const leftIndex = summaryPriority.indexOf(pricingItemBaseKey(left))
      const rightIndex = summaryPriority.indexOf(pricingItemBaseKey(right))
      const normalizedLeft = (leftIndex === -1 ? summaryPriority.length : leftIndex) + (left.key.includes(':') ? 100 : 0)
      const normalizedRight = (rightIndex === -1 ? summaryPriority.length : rightIndex) + (right.key.includes(':') ? 100 : 0)
      return normalizedLeft - normalizedRight
    })
    .slice(0, limit)
}

export function bestSavingsPercent(items: PricingItem[]): number | null {
  const values = items
    .map((item) => item.savings_percent)
    .filter((value): value is string => value !== null && value !== undefined && value !== '')
    .map(Number)
    .filter(Number.isFinite)
  return values.length > 0 ? Math.max(...values) : null
}

export function pricingCurrencySymbol(currency?: string): string {
  return currency === 'CNY' ? '¥' : '$'
}

export function pricingProviderInitial(provider: string): string {
  return ({
    openai: 'O',
    anthropic: 'C',
    gemini: 'G',
    grok: 'X',
    cn: 'CN',
  } as Record<string, string>)[provider] || 'AI'
}

export function pricingProviderIconClass(provider: string): string {
  return ({
    openai: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300',
    anthropic: 'bg-orange-100 text-orange-700 dark:bg-orange-900/30 dark:text-orange-300',
    gemini: 'bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300',
    grok: 'bg-gray-900 text-white dark:bg-gray-100 dark:text-gray-900',
    cn: 'bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300',
  } as Record<string, string>)[provider] || 'bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-dark-200'
}
