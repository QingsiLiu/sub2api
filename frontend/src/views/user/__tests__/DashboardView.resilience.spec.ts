import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import DashboardView from '../DashboardView.vue'

const { getDashboardStats, getDashboardTrend, getDashboardModels, getByDateRange, refreshUser, getMyPlatformQuotas } = vi.hoisted(() => ({
  getDashboardStats: vi.fn(), getDashboardTrend: vi.fn(), getDashboardModels: vi.fn(), getByDateRange: vi.fn(), refreshUser: vi.fn(), getMyPlatformQuotas: vi.fn(),
}))
vi.mock('@/api/usage', () => ({ usageAPI: { getDashboardStats, getDashboardTrend, getDashboardModels, getByDateRange } }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { balance: 0 }, isSimpleMode: false, refreshUser }) }))
vi.mock('@/api/user', () => ({ getMyPlatformQuotas }))
vi.mock('vue-i18n', async () => ({ ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')), useI18n: () => ({ t: (key: string) => key }) }))

function mountDashboard() {
  return mount(DashboardView, { global: { stubs: {
    AppLayout: { template: '<div><slot /></div>' }, LoadingSpinner: true,
    UserDashboardStats: { props: ['stats'], template: '<div data-testid="stats">{{ JSON.stringify(stats) }}</div>' },
    UserDashboardCharts: true, UserDashboardRecentUsage: true, UserDashboardQuickActions: true,
  } } })
}
beforeEach(() => {
  vi.clearAllMocks()
  getDashboardStats.mockResolvedValue({ today_actual_cost: 90 })
  getDashboardTrend.mockResolvedValue({ trend: [] })
  getDashboardModels.mockResolvedValue({ models: [] })
  getByDateRange.mockResolvedValue({ items: [] })
  refreshUser.mockResolvedValue(undefined)
  getMyPlatformQuotas.mockResolvedValue({ platform_quotas: [] })
})
afterEach(() => vi.restoreAllMocks())

describe('user dashboard resilience', () => {
  it('shows recent usage and shortcuts while the totals are pending', async () => {
    getDashboardStats.mockReturnValue(new Promise(() => {}))
    const wrapper = mountDashboard()
    await flushPromises()
    expect(wrapper.find('user-dashboard-recent-usage-stub').exists()).toBe(true)
    expect(wrapper.find('user-dashboard-quick-actions-stub').exists()).toBe(true)
    expect(wrapper.find('[data-testid="stats"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('keeps the dashboard usable on totals failure and supports retry', async () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    getDashboardStats.mockRejectedValueOnce(new Error('timeout'))
    const wrapper = mountDashboard()
    await flushPromises()
    expect(wrapper.find('user-dashboard-recent-usage-stub').exists()).toBe(true)
    expect(wrapper.get('[role="alert"]').text()).toContain('admin.dashboard.failedToLoad')
    await wrapper.get('[role="alert"] button').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="stats"]').text()).toContain('"today_actual_cost":90')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('does not discard totals when refreshing the profile fails', async () => {
    vi.spyOn(console, 'warn').mockImplementation(() => {})
    refreshUser.mockRejectedValueOnce(new Error('profile timeout'))
    const wrapper = mountDashboard()
    await flushPromises()
    expect(wrapper.get('[data-testid="stats"]').text()).toContain('"today_actual_cost":90')
    wrapper.unmount()
  })
})
