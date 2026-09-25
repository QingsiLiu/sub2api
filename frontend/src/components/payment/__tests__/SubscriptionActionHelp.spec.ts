import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import type { UserSubscription } from '@/types'
import SubscriptionActionHelp from '../SubscriptionActionHelp.vue'

vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => `${key} ${JSON.stringify(params ?? {})}` })
}))
vi.mock('@/utils/format', () => ({ formatDateTimeToMinute: (date: Date) => date.toISOString() }))

afterEach(() => vi.useRealTimers())
const subscription = (id: number, expiresAt: string | null, status = 'active') => ({ id, status, expires_at: expiresAt } as UserSubscription)
const mountHelp = (subscriptions: UserSubscription[], reason = 'subscriptionRights.compatibilityHint', contactInfo = '') => mount(SubscriptionActionHelp, {
  props: { subscriptions, reason, contactInfo },
  global: { stubs: { Teleport: true, Transition: false, Icon: true } }
})

describe('subscription restriction help', () => {
  it('shows the last active expiry, support contact, and a working close button', async () => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-09-25T15:30:00+08:00'))
    const wrapper = mountHelp([
      subscription(1, '2026-09-25T13:55:52+08:00'),
      subscription(2, '2026-09-26T19:10:44+08:00'),
      subscription(3, '2026-09-27T13:15:51+08:00'),
      subscription(4, '2026-10-01T00:00:00Z', 'revoked')
    ], undefined, 'help@example.invalid')
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    await wrapper.get('button').trigger('click')
    const dialog = wrapper.get('[role="dialog"]')
    expect(dialog.text()).toContain('subscriptionRights.compatibilityHint')
    expect(dialog.text()).toContain('2026-09-27T05:15:51.000Z')
    expect(dialog.text()).toContain('help@example.invalid')
    await dialog.findAll('button').find(button => button.text().startsWith('common.close'))!.trigger('click')
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it.each([
    { name: 'null expiry', subs: [subscription(1, null)], reason: 'subscriptionRights.compatibilityHint' },
    { name: 'mixed unknown expiry', subs: [subscription(1, '2099-01-01'), subscription(2, 'invalid')], reason: 'subscriptionRights.compatibilityHint' },
    { name: 'unknown expiry', subs: [subscription(1, '')], reason: 'subscriptionRights.compatibilityHint' },
    { name: 'invalid expiry', subs: [subscription(1, 'invalid')], reason: 'subscriptionRights.compatibilityHint' },
    { name: 'suspended rights', subs: [subscription(1, '2099-01-01', 'suspended')], reason: 'subscriptionRights.compatibilityHint' },
    { name: 'unsupported plan', subs: [subscription(1, '2099-01-01')], reason: 'subscriptionRights.unavailablePlan' },
    { name: 'expired rights', subs: [subscription(1, '2000-01-01')], reason: 'subscriptionRights.compatibilityHint' }
  ])('does not promise a purchase time for $name', async ({ subs, reason }) => {
    const wrapper = mountHelp(subs, reason)
    await wrapper.get('button').trigger('click')
    expect(wrapper.get('[role="dialog"]').text()).toContain(reason)
    expect(wrapper.text()).not.toContain('subscriptionRights.resubscribeAt')
    expect(wrapper.text()).not.toContain('subscriptionRights.supportContact')
    wrapper.unmount()
  })

  it('closes with Escape without changing any subscription', async () => {
    const subs = [subscription(1, '2099-01-01')]
    const before = JSON.stringify(subs)
    const wrapper = mountHelp(subs)
    await wrapper.get('button').trigger('click')
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }))
    await wrapper.vm.$nextTick()
    expect(wrapper.find('[role="dialog"]').exists()).toBe(false)
    expect(JSON.stringify(subs)).toBe(before)
    wrapper.unmount()
  })
})
