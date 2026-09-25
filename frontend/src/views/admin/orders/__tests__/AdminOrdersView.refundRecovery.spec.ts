import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AdminOrdersView from '../AdminOrdersView.vue'

const { getOrders, queryRefund, refundOrder, showError, showSuccess } = vi.hoisted(() => ({ getOrders: vi.fn(), queryRefund: vi.fn(), refundOrder: vi.fn(), showError: vi.fn(), showSuccess: vi.fn() }))
vi.mock('@/api/admin/payment', () => ({ adminPaymentAPI: { getOrders, queryRefund, refundOrder }, default: { getOrders, queryRefund, refundOrder } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess }) }))
vi.mock('vue-i18n', async () => ({ ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')), useI18n: () => ({ t: (key: string) => key }) }))
beforeEach(() => {
  vi.clearAllMocks()
  getOrders.mockResolvedValue({ data: { items: [{ id: 42, status: 'REFUNDING', pay_amount: 1, amount: 1, created_at: '2026-09-26T00:00:00Z' }], total: 1 } })
  queryRefund.mockResolvedValue({ data: { success: false, warning: 'refund outcome pending confirmation' } })
})
describe('durable refund recovery', () => {
  it('offers read-only provider reconciliation, not a fresh refund, for a crashed REFUNDING order', async () => {
    const wrapper = mount(AdminOrdersView, { global: { stubs: {
      AppLayout: { template: '<div><slot /></div>' }, OrderTable: { props: ['orders'], template: '<div><template v-for="row in orders"><slot name="actions" :row="row" /></template></div>' },
      Select: true, Icon: true, BaseDialog: true, Pagination: true, AdminRefundDialog: true, OrderStatusBadge: true,
    } } })
    await flushPromises()
    const query = wrapper.findAll('button').find(button => button.text() === 'payment.admin.queryRefundStatus')
    expect(query).toBeDefined()
    await query!.trigger('click')
    await flushPromises()
    expect(queryRefund).toHaveBeenCalledWith(42)
    expect(refundOrder).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
