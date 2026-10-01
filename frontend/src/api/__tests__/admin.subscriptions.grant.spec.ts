import { beforeEach, describe, expect, it, vi } from 'vitest'
import { subscriptionsAPI } from '../admin/subscriptions'

const { post, get } = vi.hoisted(() => ({ post: vi.fn(), get: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { post, get } }))

describe('admin subscription grant APIs', () => {
  beforeEach(() => vi.clearAllMocks())

  const request = { user_id: 9, daily_limit_usd: 360, days: 30, reason: 'offline payment' }

  it('previews without a key and never applies', async () => {
    post.mockResolvedValue({ data: { snapshot: 's' } })
    expect(await subscriptionsAPI.previewGrant(request)).toEqual({ snapshot: 's' })
    expect(post).toHaveBeenCalledWith('/admin/subscriptions/grant', { ...request, apply: false })
  })

  it('applies the reviewed snapshot with the operation key', async () => {
    post.mockResolvedValue({ data: { applied: true } })
    await subscriptionsAPI.applyGrant({ ...request, expected_snapshot: 's' }, 'subscription-grant-1-x')
    expect(post).toHaveBeenCalledWith(
      '/admin/subscriptions/grant',
      { ...request, expected_snapshot: 's', apply: true },
      { headers: { 'Idempotency-Key': 'subscription-grant-1-x' } }
    )
  })

  it('ends a grant early and loads entitlements', async () => {
    post.mockResolvedValue({ data: { applied: true } })
    get.mockResolvedValue({ data: { lots: [] } })
    await subscriptionsAPI.terminateGrant(42, 8, 'refund', 'subscription-terminate-1-y')
    expect(post).toHaveBeenCalledWith('/admin/subscriptions/42/entitlements/8/terminate', { reason: 'refund' }, {
      headers: { 'Idempotency-Key': 'subscription-terminate-1-y' }
    })
    expect(await subscriptionsAPI.getEntitlements(42)).toEqual({ lots: [] })
    expect(get).toHaveBeenCalledWith('/admin/subscriptions/42/entitlements')
  })
})
