import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import SubscriptionProgressMini from '../SubscriptionProgressMini.vue'

const { store } = vi.hoisted(() => ({ store: {
  activeSubscriptions: [] as Record<string, unknown>[], hasActiveSubscriptions: true,
  fetchActiveSubscriptions: vi.fn().mockResolvedValue([]), startPolling: vi.fn(), stopPolling: vi.fn()
} }))
vi.mock('@/stores', () => ({ useSubscriptionStore: () => store }))
vi.mock('@/utils/featureFlags', () => ({ FeatureFlags: { subscription: 'subscription' }, isFeatureFlagEnabled: () => true }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('subscription header quota', () => {
  it('shows the actual daily overrun and purchased quota returned by the active endpoint', async () => {
    store.activeSubscriptions = [{ id: 1, status: 'active', daily_usage_usd: 90.21295472, weekly_usage_usd: 181.26703904, monthly_usage_usd: 181.26703904,
      plan: { name: 'Purchased plan', daily_limit_usd: 900 },
      quota_summary: { active_lot_count: 1, daily_limit_usd: 90, weekly_limit_usd: 630, monthly_limit_usd: 2700 }
    }]
    const wrapper = mount(SubscriptionProgressMini, { global: { stubs: { Icon: true, RouterLink: true } } })
    await wrapper.get('button').trigger('click')
    expect(wrapper.text()).toContain('$90.21/$90.00')
    expect(wrapper.text()).toContain('$181.27/$630.00')
    expect(wrapper.text()).not.toContain('$900.00')
    wrapper.unmount()
  })

  it('does not advertise unlimited quota when all lots have expired', async () => {
    store.activeSubscriptions = [{ id: 1, status: 'expired', quota_summary: { active_lot_count: 0, daily_limit_usd: 0, weekly_limit_usd: 0, monthly_limit_usd: 0 } }]
    const wrapper = mount(SubscriptionProgressMini, { global: { stubs: { Icon: true, RouterLink: true } } })
    await wrapper.get('button').trigger('click')
    expect(wrapper.text()).not.toContain('subscriptionProgress.unlimited')
    wrapper.unmount()
  })
})

describe('V2 header ledger projection', () => {
  it.each([
    { mode: 'v2', count: 2, limit: 90, used: 87.04857312, remaining: 2.95142688 },
    { mode: 'legacy_daily', count: 2, limit: 90, used: 90.12, remaining: 0 },
    { mode: 'legacy_daily', count: 1, limit: null, used: 100, remaining: null },
    { mode: 'v2', count: 0, limit: 0, used: 0, remaining: 0 },
  ])('$mode count $count displays current daily values only', async ({ mode, count, limit, used, remaining }) => {
    store.activeSubscriptions = [{ id: 42, status: 'active', daily_usage_usd: 999, contract: { mode, kind: 'month', quantity: 2, unit_daily_usd: 45 }, quota_summary: { active_lot_count: count, daily_limit_usd: limit, daily_usage_usd: used, remaining_usd: remaining, weekly_limit_usd: 315, monthly_limit_usd: 1350 } }]
    const wrapper = mount(SubscriptionProgressMini, { global: { stubs: { Icon: true, RouterLink: true } } })
    await wrapper.get('button').trigger('click')
    expect(wrapper.text()).not.toContain('$999')
    expect(wrapper.text()).not.toContain('subscriptionProgress.weekly')
    expect(wrapper.text()).not.toContain('subscriptionProgress.monthly')
    if (limit) expect(wrapper.text()).toContain(`$${used.toFixed(2)}/$${limit.toFixed(2)}`)
    if (remaining !== null) expect(wrapper.text()).toContain(`$${remaining.toFixed(4)}`)
    expect(wrapper.text().includes('subscriptionProgress.unlimited')).toBe(count > 0 && limit === null)
    wrapper.unmount()
  })
})
