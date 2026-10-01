import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { AdminEntitlementView } from '@/types'
import SubscriptionLotsDialog from '../SubscriptionLotsDialog.vue'

const { getEntitlements, terminateGrant } = vi.hoisted(() => ({ getEntitlements: vi.fn(), terminateGrant: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { subscriptions: { getEntitlements, terminateGrant } } }))
vi.mock('@/utils/format', () => ({ formatDateTimeToMinute: (value: string) => value.slice(0, 16) }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => params ? `${key} ${JSON.stringify(params)}` : key,
    te: (key: string) => key !== 'subscriptionRights.operation_mystery'
  })
}))

const future = '2099-01-01T00:00:00Z'

function view(daily = 405): AdminEntitlementView {
  return {
    subscription_id: 42,
    user_id: 9,
    contract: { mode: 'legacy_daily' },
    summary: { active_lot_count: 2, daily_limit_usd: daily, remaining_usd: daily - 10, expires_at: future },
    timeline: [{ starts_at: '2026-10-02T00:00:00Z', ends_at: future, daily_limit_usd: daily, lot_count: 2 }],
    lots: [
      { id: 7, source_type: 'campaign', status: 'active', starts_at: '2026-09-30T00:00:00Z', expires_at: future, daily_limit_usd: 45 },
      { id: 8, source_type: 'admin_grant', status: 'active', starts_at: '2026-10-02T00:00:00Z', expires_at: future, daily_limit_usd: 360 },
      { id: 6, source_type: 'payment', status: 'active', starts_at: '2026-09-01T00:00:00Z', expires_at: future, daily_limit_usd: 90 }
    ],
    operations: [
      { id: 1, entitlement_id: 8, operation: 'admin_grant', source_type: 'admin', source_reference: 'k', actor_id: 1, actor_email: 'admin@example.com', reason: '线下付款', created_at: '2026-10-02T00:00:00Z' },
      { id: 2, entitlement_id: 7, operation: 'mystery', source_type: 'campaign', source_reference: '', actor_id: 0, created_at: '2026-09-30T00:00:00Z' }
    ]
  } as unknown as AdminEntitlementView
}

const wrappers: ReturnType<typeof mountDialog>[] = []
let adminId = 900

function mountDialog() {
  const wrapper = mount(SubscriptionLotsDialog, {
    props: { show: true, subscriptionId: 42, email: '178531351@qq.com' },
    global: {
      stubs: {
        BaseDialog: {
          name: 'BaseDialog',
          props: ['show', 'title', 'closeOnEscape', 'showCloseButton', 'width'],
          emits: ['close'],
          template: '<section><h2>{{ title }}</h2><slot /><footer><slot name="footer" /></footer></section>'
        }
      }
    }
  })
  wrappers.push(wrapper)
  return wrapper
}

beforeEach(() => {
  getEntitlements.mockReset()
  terminateGrant.mockReset()
  getEntitlements.mockResolvedValueOnce(view()).mockResolvedValue(view(45))
  terminateGrant.mockResolvedValue({ applied: true })
  localStorage.setItem('auth_user', JSON.stringify({ id: ++adminId }))
  sessionStorage.clear()
})

afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount())
})

describe('SubscriptionLotsDialog', () => {
  it('shows lots, sources and audit history, offering early end only for admin grants', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    expect(getEntitlements).toHaveBeenCalledWith(42)
    expect(wrapper.get('[data-test="lots-daily-limit"]').text()).toBe('$405')
    expect(wrapper.text()).toContain('admin.subscriptions.lots.source_admin_grant')
    expect(wrapper.text()).toContain('admin.subscriptions.lots.source_campaign')
    expect(wrapper.find('[data-test="terminate-8"]').exists()).toBe(true)
    expect(wrapper.find('[data-test="terminate-7"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="terminate-6"]').exists()).toBe(false)
    const history = wrapper.get('[data-test="lots-history"]').text()
    expect(history).toContain('subscriptionRights.operation_admin_grant')
    expect(history).toContain('admin@example.com')
    expect(history).toContain('线下付款')
    expect(history).toContain('mystery')
  })

  it('ends an admin grant with a reason and reloads', async () => {
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.get('[data-test="terminate-8"]').trigger('click')
    await wrapper.get('[data-test="terminate-form"] textarea').setValue('  用户退款  ')
    await wrapper.get('[data-test="terminate-form"]').trigger('submit')
    await flushPromises()

    expect(terminateGrant).toHaveBeenCalledWith(42, 8, '用户退款', expect.stringMatching(new RegExp(`^subscription-terminate-${adminId}-`)))
    expect(wrapper.emitted('changed')).toHaveLength(1)
    expect(getEntitlements).toHaveBeenCalledTimes(2)
    expect(wrapper.get('[data-test="lots-daily-limit"]').text()).toBe('$45')
    expect(wrapper.find('[data-test="terminate-form"]').exists()).toBe(false)
  })

  it('retries an uncertain early end with the same key', async () => {
    terminateGrant.mockRejectedValueOnce({ status: 500, message: 'boom' })
    const wrapper = mountDialog()
    await flushPromises()
    await wrapper.get('[data-test="terminate-8"]').trigger('click')
    await wrapper.get('[data-test="terminate-form"] textarea').setValue('用户退款')
    await wrapper.get('[data-test="terminate-form"]').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.subscriptions.grant.retryHint')

    await wrapper.get('[data-test="terminate-form"]').trigger('submit')
    await flushPromises()
    expect(terminateGrant).toHaveBeenCalledTimes(2)
    expect(terminateGrant.mock.calls[1][3]).toBe(terminateGrant.mock.calls[0][3])
    expect(wrapper.emitted('changed')).toHaveLength(1)
  })
})
