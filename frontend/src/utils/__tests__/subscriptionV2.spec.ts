import { describe, expect, it } from 'vitest'
import type { UserSubscription, SubscriptionContract } from '@/types'
import type { PaymentOrder, SubscriptionPlan } from '@/types/payment'
import { contractLabel, isPaidSubscriptionReview, subscriptionActions, subscriptionOrderLabel, subscriptionQuota } from '../subscriptionV2'

const contract: SubscriptionContract = { mode: 'v2', kind: 'month', unit_daily_usd: 45, quantity: 2, period_days: 30, term_id: 'term-1', revision: 4, plan_id: 1, plan_name: 'Monthly 45', status: 'active', user_id: 382, subscription_id: 9, starts_at: '2026-09-01T00:00:00Z', expires_at: '2026-10-01T00:00:00Z' }
const now = Date.parse('2026-09-21T00:00:00Z')
const plan = { id: 1, validity_days: 30, validity_unit: 'days', daily_limit_usd: 45 } as SubscriptionPlan
const sub = { contract, id: 9, status: 'active', expires_at: contract.expires_at } as UserSubscription

describe('subscription V2 rules', () => {
  it('allows each of the five tiers on a new term', () => {
    for (const [days, daily] of [[7, 90], [7, 180], [30, 45], [30, 90], [30, 180]]) {
      expect(subscriptionActions({ ...plan, validity_days: days, daily_limit_usd: daily }, [], now).actions).toEqual(['purchase'])
    }
  })
  it('rejects unsupported historical tiers without offering purchase', () => {
    expect(subscriptionActions({ ...plan, daily_limit_usd: 9 }, [], now).actions).toEqual([])
    expect(subscriptionActions({ ...plan, validity_days: 7, daily_limit_usd: 45 }, [], now).actions).toEqual([])
  })
  it('preserves unit identity and offers stack/renew only at the current tier', () => {
    expect(subscriptionActions(plan, [sub], now).actions).toEqual(['stack', 'renew'])
    expect(subscriptionActions({ ...plan, id: 2, daily_limit_usd: 90 }, [sub], now).actions).toEqual(['upgrade'])
    expect(subscriptionActions({ ...plan, id: 3, daily_limit_usd: 180 }, [sub], now).actions).toEqual(['upgrade'])
  })
  it('blocks cross-type, lower/equal different tiers and legacy changes', () => {
    expect(subscriptionActions({ ...plan, id: 4, validity_days: 7, daily_limit_usd: 180 }, [sub], now).reason).toBe('subscriptionRights.sameTypeOnly')
    expect(subscriptionActions({ ...plan, id: 5, daily_limit_usd: 45 }, [sub], now).reason).toBe('subscriptionRights.noDowngrade')
    expect(subscriptionActions(plan, [{ ...sub, contract: { ...contract, mode: 'legacy_daily' } }], now).actions).toEqual([])
    expect(subscriptionActions(plan, [{ ...sub, contract: undefined }], now).actions).toEqual([])
    expect(subscriptionActions(plan, [sub, { ...sub, id: 10 }], now).actions).toEqual([])
  })
  it('does not offer purchase or changes for an unexpired suspended contract', () => {
    expect(subscriptionActions(plan, [{ ...sub, status: 'suspended' }], now)).toEqual({ actions: [], reason: 'subscriptionRights.suspendedHint' })
  })
  it('allows selecting a different type after the old term expires', () => {
    expect(subscriptionActions({ ...plan, validity_days: 7, daily_limit_usd: 90 }, [sub], Date.parse(contract.expires_at)).actions).toEqual(['purchase'])
  })
})

describe('subscription V2 quota projection', () => {
  it('uses the ledger over stale parent counters and ignores weekly exhaustion', () => {
    const quota = subscriptionQuota({ ...sub, daily_usage_usd: 0, weekly_usage_usd: 315, monthly_usage_usd: 500, quota_summary: { active_lot_count: 2, daily_limit_usd: 90, daily_usage_usd: 87.04857312, weekly_limit_usd: 315, weekly_usage_usd: 315, monthly_limit_usd: 1350, monthly_usage_usd: 500 } })
    expect(quota.dailyUsed).toBe(87.04857312)
    expect(quota.remaining).toBeCloseTo(2.95142688, 8)
    expect(quota.weekly).toBeNull()
    expect(quota.monthly).toBeNull()
  })
  it('keeps an explicit server remaining value including zero authoritative', () => {
    const quota = subscriptionQuota({ ...sub, quota_summary: { active_lot_count: 2, daily_limit_usd: 90, daily_usage_usd: 1, remaining_usd: 0, weekly_limit_usd: null, monthly_limit_usd: null, weekly_usage_usd: 0, monthly_usage_usd: 0 } })
    expect(quota.remaining).toBe(0)
  })
})

describe('subscription V2 labels', () => {
  const t = (key: string, args?: Record<string, unknown>) => `${key}${args ? ':' + JSON.stringify(args) : ''}`
  it('labels renewal cycles separately from added units', () => {
    const order = { operation: 'renew', periods: 3, units: 2 } as PaymentOrder
    expect(subscriptionOrderLabel(order, t)).toContain('subscriptionRights.periods:{"count":3}')
    expect(subscriptionOrderLabel({ ...order, operation: 'stack' }, t)).toContain('subscriptionRights.units:{"count":2}')
    expect(subscriptionOrderLabel({ ...order, operation: 'upgrade' }, t)).toBe('subscriptionRights.upgrade')
    expect(contractLabel(contract, t)).toContain('"count":2')
  })
  it('identifies paid fulfillment failures without mislabeling unpaid failures', () => {
    expect(isPaidSubscriptionReview({ order_type: 'subscription', status: 'FAILED', paid_at: '2026-09-21' })).toBe(true)
    expect(isPaidSubscriptionReview({ order_type: 'subscription', status: 'FAILED' })).toBe(false)
    expect(isPaidSubscriptionReview({ order_type: 'balance', status: 'FAILED', paid_at: '2026-09-21' })).toBe(false)
  })
})

describe('complete active-plan eligibility matrix', () => {
  const catalog = [[7, 90], [7, 180], [30, 45], [30, 90], [30, 180]].map(([days, daily], index) => ({ ...plan, id: index + 1, validity_days: days, daily_limit_usd: daily }))
  it.each(catalog.flatMap(from => catalog.map(to => ({ from, to }))))('$from.validity_days-day $from.daily_limit_usd to $to.validity_days-day $to.daily_limit_usd', ({ from, to }) => {
    const current = { ...sub, contract: { ...contract, period_days: from.validity_days, kind: from.validity_days === 7 ? 'week' as const : 'month' as const, unit_daily_usd: from.daily_limit_usd, plan_id: from.id } }
    const result = subscriptionActions(to, [current], now)
    if (from.id === to.id) expect(result.actions).toEqual(['stack', 'renew'])
    else if (from.validity_days !== to.validity_days) expect(result.reason).toBe('subscriptionRights.sameTypeOnly')
    else if (to.daily_limit_usd > from.daily_limit_usd) expect(result.actions).toEqual(['upgrade'])
    else expect(result.reason).toBe('subscriptionRights.noDowngrade')
  })
  it.each(['active', 'expired', 'revoked', 'suspended'] as const)('allows a new type after %s historical rights expire', status => {
    expect(subscriptionActions(catalog[0], [{ ...sub, status, expires_at: '2026-09-01', contract: { ...contract, mode: 'legacy_daily' } }], now).actions).toEqual(['purchase'])
  })
})
