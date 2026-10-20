import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AccountsView from '../AccountsView.vue'

const { listAccounts, setSchedulable } = vi.hoisted(() => ({
  setSchedulable: vi.fn(),
  listAccounts: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      setSchedulable,
      listWithEtag: vi.fn(),
      getBatchTodayStats: vi.fn().mockResolvedValue({ stats: {} }),
      getUpstreamBillingProbeSettings: vi.fn().mockResolvedValue({ enabled: true, interval_minutes: 30 }),
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      toggleSchedulable: vi.fn()
    },
    proxies: { getAll: vi.fn().mockResolvedValue([]) },
    groups: { getAll: vi.fn().mockResolvedValue([]) }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn() })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ token: 'test-token', isSimpleMode: false })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const DataTableStub = {
  props: ['data'],
  template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-schedulable" :row="row" /></div></div>'
}

function mountView() {
  return mount(AccountsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: {
          template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
        },
        DataTable: DataTableStub,
        AccountTableActions: { template: '<div><slot name="after" /></div>' },
        AccountTableFilters: true,
        AccountBulkActionsBar: true,
        Pagination: true,
        ConfirmDialog: true,
        AccountActionMenu: true,
        ImportDataModal: true,
        ReAuthAccountModal: true,
        AccountTestModal: true,
        AccountStatsModal: true,
        ScheduledTestsPanel: true,
        SyncFromCrsModal: true,
        TempUnschedStatusModal: true,
        ErrorPassthroughRulesModal: true,
        TLSFingerprintProfilesModal: true,
        CreateAccountModal: true,
        EditAccountModal: true,
        BulkEditAccountModal: true,
        PlatformTypeBadge: true,
        AccountCapacityCell: true,
        AccountStatusIndicator: true,
        AccountTodayStatsCell: true,
        AccountGroupsCell: true,
        AccountUsageCell: true,
        HelpTooltip: true,
        Icon: true,
        Teleport: true
      }
    }
  })
}

describe('account scheduling switches', () => {
  beforeEach(() => { localStorage.clear(); vi.clearAllMocks() })

  it.each([true, false])('can manually stop an error-stopped account with unpaid=%s in one click', async balance_blocked => {
    const account = { id: 17, name: 'error account', status: 'error', schedulable: false, platform: 'anthropic', type: 'apikey',
      runtime_state: { state: 'temporarily_stopped', stop_reason: 'temporary_failure', balance_blocked } }
    listAccounts.mockResolvedValue({ items: [account], total: 1, page: 1, page_size: 20, pages: 1 })
    setSchedulable.mockResolvedValue({ ...account, runtime_state: { ...account.runtime_state, state: 'stopped', stop_reason: 'manual' } })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[title="admin.accounts.schedulableEnabled"]').trigger('click')
    await flushPromises()
    expect(setSchedulable).toHaveBeenCalledWith(17, false)
    expect(wrapper.find('[title="admin.accounts.schedulableDisabled"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('enables a manually stopped unpaid account without opening a recheck dialog', async () => {
    const account = { id: 17, name: 'manual account', status: 'inactive', schedulable: false, platform: 'anthropic', type: 'apikey',
      runtime_state: { state: 'stopped', stop_reason: 'manual', balance_blocked: true } }
    listAccounts.mockResolvedValue({ items: [account], total: 1, page: 1, page_size: 20, pages: 1 })
    setSchedulable.mockResolvedValue({ ...account, status: 'active', schedulable: true,
      runtime_state: { ...account.runtime_state, state: 'enabled', stop_reason: '' } })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[title="admin.accounts.schedulableDisabled"]').trigger('click')
    await flushPromises()
    expect(setSchedulable).toHaveBeenCalledWith(17, true)
    expect(wrapper.find('[title="admin.accounts.schedulableEnabled"]').exists()).toBe(true)
    wrapper.unmount()
  })
})
