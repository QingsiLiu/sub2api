import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { AdminGrantResult } from '@/types'
import AdminGrantDialog from '../AdminGrantDialog.vue'

const { previewGrant, applyGrant, listUsers } = vi.hoisted(() => ({
  previewGrant: vi.fn(),
  applyGrant: vi.fn(),
  listUsers: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: { subscriptions: { previewGrant, applyGrant }, users: { list: listUsers } } }))
vi.mock('@/utils/format', () => ({ formatDateTimeToMinute: (value: string) => value.slice(0, 16) }))
vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => params ? `${key} ${JSON.stringify(params)}` : key
  })
}))

function result(overrides: Partial<AdminGrantResult> = {}): AdminGrantResult {
  return {
    applied: false,
    subscription_id: 42,
    entitlement_id: 0,
    created_pool: false,
    converts_from_v2: false,
    current_daily_limit_usd: 45,
    after_daily_limit_usd: 405,
    daily_usage_usd: 12.5,
    starts_at: '2026-10-02T00:00:00Z',
    expires_at: '2026-11-01T00:00:00Z',
    timeline: [
      { starts_at: '2026-10-02T00:00:00Z', ends_at: '2026-10-07T00:00:00Z', daily_limit_usd: 405, lot_count: 2 },
      { starts_at: '2026-10-07T00:00:00Z', ends_at: '2026-11-01T00:00:00Z', daily_limit_usd: 360, lot_count: 1 }
    ],
    lots: [
      { id: 7, source_type: 'campaign', status: 'active', starts_at: '2026-09-30T00:00:00Z', expires_at: '2026-10-07T00:00:00Z', daily_limit_usd: 45, new: false },
      { id: 0, source_type: 'admin_grant', status: 'active', starts_at: '2026-10-02T00:00:00Z', expires_at: '2026-11-01T00:00:00Z', daily_limit_usd: 360, new: true }
    ],
    warnings: ['self_service_paused'],
    snapshot: 'snap-1',
    ...overrides
  } as AdminGrantResult
}

const wrappers: ReturnType<typeof mountDialog>[] = []
let adminId = 500

function mountDialog() {
  const wrapper = mount(AdminGrantDialog, {
    props: { show: true },
    global: {
      stubs: {
        BaseDialog: {
          name: 'BaseDialog',
          props: ['show', 'title', 'closeOnEscape', 'showCloseButton', 'width'],
          emits: ['close'],
          template: '<section><h2>{{ title }}</h2><slot /><footer><slot name="footer" /></footer></section>'
        },
        Icon: true
      }
    }
  })
  wrappers.push(wrapper)
  return wrapper
}

async function fillForm(wrapper: ReturnType<typeof mountDialog>) {
  await wrapper.get('#admin-grant-user').setValue('178531351')
  await wrapper.get('#admin-grant-user').trigger('focus')
  await new Promise(resolve => setTimeout(resolve, 320))
  await flushPromises()
  const option = wrapper.findAll('button').find(button => button.text().includes('#9'))
  expect(option).toBeTruthy()
  await option!.trigger('click')
  await wrapper.get('#admin-grant-reason').setValue('线下付款')
}

async function submit(wrapper: ReturnType<typeof mountDialog>) {
  await wrapper.get('form').trigger('submit')
  await flushPromises()
}

beforeEach(() => {
  previewGrant.mockReset()
  applyGrant.mockReset()
  listUsers.mockReset()
  listUsers.mockResolvedValue({ items: [{ id: 9, email: '178531351@qq.com' }] })
  previewGrant.mockResolvedValue(result())
  applyGrant.mockResolvedValue(result({ applied: true, entitlement_id: 88 }))
  localStorage.setItem('auth_user', JSON.stringify({ id: ++adminId }))
  sessionStorage.clear()
})

afterEach(() => {
  wrappers.splice(0).forEach(wrapper => wrapper.unmount())
})

describe('AdminGrantDialog', () => {
  it('previews, then applies with the reviewed snapshot and an idempotency key', async () => {
    const wrapper = mountDialog()
    await fillForm(wrapper)
    await submit(wrapper)

    expect(previewGrant).toHaveBeenCalledWith({ user_id: 9, daily_limit_usd: 360, days: 30, reason: '线下付款' })
    expect(wrapper.get('[data-test="grant-limit-change"]').text()).toBe('$45 → $405')
    expect(wrapper.get('[data-test="grant-timeline"]').text()).toContain('$360')
    expect(wrapper.get('[data-test="grant-warnings"]').text()).toContain('warning_self_service_paused')
    expect((wrapper.get('#admin-grant-daily').element as HTMLInputElement).closest('fieldset')!.disabled).toBe(true)

    await submit(wrapper)
    expect(applyGrant).toHaveBeenCalledWith(
      { user_id: 9, daily_limit_usd: 360, days: 30, reason: '线下付款', expected_snapshot: 'snap-1' },
      expect.stringMatching(new RegExp(`^subscription-grant-${adminId}-`))
    )
    expect(wrapper.get('[data-test="grant-done"]').exists()).toBe(true)
    expect(wrapper.emitted('completed')?.[0]?.[0]).toMatchObject({ applied: true, entitlement_id: 88 })
    expect(wrapper.find('[data-test="grant-submit"]').exists()).toBe(false)
    expect(Object.keys(sessionStorage)).toHaveLength(0)
  })

  it('reuses the same key when the outcome is uncertain', async () => {
    applyGrant.mockRejectedValueOnce({ status: 504, message: 'timeout' })
    const wrapper = mountDialog()
    await fillForm(wrapper)
    await submit(wrapper)
    await submit(wrapper)

    expect(wrapper.text()).toContain('admin.subscriptions.grant.retryHint')
    expect(wrapper.get('[data-test="grant-submit"]').text()).toBe('admin.subscriptions.grant.retry')
    expect(Object.keys(sessionStorage)).toHaveLength(1)

    await submit(wrapper)
    expect(applyGrant).toHaveBeenCalledTimes(2)
    expect(applyGrant.mock.calls[1][1]).toBe(applyGrant.mock.calls[0][1])
    expect(wrapper.get('[data-test="grant-done"]').exists()).toBe(true)
  })

  it('keeps the key across a fresh dialog with the same terms', async () => {
    applyGrant.mockRejectedValueOnce({ status: 502, message: 'bad gateway' })
    const first = mountDialog()
    await fillForm(first)
    await submit(first)
    await submit(first)
    const key = applyGrant.mock.calls[0][1]
    first.unmount()

    const second = mountDialog()
    await fillForm(second)
    await submit(second)
    await submit(second)
    expect(applyGrant.mock.calls[1][1]).toBe(key)
  })

  it('drops the preview when the snapshot no longer matches', async () => {
    applyGrant.mockRejectedValueOnce({ status: 409, reason: 'ADMIN_GRANT_SNAPSHOT_MISMATCH', message: 'changed' })
    const wrapper = mountDialog()
    await fillForm(wrapper)
    await submit(wrapper)
    await submit(wrapper)

    expect(wrapper.text()).toContain('admin.subscriptions.grant.snapshotChanged')
    expect(wrapper.find('[data-test="grant-preview"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="grant-submit"]').text()).toBe('admin.subscriptions.grant.preview')
    expect(Object.keys(sessionStorage)).toHaveLength(0)

    await submit(wrapper)
    expect(previewGrant).toHaveBeenCalledTimes(2)
  })

  it('requires a user and a reason before previewing', async () => {
    const wrapper = mountDialog()
    await submit(wrapper)
    expect(previewGrant).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.subscriptions.grant.userRequired')
  })
})
