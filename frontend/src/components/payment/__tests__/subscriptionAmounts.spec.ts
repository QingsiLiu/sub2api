import { describe, expect, it } from 'vitest'
import { subscriptionPaymentAmounts } from '../subscriptionAmounts'

describe('decimal subscription gateway totals', () => {
  it('does not round a representable seven-cent fee up to eight cents', () => {
    expect(subscriptionPaymentAmounts(1.4, 'USD', 0, 5)).toEqual({ base: 1.4, fee: 0.07, total: 1.47, valid: true })
  })
  it('rounds FX first and ceilings the fee exactly once as the server does', () => {
    expect(subscriptionPaymentAmounts(9.99, 'CNY', 7.15, 2.5)).toEqual({ base: 71.43, fee: 1.79, total: 73.22, valid: true })
    expect(subscriptionPaymentAmounts(1.05, 'CNY', 1.1, 0)).toEqual({ base: 1.16, fee: 0, total: 1.16, valid: true })
  })
  it('preserves a genuine fractional cent and parses exponent-form rates', () => {
    expect(subscriptionPaymentAmounts(1.4, 'USD', 0, 5.000001).fee).toBe(0.08)
    expect(subscriptionPaymentAmounts(1.4, 'USD', 0, 1e-7).fee).toBe(0.01)
  })
  it('matches gateway zero and three decimal precisions and rejects an invalid base', () => {
    expect(subscriptionPaymentAmounts(1, 'JPY', 0, 5)).toEqual({ base: 1, fee: 1, total: 2, valid: true })
    expect(subscriptionPaymentAmounts(1.4, 'MGA', 0, 5).valid).toBe(false)
    expect(subscriptionPaymentAmounts(1.4, 'BHD', 0, 5)).toEqual({ base: 1.4, fee: 0.07, total: 1.47, valid: true })
    expect(subscriptionPaymentAmounts(1.4, 'ISK', 0, 5).valid).toBe(false)
  })
})
