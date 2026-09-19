import { describe, expect, it } from 'vitest'

import type { UserSubscription } from '@/types'
import { getExpirationDateRelation, getRemainingExpiryDuration, getSubscriptionResetAt } from '../subscriptionQuota'

describe('subscription expiry timing', () => {
  it('uses local calendar dates for today and tomorrow', () => {
    const now = new Date(2026, 2, 7, 23, 30)

    expect(getExpirationDateRelation(new Date(2026, 2, 7, 23, 45), now)).toBe('today')
    expect(getExpirationDateRelation(new Date(2026, 2, 8, 3, 30), now)).toBe('tomorrow')
  })

  it('treats the exact expiry instant and elapsed expiries as expired', () => {
    const now = new Date(2026, 6, 30, 9, 0)

    expect(getExpirationDateRelation(now, now)).toBe('expired')
    expect(getRemainingExpiryDuration(now, now)).toBeNull()
    expect(getExpirationDateRelation(new Date(2026, 6, 30, 8, 59), now)).toBe('expired')
    expect(getRemainingExpiryDuration(new Date(2026, 6, 30, 8, 59), now)).toBeNull()
  })

  it('rejects invalid target and current dates', () => {
    const invalid = new Date('invalid')
    const valid = new Date(2026, 6, 30, 9, 0)

    expect(getExpirationDateRelation(invalid, valid)).toBeNull()
    expect(getExpirationDateRelation(valid, invalid)).toBeNull()
    expect(getRemainingExpiryDuration(invalid, valid)).toBeNull()
    expect(getRemainingExpiryDuration(valid, invalid)).toBeNull()
  })

  it('returns rounded-up hours and minutes for an expiry under 24 hours away', () => {
    const now = new Date(2026, 6, 30, 9, 0)

    expect(getRemainingExpiryDuration(new Date(2026, 6, 31, 8, 30), now)).toEqual({
      unit: 'hoursMinutes',
      hours: 23,
      minutes: 30
    })
    expect(getRemainingExpiryDuration(new Date(now.getTime() + 1), now)).toEqual({
      unit: 'hoursMinutes',
      hours: 0,
      minutes: 1
    })
    expect(getRemainingExpiryDuration(new Date(now.getTime() + 23 * 60 * 60 * 1000 + 1), now)).toEqual({
      unit: 'hoursMinutes',
      hours: 23,
      minutes: 1
    })
  })

  it('preserves rounded-up day display from 24 hours onward', () => {
    const now = new Date(2026, 6, 30, 9, 0)

    expect(getRemainingExpiryDuration(new Date(now.getTime() + 24 * 60 * 60 * 1000), now)).toEqual({
      unit: 'days',
      days: 1
    })
    expect(getRemainingExpiryDuration(new Date(now.getTime() + 24 * 60 * 60 * 1000 + 1), now)).toEqual({
      unit: 'days',
      days: 2
    })
  })
})


describe('subscription effective reset times', () => {
  const subscription = {
    starts_at: '2026-09-18T21:00:00+08:00', expires_at: '2026-10-18T21:00:00+08:00',
    daily_window_start: '2026-09-18T00:00:00+08:00', weekly_window_start: null, monthly_window_start: null
  }
  it('uses the next lot reset even if the parent window is stale or absent', () => {
    const quota_summary: NonNullable<UserSubscription['quota_summary']> = {
      active_lot_count: 2, daily_limit_usd: 180, weekly_limit_usd: 1260, monthly_limit_usd: 5400,
      daily_usage_usd: 90.21, weekly_usage_usd: 181.27, monthly_usage_usd: 181.27,
      daily_reset_at: '2026-09-20T00:00:00+08:00', weekly_reset_at: '2026-09-25T21:00:00+08:00', monthly_reset_at: null
    }
    expect(getSubscriptionResetAt({ ...subscription, quota_summary }, 'daily')).toBe(quota_summary.daily_reset_at)
    expect(getSubscriptionResetAt({ ...subscription, quota_summary }, 'weekly')).toBe(quota_summary.weekly_reset_at)
    expect(getSubscriptionResetAt({ ...subscription, monthly_window_start: subscription.starts_at, quota_summary }, 'monthly')).toBeNull()
  })
  it('retains legacy windows and one-time daily quota expiry', () => {
    expect(getSubscriptionResetAt(subscription, 'daily')).toBe('2026-09-18T16:00:00.000Z')
    expect(getSubscriptionResetAt(subscription, 'weekly')).toBeNull()
    expect(getSubscriptionResetAt({ ...subscription, expires_at: '2026-09-19T21:00:00+08:00' }, 'daily')).toBe('2026-09-19T21:00:00+08:00')
    expect(getSubscriptionResetAt({ ...subscription, daily_window_start: 'invalid' }, 'daily')).toBeNull()
  })
})
