import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import UserOrdersView from '../UserOrdersView.vue'
import type { PaymentOrder } from '@/types/payment'

const getMyOrders = vi.hoisted(() => vi.fn())
const getRefundEligibleProviders = vi.hoisted(() => vi.fn())
const showError = vi.hoisted(() => vi.fn())
const downloadReceiptPdf = vi.hoisted(() => vi.fn())

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() }),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    siteName: '给力 API',
    apiBaseUrl: 'https://sub.geiliapi.com/',
    contactInfo: 'support@example.com',
    showError,
    showSuccess: vi.fn(),
  }),
  useAuthStore: () => ({
    user: { email: 'hcdmumu@gmail.com', username: 'mumu' },
  }),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getMyOrders,
    getRefundEligibleProviders,
    cancelOrder: vi.fn(),
    requestRefund: vi.fn(),
  },
}))

vi.mock('@/components/payment/receiptPdf', () => ({
  downloadReceiptPdf,
}))

import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh'

const completed: PaymentOrder = {
  id: 81,
  user_id: 9,
  amount: 25000,
  pay_amount: 25000,
  currency: 'CNY',
  fee_rate: 0,
  payment_type: 'alipay',
  out_trade_no: 'sub2_20260806abc',
  status: 'COMPLETED',
  order_type: 'balance',
  created_at: '2026-08-06T07:55:07.000Z',
  expires_at: '2026-08-06T08:25:07.000Z',
  paid_at: '2026-08-06T07:55:07.000Z',
  refund_amount: 0,
}

const pending: PaymentOrder = { ...completed, id: 82, status: 'PENDING', out_trade_no: 'sub2_pending' }

function mountOrders(items: PaymentOrder[]) {
  getMyOrders.mockResolvedValue({ data: { items, total: items.length } })
  getRefundEligibleProviders.mockResolvedValue({ data: { provider_instance_ids: [] } })
  const i18n = createI18n({ legacy: false, locale: 'zh', messages: { zh } })
  return mount(UserOrdersView, {
    global: {
      plugins: [i18n],
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        Pagination: true,
        Select: true,
        Icon: true,
        BaseDialog: {
          props: ['show', 'title'],
          template: '<div v-if="show"><h3>{{ title }}</h3><slot /><slot name="footer" /></div>',
        },
        OrderTable: {
          props: ['orders'],
          template: '<div><div v-for="row in orders" :key="row.id"><slot name="actions" :row="row" /></div></div>',
        },
      },
    },
  })
}

describe('UserOrdersView receipt', () => {
  beforeEach(() => {
    getMyOrders.mockReset()
    getRefundEligibleProviders.mockReset()
    downloadReceiptPdf.mockReset()
    showError.mockReset()
  })

  it('shows a receipt button only for credited orders', async () => {
    const wrapper = mountOrders([completed, pending])
    await flushPromises()
    const buttons = wrapper.findAll('button').filter(button => button.text().includes('payment.orders.viewReceipt'))
    expect(buttons).toHaveLength(1)
  })

  it.each([undefined, '2026091823001481451440748515'])('opens the receipt and downloads a PDF with provider number %s', async (paymentTradeNo) => {
    const tradeNo = paymentTradeNo || completed.out_trade_no
    const wrapper = mountOrders([{ ...completed, payment_trade_no: paymentTradeNo }])
    await flushPromises()
    const open = wrapper.findAll('button').find(button => button.text().includes('payment.orders.viewReceipt'))
    expect(open).toBeTruthy()
    await open!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('payment.receipt.title')
    expect(wrapper.text()).toContain(tradeNo)
    expect(wrapper.text()).toContain('payment.receipt.itemBalance')
    expect(wrapper.text()).toContain('人民币贰万伍仟元整')
    expect(wrapper.text()).toContain('给力 API')
    expect(wrapper.text()).toContain('https://sub.geiliapi.com')
    expect(wrapper.text()).not.toContain('Sub2API')
    expect(wrapper.findAll('.receipt-sheet ol li')).toHaveLength(2)
    expect(wrapper.text()).not.toContain('payment.receipt.noteNotInvoice')

    const download = wrapper.findAll('button').find(button => button.text().includes('payment.orders.downloadReceipt'))
    expect(download).toBeTruthy()
    await download!.trigger('click')
    expect(downloadReceiptPdf).toHaveBeenCalledTimes(1)
    expect(downloadReceiptPdf.mock.calls[0][0].tradeNo).toBe(tradeNo)
    expect(downloadReceiptPdf.mock.calls[0][0].filename).toMatch(/^RCP-.*\.pdf$/)
  })
})
