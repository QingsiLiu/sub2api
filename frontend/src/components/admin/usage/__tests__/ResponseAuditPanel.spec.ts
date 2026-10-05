import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import ResponseAuditPanel from '../ResponseAuditPanel.vue'
import type { ResponseAudit } from '@/api/admin/responseAudit'

const api = vi.hoisted(() => ({ list: vi.fn(), stats: vi.fn(), get: vi.fn() }))
vi.mock('@/api/admin/responseAudit', () => ({ responseAuditAPI: api }))
vi.mock('@/api/admin/ops', () => ({ listSystemLogs: vi.fn(), listRequestErrors: vi.fn() }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const row = { id: 1, status: 'empty', reason: 'completed_without_output', model: 'test-model', endpoint: '/v1/messages', protocol: 'messages', turn: 0, settlement_state: 'settled', charged_amount: '0.0838089' } as ResponseAudit
function render() {
  return mount(ResponseAuditPanel, { global: { stubs: {
    DataTable: { props: ['data', 'columns', 'loading'], template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-status" :row="row" /><slot name="cell-cost" :row="row" /><slot name="cell-action" :row="row" /></div></div>' },
    Pagination: true, BaseDialog: true,
  } } })
}
describe('Response audit observation panel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    api.list.mockResolvedValue({ items: [row], total: 1 })
    api.stats.mockResolvedValue({ total: 1, counts: { success: 0, partial_failure: 0, empty: 1, failed: 0, unknown: 0 }, missing_terminal: 0, write_failed: 0, empty_charged_receipts: 1, observation_started_at: null, process_started_at: 'now', dropped_since_start: 0, write_failures_since_start: 0 })
  })
  it('shows an empty outcome with the existing charge, without financial action', async () => {
    const wrapper = render(); await flushPromises()
    expect(wrapper.text()).toContain('responseAudit.status.empty')
    expect(wrapper.text()).toContain('$0.0838089')
    expect(api.list.mock.calls[0]?.[0].page_size).toBe(50)
    expect(wrapper.find('[data-testid="response-audit-panel"]').exists()).toBe(true)
  })
  it('keeps a missing receipt distinct from a zero charge', async () => {
    api.list.mockResolvedValue({ items: [{ ...row, settlement_state: 'not_found', charged_amount: null }], total: 1 })
    const wrapper = render(); await flushPromises()
    expect(wrapper.text()).toContain('responseAudit.settlement.not_found')
    expect(wrapper.text()).not.toContain('$0')
  })
  it('rejects invalid identity filters before querying', async () => {
    const wrapper = render(); await flushPromises(); vi.clearAllMocks()
    await wrapper.findAll('input')[0]!.setValue('invalid-user')
    await wrapper.find('form').trigger('submit'); await flushPromises()
    expect(api.list).not.toHaveBeenCalled()
    expect(wrapper.find('[role="alert"]').text()).toContain('responseAudit.invalidId')
  })
  it('reports unavailable observation instead of keeping stale success statistics', async () => {
    api.list.mockRejectedValue(new Error('unavailable'))
    const wrapper = render(); await flushPromises()
    expect(wrapper.find('[role="alert"]').text()).toContain('unavailable')
    expect(wrapper.find('[data-testid="response-audit-badge"]').exists()).toBe(false)
  })
})
