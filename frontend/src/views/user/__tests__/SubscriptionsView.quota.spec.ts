import { afterEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import SubscriptionsView from '../SubscriptionsView.vue'

const { getMySubscriptions } = vi.hoisted(() => ({ getMySubscriptions: vi.fn() }))
vi.mock('@/api/subscriptions', () => ({ default: { getMySubscriptions } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))
vi.mock('vue-router', () => ({ useRouter: () => ({ push: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => `${key} ${JSON.stringify(params ?? {})}` })
}))

afterEach(() => vi.useRealTimers())

describe('subscription entitlement display', () => {
  it('renders purchased aggregate limits and lot resets despite stale parent windows', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-09-19T18:00:00+08:00'))
    getMySubscriptions.mockResolvedValue([{
      id: 1, status: 'active', starts_at: '2026-09-18T21:00:00+08:00', expires_at: '2026-10-18T21:00:00+08:00',
      daily_usage_usd: 90.21, weekly_usage_usd: 181.27, monthly_usage_usd: 181.27,
      daily_window_start: '2026-09-18T00:00:00+08:00', weekly_window_start: null, monthly_window_start: null,
      plan: { name: 'Purchased plan', daily_limit_usd: 900, weekly_limit_usd: 9000, monthly_limit_usd: 90000 },
      quota_summary: { active_lot_count: 2, daily_limit_usd: 180, weekly_limit_usd: 1260, monthly_limit_usd: 5400,
        daily_reset_at: '2026-09-20T00:00:00+08:00', weekly_reset_at: '2026-09-21T18:00:00+08:00', monthly_reset_at: '2026-10-01T18:00:00+08:00' }
    }])
    const wrapper = mount(SubscriptionsView, { global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Icon: true } } })
    await flushPromises()
    expect(wrapper.text()).toContain('$90.21 / $180.00')
    expect(wrapper.text()).toContain('$181.27 / $1260.00')
    expect(wrapper.text()).toContain('$181.27 / $5400.00')
    expect(wrapper.text()).not.toContain('$900.00')
    expect(wrapper.text()).toContain('6h 0m')
    expect(wrapper.text()).toContain('2d 0h')
    expect(wrapper.text()).toContain('12d 0h')
    expect(wrapper.text()).not.toContain('windowNotActive')
    wrapper.unmount()
  })
  it('does not label an expired or refunded quota as unlimited', async () => {
    getMySubscriptions.mockResolvedValue([{
      id: 1, status: 'expired', starts_at: '2026-08-01T00:00:00Z', expires_at: '2026-09-01T00:00:00Z',
      daily_usage_usd: 0, weekly_usage_usd: 0, monthly_usage_usd: 0,
      plan: { name: 'Expired plan', daily_limit_usd: 90, weekly_limit_usd: 630, monthly_limit_usd: 2700 },
      quota_summary: { active_lot_count: 0, daily_limit_usd: 0, weekly_limit_usd: 0, monthly_limit_usd: 0 }
    }])
    const wrapper = mount(SubscriptionsView, { global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Icon: true } } })
    await flushPromises()
    expect(wrapper.text()).toContain('userSubscriptions.status.expired')
    expect(wrapper.text()).not.toContain('userSubscriptions.unlimited')
    wrapper.unmount()
  })

})

describe('V2 subscription user quota matrix', () => {
  it.each([
    { mode: 'v2', count: 2, limit: 90, used: 87.04857312, remaining: 2.95142688, status: 'active', unlimited: false },
    { mode: 'v2', count: 2, limit: 90, used: 90.12, remaining: 0, status: 'active', unlimited: false },
    { mode: 'v2', count: 0, limit: 0, used: 0, remaining: 0, status: 'expired', unlimited: false },
    { mode: 'legacy_daily', count: 2, limit: 90, used: 87.04857312, remaining: 2.95142688, status: 'active', unlimited: false },
    { mode: 'legacy_daily', count: 1, limit: null, used: 100, remaining: null, status: 'active', unlimited: true },
  ])('$mode $status daily limit $limit uses the ledger and action gates', async ({ mode, count, limit, used, remaining, status, unlimited }) => {
    getMySubscriptions.mockResolvedValue([{ id: 42, status, starts_at: '2026-09-01', expires_at: '2099-01-01', daily_usage_usd: 999, weekly_usage_usd: 315, monthly_usage_usd: 1000, contract: { mode, kind: 'month', quantity: 2, unit_daily_usd: 45, plan_id: 7 }, quota_summary: { active_lot_count: count, daily_limit_usd: limit, daily_usage_usd: used, remaining_usd: remaining, weekly_limit_usd: 315, monthly_limit_usd: 1350 } }])
    const wrapper = mount(SubscriptionsView, { global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, Icon: true } } })
    await flushPromises()
    expect(wrapper.text()).not.toContain('$999')
    expect(wrapper.text()).not.toContain('userSubscriptions.weekly')
    expect(wrapper.text()).not.toContain('userSubscriptions.monthly')
    if (limit) expect(wrapper.text()).toContain(`$${used.toFixed(2)} / $${limit.toFixed(2)}`)
    if (remaining !== null) expect(wrapper.text()).toContain(`$${remaining.toFixed(4)}`)
    expect(wrapper.text().includes('userSubscriptions.unlimited')).toBe(unlimited)
    expect(wrapper.findAll('button').filter(button => ['subscriptionRights.stack', 'subscriptionRights.renew', 'subscriptionRights.upgrade'].some(action => button.text().includes(action))).length).toBe(mode === 'v2' && status === 'active' ? 3 : 0)
    if (mode === 'legacy_daily') expect(wrapper.text()).toContain('subscriptionRights.compatibilityHint')
    wrapper.unmount()
  })
})
