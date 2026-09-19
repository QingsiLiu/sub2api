import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'

import SubscriptionsView from '../SubscriptionsView.vue'

const { listSubscriptions, assignSubscription, getAllGroups, listUsers, searchUsageUsers, showError } = vi.hoisted(() => ({
  listSubscriptions: vi.fn(),
  assignSubscription: vi.fn(),
  showError: vi.fn(),
  getAllGroups: vi.fn(),
  listUsers: vi.fn(),
  searchUsageUsers: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    subscriptions: { list: listSubscriptions, assign: assignSubscription },
    groups: { getAll: getAllGroups },
    users: { list: listUsers },
    usage: { searchUsers: searchUsageUsers }
  }
}))

vi.mock('@/api/admin/payment', () => ({ adminPaymentAPI: { getPlans: vi.fn().mockResolvedValue({ data: [{ id: 3, name: 'Plan 3', validity_days: 30, validity_unit: 'days' }, { id: 7, name: 'Plan 7', validity_days: 30, validity_unit: 'days' }] }) } }))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: { id?: number }) =>
        key === 'admin.redeem.userPrefix' ? `User #${params?.id}` : key
    })
  }
})

const DataTableStub = {
  props: ['data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id">
        <slot name="cell-user" :row="row" /><slot name="cell-usage" :row="row" />
      </div>
    </div>
  `
}

const RouterLinkStub = defineComponent({
  name: 'RouterLink',
  props: { to: { type: [String, Object], required: true } },
  template: `<a :href="typeof to === 'string' ? to : to.path + '?user_id=' + to.query?.user_id"><slot /></a>`
})

describe('admin subscription users', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    localStorage.clear()
    listSubscriptions.mockResolvedValue({
      items: [{
        id: 9,
        user_id: 42,
        group_id: 3,
        status: 'active',
        starts_at: '2026-01-01T00:00:00Z',
        expires_at: null,
        daily_usage_usd: 0,
        weekly_usage_usd: 0,
        monthly_usage_usd: 0,
        daily_window_start: null,
        weekly_window_start: null,
        monthly_window_start: null,
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
        user: { email: 'reader@example.com', username: 'Reader' }
      }],
      total: 1,
      pages: 1
    })
    assignSubscription.mockResolvedValue({})
    getAllGroups.mockResolvedValue([])
    listUsers.mockResolvedValue({
      items: [{ id: 42, email: 'reader@example.com' }],
      total: 1,
      pages: 1
    })
    searchUsageUsers.mockResolvedValue([
      { id: 14, email: 'deleted@example.com', deleted: true }
    ])
  })

  const mountView = () => mount(SubscriptionsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /></div>' },
        DataTable: DataTableStub,
        RouterLink: RouterLinkStub,
        Pagination: true,
        BaseDialog: {
          props: ['show'],
          template: '<div v-if="show"><slot /><slot name="footer" /></div>'
        },
        ConfirmDialog: true,
        EmptyState: true,
        Select: true,
        GroupBadge: true,
        GroupOptionItem: true,
        Icon: true,
        Teleport: true
      }
    }
  })

  it('shows lot reset countdowns when parent windows are absent', async () => {
    const now = Date.now()
    listSubscriptions.mockResolvedValue({ items: [{
      id: 9, user_id: 42, status: 'active', starts_at: new Date(now - 86400000).toISOString(), expires_at: new Date(now + 30 * 86400000).toISOString(),
      daily_usage_usd: 90.21, weekly_usage_usd: 181.27, monthly_usage_usd: 181.27,
      daily_window_start: null, weekly_window_start: null, monthly_window_start: null,
      quota_summary: { active_lot_count: 1, daily_limit_usd: 90, weekly_limit_usd: 630, monthly_limit_usd: 2700,
        daily_reset_at: new Date(now + 6 * 3600000).toISOString(), weekly_reset_at: new Date(now + 2 * 86400000).toISOString(), monthly_reset_at: new Date(now + 12 * 86400000).toISOString() }
    }], total: 1, pages: 1 })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.findAll('.reset-info')).toHaveLength(3)
    expect(wrapper.text()).toContain('admin.subscriptions.resetInHoursMinutes')
    expect(wrapper.text()).toContain('admin.subscriptions.resetInDaysHours')
    expect(wrapper.text()).not.toContain('admin.subscriptions.windowNotActive')
    wrapper.unmount()
  })

  it('displays purchased aggregate quota instead of the edited sale plan', async () => {
    listSubscriptions.mockResolvedValue({ items: [{ id: 9, user_id: 42, status: 'active', plan: { id: 3, name: 'Edited plan', daily_limit_usd: 90 }, quota_summary: { active_lot_count: 1, daily_limit_usd: 45, weekly_limit_usd: null, monthly_limit_usd: null, daily_usage_usd: 2 }, daily_usage_usd: 2, weekly_usage_usd: 2, monthly_usage_usd: 2 }], total: 1, pages: 1 })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).toContain('45.00')
    expect(wrapper.text()).not.toContain('90.00')
    wrapper.unmount()
  })

  it('searches current users when assigning a subscription', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const wrapper = mountView()
    try {
      await flushPromises()
      await wrapper.findAll('button')
        .find((button) => button.text() === 'admin.subscriptions.assignSubscription')!
        .trigger('click')
      const search = wrapper.get('[data-assign-user-search] input')
      await search.trigger('focus')
      await search.setValue('  example.com  ')
      await vi.advanceTimersByTimeAsync(300)
      await flushPromises()

      expect(listUsers).toHaveBeenCalledWith(1, 30, {
        search: 'example.com', sort_by: 'email', sort_order: 'asc'
      })
      expect(searchUsageUsers).not.toHaveBeenCalled()
      const picker = wrapper.get('[data-assign-user-search]')
      expect(picker.text()).toContain('reader@example.com')
      expect(picker.text()).not.toContain('deleted@example.com')
      await picker.get('button').trigger('click')
      expect((search.element as HTMLInputElement).value).toBe('reader@example.com')
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
    }
  })

  it.each(['another', ''])('clears the assignment user immediately when input changes to %j', async (keyword) => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const wrapper = mountView()
    try {
      await flushPromises()
      await wrapper.findAll('button')
        .find((button) => button.text() === 'admin.subscriptions.assignSubscription')!
        .trigger('click')
      const form = wrapper.get('#assign-subscription-form')
      form.getComponent({ name: 'Select' }).vm.$emit('update:modelValue', 3)
      const search = wrapper.get('[data-assign-user-search] input')
      await search.trigger('focus')
      await search.setValue('reader')
      await vi.advanceTimersByTimeAsync(300)
      await flushPromises()
      await wrapper.get('[data-assign-user-search] button').trigger('click')

      await search.setValue(keyword)
      await form.trigger('submit')
      await flushPromises()

      expect(assignSubscription).not.toHaveBeenCalled()
      expect(showError).toHaveBeenCalledWith('admin.subscriptions.pleaseSelectUser')
      expect(listUsers).toHaveBeenCalledTimes(1)

      listUsers.mockResolvedValue({ items: [{ id: 84, email: 'another@example.com' }] })
      await search.trigger('focus')
      await search.setValue('another')
      await vi.advanceTimersByTimeAsync(300)
      await flushPromises()
      await wrapper.get('[data-assign-user-search] button').trigger('click')
      await form.trigger('submit')
      await flushPromises()

      expect(assignSubscription).toHaveBeenCalledTimes(1)
      expect(assignSubscription).toHaveBeenCalledWith({
        user_id: 84, plan_id: 3, validity_days: 30
      })
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
    }
  })

  it('keeps deleted users available when filtering subscription history', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
    const wrapper = mountView()
    try {
      await flushPromises()
      const search = wrapper.get('[data-filter-user-search] input')
      await search.trigger('focus')
      await search.setValue('deleted')
      await vi.advanceTimersByTimeAsync(300)
      await flushPromises()

      expect(searchUsageUsers).toHaveBeenCalledWith('deleted')
      expect(listUsers).not.toHaveBeenCalled()
      const picker = wrapper.get('[data-filter-user-search]')
      expect(picker.text()).toContain('deleted@example.com')
      await picker.get('button').trigger('click')
      expect(listSubscriptions).toHaveBeenLastCalledWith(
        1, expect.any(Number), expect.objectContaining({ user_id: 14 }), expect.any(Object)
      )
    } finally {
      wrapper.unmount()
      vi.useRealTimers()
    }
  })

  it('renders the user email as a link to that user filtered usage records', async () => {
    const wrapper = mountView()

    await flushPromises()

    const link = wrapper.findAllComponents(RouterLinkStub).find(link => typeof link.props('to') === 'object')!
    expect(link.text()).toBe('reader@example.com')
    expect(link.props('to')).toEqual({ path: '/admin/usage', query: { user_id: 42 } })
  })

  it('uses the user ID label for the usage link when username mode has no username', async () => {
    localStorage.setItem('subscription-user-column-mode', 'username')
    listSubscriptions.mockResolvedValue({
      items: [{
        id: 9,
        user_id: 42,
        group_id: 3,
        status: 'active',
        starts_at: '2026-01-01T00:00:00Z',
        expires_at: null,
        daily_usage_usd: 0,
        weekly_usage_usd: 0,
        monthly_usage_usd: 0,
        daily_window_start: null,
        weekly_window_start: null,
        monthly_window_start: null,
        created_at: '2026-01-01T00:00:00Z',
        updated_at: '2026-01-01T00:00:00Z',
        user: { email: 'reader@example.com' }
      }],
      total: 1,
      pages: 1
    })

    const wrapper = mountView()
    await flushPromises()

    const link = wrapper.findAllComponents(RouterLinkStub).find(link => typeof link.props('to') === 'object')!
    expect(link.text()).toBe('User #42')
    expect(link.props('to')).toEqual({ path: '/admin/usage', query: { user_id: 42 } })
  })
})
