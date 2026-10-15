import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import SupplierAccountsView from '../SupplierAccountsView.vue'

enableAutoUnmount(afterEach)

const { getMeta, listAccounts, createAccount, updateAccount, pauseAccount } = vi.hoisted(() => ({
  getMeta: vi.fn(),
  listAccounts: vi.fn(),
  createAccount: vi.fn(),
  updateAccount: vi.fn(),
  pauseAccount: vi.fn()
}))

vi.mock('@/api/supplier', () => ({
  supplierAPI: {
    getMeta,
    listAccounts,
    createAccount,
    updateAccount,
    pauseAccount,
    deleteAccount: vi.fn(),
    testAccountUrl: (id: number) => `/api/v1/supplier/accounts/${id}/test`
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() })
}))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key })
}))

const account = {
  id: 11,
  name: 'my key',
  notes: null,
  platform: 'openai',
  type: 'apikey',
  base_url: '',
  api_key_hint: 'sk-o****3456',
  concurrency: 3,
  rate_multiplier: 1,
  status: 'active',
  schedulable: true,
  review_state: 'running',
  error_message: '',
  groups: ['default'],
  last_used_at: null,
  created_at: '2026-10-16T00:00:00Z',
  updated_at: '2026-10-16T00:00:00Z'
}

function mountView() {
  return mount(SupplierAccountsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
        ConfirmDialog: true,
        Icon: true
      }
    }
  })
}

describe('供应商账号页', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    getMeta.mockResolvedValue({ platforms: ['openai', 'anthropic'] })
    listAccounts.mockResolvedValue({ items: [account], total: 1, page: 1, page_size: 20, pages: 1 })
    createAccount.mockResolvedValue({ ...account, id: 12, review_state: 'pending' })
    updateAccount.mockResolvedValue(account)
  })

  it('只展示供应商接口返回的账号，并显示审核状态', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(listAccounts).toHaveBeenCalledWith(1, expect.any(Number), { platform: undefined, search: undefined })
    expect(wrapper.text()).toContain('my key')
    expect(wrapper.text()).toContain('supplier.accounts.states.running')
    expect(wrapper.text()).toContain('sk-o****3456')
  })

  it('上传只提交供应商可填字段', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.find('button.btn-primary').trigger('click')
    const form = wrapper.find('#supplier-account-form')
    await form.find('input[type="text"]').setValue('new key')
    await form.find('input[type="password"]').setValue(' sk-new-1234567890 ')
    await form.trigger('submit')
    await flushPromises()
    expect(createAccount).toHaveBeenCalledWith({
      platform: 'openai',
      name: 'new key',
      notes: null,
      api_key: 'sk-new-1234567890',
      base_url: '',
      concurrency: 3
    })
    const payload = createAccount.mock.calls[0][0]
    expect(payload).not.toHaveProperty('group_ids')
    expect(payload).not.toHaveProperty('priority')
    expect(payload).not.toHaveProperty('rate_multiplier')
  })

  it('编辑时 Key 留空不提交 api_key', async () => {
    const wrapper = mountView()
    await flushPromises()
    const editButton = wrapper.findAll('button').find((b) => b.text().includes('common.edit'))
    await editButton!.trigger('click')
    await wrapper.find('#supplier-account-form').trigger('submit')
    await flushPromises()
    expect(updateAccount).toHaveBeenCalledTimes(1)
    expect(updateAccount.mock.calls[0][0]).toBe(11)
    expect(updateAccount.mock.calls[0][1]).not.toHaveProperty('api_key')
  })
})
