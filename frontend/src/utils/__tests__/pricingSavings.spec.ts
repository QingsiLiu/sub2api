import { describe, expect, it } from 'vitest'
import { currencySymbol, formatScaled, savingsPercent } from '../pricing'

const rates = { usdCnyRate: 6.8, quotaUsdPerCny: 1 }

describe('savingsPercent', () => {
  it('converts USD official to CNY and compares with USD-quota paid', () => {
    // official $10 → ¥68; paid $1.5 at ¥1=$1 → ¥1.5 ; saved (68-1.5)/68
    const pct = savingsPercent({ paid: 1.5, official: 10, ...rates })
    expect(pct).toBeCloseTo(97.794, 2)
  })

  it('honours the quota rate when ¥1 buys more than $1 of credit', () => {
    // ¥1 = $2 credit → paid $2 costs ¥1
    const pct = savingsPercent({ paid: 2, official: 10, usdCnyRate: 6.8, quotaUsdPerCny: 2 })
    expect(pct).toBeCloseTo(((68 - 1) / 68) * 100, 6)
  })

  it('compares CNY with CNY without converting', () => {
    expect(savingsPercent({ paid: 8, paidCurrency: 'cny', official: 16, officialCurrency: 'cny', ...rates })).toBeCloseTo(50, 6)
  })

  it('mixes a CNY paid price with a USD official price', () => {
    // official $10 → ¥68, paid ¥34 → 50%
    expect(savingsPercent({ paid: 34, paidCurrency: 'cny', official: 10, ...rates })).toBeCloseTo(50, 6)
  })

  it('goes negative when paid exceeds official', () => {
    expect(savingsPercent({ paid: 20, official: 1, ...rates })!).toBeLessThan(0)
  })

  it('returns null for missing, zero or unusable inputs', () => {
    expect(savingsPercent({ paid: null, official: 10, ...rates })).toBeNull()
    expect(savingsPercent({ paid: 1, official: undefined, ...rates })).toBeNull()
    expect(savingsPercent({ paid: 1, official: 0, ...rates })).toBeNull()
    expect(savingsPercent({ paid: -1, official: 10, ...rates })).toBeNull()
    expect(savingsPercent({ paid: 1, official: 10, usdCnyRate: 0, quotaUsdPerCny: 1 })).toBeNull()
    expect(savingsPercent({ paid: 1, official: 10, usdCnyRate: 6.8, quotaUsdPerCny: 0 })).toBeNull()
  })
})

describe('currency formatting', () => {
  it('maps currency codes to symbols, defaulting to $', () => {
    expect(currencySymbol('cny')).toBe('¥')
    expect(currencySymbol('usd')).toBe('$')
    expect(currencySymbol(undefined)).toBe('$')
  })

  it('formatScaled uses the given symbol and keeps the $ default', () => {
    expect(formatScaled(3e-6, 1_000_000, 2)).toBe('$3.00')
    expect(formatScaled(3e-6, 1_000_000, 2, '¥')).toBe('¥3.00')
    expect(formatScaled(null, 1, 0, '¥')).toBe('-')
  })
})
