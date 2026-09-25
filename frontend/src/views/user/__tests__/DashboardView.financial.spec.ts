import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import DashboardView from '../DashboardView.vue'

const { getDashboardStats, getDashboardTrend, getDashboardModels, query, refreshUser, getMyPlatformQuotas } = vi.hoisted(() => ({
  getDashboardStats: vi.fn(), getDashboardTrend: vi.fn(), getDashboardModels: vi.fn(), query: vi.fn(), refreshUser: vi.fn(), getMyPlatformQuotas: vi.fn(),
}))
vi.mock('@/api/usage', () => ({ usageAPI: { getDashboardStats, getDashboardTrend, getDashboardModels, query } }))
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
  getDashboardStats.mockResolvedValue({ today_actual_cost: 90, today_subscription_actual_cost: 90, today_balance_actual_cost: 0, date_basis: 'accounting' })
  getDashboardTrend.mockResolvedValue({ trend: [] })
  getDashboardModels.mockResolvedValue({ models: [] })
  query.mockResolvedValue({ items: [] })
  refreshUser.mockResolvedValue(undefined)
  getMyPlatformQuotas.mockResolvedValue({ platform_quotas: [] })
})
afterEach(() => vi.restoreAllMocks())

describe('user dashboard financial date contract', () => {
  it('loads stats, charts and recent evidence using the same Beijing accounting basis', async () => {
    const wrapper = mountDashboard()
    await flushPromises()
    const dateParams = { date_basis: 'accounting', timezone: 'Asia/Shanghai' }
    expect(getDashboardStats).toHaveBeenCalledWith(dateParams)
    expect(getDashboardTrend).toHaveBeenCalledWith(expect.objectContaining(dateParams))
    expect(getDashboardModels).toHaveBeenCalledWith(expect.objectContaining(dateParams))
    expect(query).toHaveBeenCalledWith(expect.objectContaining({ ...dateParams, page: 1, page_size: 5 }))
    await wrapper.get('[data-testid="financial-date-basis"]').setValue('completed')
    await flushPromises()
    const completed = { date_basis: 'completed', timezone: Intl.DateTimeFormat().resolvedOptions().timeZone }
    expect(getDashboardStats).toHaveBeenLastCalledWith(completed)
    expect(query).toHaveBeenLastCalledWith(expect.objectContaining(completed))
    wrapper.unmount()
  })

  it('ignores an obsolete stats response after the date basis changes again', async () => {
    const wrapper = mountDashboard()
    await flushPromises()
    let resolveOld!: (value: unknown) => void
    getDashboardStats.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    await wrapper.get('[data-testid="financial-date-basis"]').setValue('completed')
    await flushPromises()
    await wrapper.get('[data-testid="financial-date-basis"]').setValue('accounting')
    await flushPromises()
    resolveOld({ today_actual_cost: 66, date_basis: 'completed' })
    await flushPromises()
    expect(wrapper.get('[data-testid="stats"]').text()).toContain('"today_actual_cost":90')
    expect(wrapper.get('[data-testid="stats"]').text()).toContain('"date_basis":"accounting"')
    wrapper.unmount()
  })
})
