import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const pollOrderStatus = vi.hoisted(() => vi.fn())
const cancelOrder = vi.hoisted(() => vi.fn())
const verifyOrder = vi.hoisted(() => vi.fn())
const showError = vi.hoisted(() => vi.fn())
const toCanvas = vi.hoisted(() => vi.fn())

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key,
    }),
  }
})

vi.mock('@/stores/payment', () => ({
  usePaymentStore: () => ({
    pollOrderStatus,
  }),
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({
    showError,
  }),
}))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    cancelOrder,
    verifyOrder,
  },
}))

vi.mock('qrcode', () => ({
  default: {
    toCanvas,
  },
}))

import PaymentStatusPanel from '../PaymentStatusPanel.vue'

const orderFactory = (status: string) => ({
  id: 42,
  user_id: 9,
  amount: 88,
  pay_amount: 88,
  fee_rate: 0,
  payment_type: 'alipay',
  out_trade_no: 'sub2_20260420abcd1234',
  status,
  order_type: 'balance',
  created_at: '2026-04-20T12:00:00Z',
  expires_at: '2099-01-01T12:30:00Z',
  refund_amount: 0,
})

describe('PaymentStatusPanel', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    pollOrderStatus.mockReset()
    cancelOrder.mockReset()
    verifyOrder.mockReset()
    showError.mockReset()
    toCanvas.mockReset().mockResolvedValue(undefined)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('treats RECHARGING as a successful terminal state', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('RECHARGING'))

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('shows reopen button in QR mode when payUrl is also available', async () => {
    const openSpy = vi.spyOn(window, 'open').mockReturnValue({ closed: false } as Window)

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        payUrl: 'https://pay.example.com/session/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    expect(wrapper.text()).toContain('payment.qr.openPayWindow')

    await wrapper.get('button.btn.btn-secondary.text-sm').trigger('click')
    expect(openSpy).toHaveBeenCalledWith(
      'https://pay.example.com/session/42',
      'paymentPopup',
      expect.any(String),
    )

    openSpy.mockRestore()
  })

  it('uses generic QR copy for custom methods that contain built-in names', async () => {
    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'card_alipay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()

    expect(wrapper.text()).toContain('payment.qr.scanToPay')
    expect(wrapper.text()).not.toContain('payment.qr.scanAlipay')
  })

  it('actively verifies a stuck pending order and settles it when upstream confirms payment', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    verifyOrder.mockResolvedValue({
      data: orderFactory('COMPLETED'),
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        qrCode: 'https://pay.example.com/qr/42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'wxpay',
        orderType: 'balance',
      },
      global: {
        stubs: {
          Icon: true,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(pollOrderStatus).toHaveBeenCalledWith(42)
    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')
    expect(wrapper.text()).toContain('payment.result.success')
    expect(wrapper.emitted('success')).toHaveLength(1)
  })

  it('actively verifies a pending mobile Alipay precreate order', async () => {
    const originalLocation = window.location
    const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { assign: vi.fn() },
    })
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      get: () => false,
    })
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    verifyOrder.mockResolvedValue({ data: orderFactory('COMPLETED') })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 88,
        payAmount: 88,
        qrCode: 'https://qr.alipay.com/dynamic-order-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
        outTradeNo: 'sub2_20260420abcd1234',
        mobileAlipayDeepLink: true,
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')
    expect(wrapper.emitted('success')).toHaveLength(1)

    wrapper.unmount()
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
    if (originalHidden) Object.defineProperty(document, 'hidden', originalHidden)
  })

  it('actively verifies a pending desktop Alipay order', async () => {
    pollOrderStatus.mockResolvedValue(orderFactory('PENDING'))
    verifyOrder.mockResolvedValue({ data: orderFactory('COMPLETED') })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 88,
        payAmount: 88,
        qrCode: 'https://qr.alipay.com/desktop-order-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
        outTradeNo: 'sub2_20260420abcd1234',
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(3000)
    await flushPromises()

    expect(verifyOrder).toHaveBeenCalledWith('sub2_20260420abcd1234')
    expect(wrapper.emitted('success')).toHaveLength(1)

    wrapper.unmount()
  })

  it('keeps the QR fallback hidden until the Alipay app launch times out', async () => {
    const originalLocation = window.location
    const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
    const assign = vi.fn()
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { assign },
    })
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      get: () => false,
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 88,
        payAmount: 88,
        qrCode: 'https://qr.alipay.com/dynamic-order-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
        outTradeNo: 'sub2_20260420abcd1234',
        mobileAlipayDeepLink: true,
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    expect(assign).toHaveBeenCalledWith(expect.stringContaining('alipays://platformapi/startapp?saId=10000007&qrcode='))
    expect(wrapper.find('[data-test="alipay-qr-fallback"]').exists()).toBe(false)

    await vi.advanceTimersByTimeAsync(2200)
    await flushPromises()

    expect(wrapper.find('[data-test="alipay-qr-fallback"]').exists()).toBe(true)
    expect(wrapper.text()).toContain('payment.qr.saveQRCode')
    expect(wrapper.text()).toContain('sub2_20260420abcd1234')
    expect(toCanvas).toHaveBeenCalledWith(expect.any(HTMLCanvasElement), 'https://qr.alipay.com/dynamic-order-42', expect.any(Object))

    wrapper.unmount()
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
    if (originalHidden) Object.defineProperty(document, 'hidden', originalHidden)
  })

  it('does not show the QR fallback after the page enters the background', async () => {
    const originalLocation = window.location
    const originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
    let hidden = false
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { assign: vi.fn() },
    })
    Object.defineProperty(document, 'hidden', {
      configurable: true,
      get: () => hidden,
    })

    const wrapper = mount(PaymentStatusPanel, {
      props: {
        orderId: 42,
        amount: 88,
        payAmount: 88,
        qrCode: 'https://qr.alipay.com/dynamic-order-42',
        expiresAt: '2099-01-01T12:30:00Z',
        paymentType: 'alipay',
        orderType: 'balance',
        outTradeNo: 'sub2_20260420abcd1234',
        mobileAlipayDeepLink: true,
      },
      global: { stubs: { Icon: true } },
    })

    await flushPromises()
    hidden = true
    document.dispatchEvent(new Event('visibilitychange'))
    await vi.advanceTimersByTimeAsync(2200)
    await flushPromises()

    expect(wrapper.find('[data-test="alipay-qr-fallback"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('payment.qr.alipayContinueInApp')

    wrapper.unmount()
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
    if (originalHidden) Object.defineProperty(document, 'hidden', originalHidden)
  })
})

describe('subscription fulfillment outcomes', () => {
  beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date('2026-09-21T12:00:00Z')); pollOrderStatus.mockReset(); cancelOrder.mockReset(); verifyOrder.mockReset(); showError.mockReset() })
  afterEach(() => vi.useRealTimers())
  const mountSubscription = () => mount(PaymentStatusPanel, { props: { orderId: 42, qrCode: 'test', expiresAt: '2026-09-21T12:00:10Z', paymentType: 'wxpay', orderType: 'subscription' }, global: { stubs: { Icon: true } } })
  it.each(['PAID', 'RECHARGING'])('keeps %s processing past checkout expiry, then surfaces a paid fulfillment conflict', async status => {
    pollOrderStatus.mockResolvedValue({ ...orderFactory(status), order_type: 'subscription', paid_at: '2026-09-21T12:00:01Z' })
    const wrapper = mountSubscription()
    await vi.advanceTimersByTimeAsync(3_001)
    expect(wrapper.text()).toContain('payment.result.processing')
    expect(wrapper.emitted('success')).toBeUndefined()
    expect(wrapper.findAll('button').some(button => button.text().includes('payment.qr.cancelOrder'))).toBe(false)
    await vi.advanceTimersByTimeAsync(8_000)
    expect(wrapper.text()).not.toContain('payment.qr.expired')
    pollOrderStatus.mockResolvedValue({ ...orderFactory('FAILED'), order_type: 'subscription', paid_at: '2026-09-21T12:00:01Z' })
    await vi.advanceTimersByTimeAsync(3_000)
    expect(wrapper.text()).toContain('subscriptionRights.paidReview')
    expect(wrapper.get('a').attributes('href')).toBe('/orders')
    expect(wrapper.emitted('success')).toBeUndefined()
    expect(wrapper.emitted('settled')).toEqual([['review']])
    wrapper.unmount()
  })
  it('announces success only after subscription fulfillment completes', async () => {
    pollOrderStatus.mockResolvedValue({ ...orderFactory('PAID'), order_type: 'subscription' })
    const wrapper = mountSubscription()
    await vi.advanceTimersByTimeAsync(3_001)
    pollOrderStatus.mockResolvedValue({ ...orderFactory('COMPLETED'), order_type: 'subscription' })
    await vi.advanceTimersByTimeAsync(3_000)
    expect(wrapper.emitted('success')).toHaveLength(1)
    expect(wrapper.text()).toContain('payment.result.subscriptionSuccess')
    wrapper.unmount()
  })
  it('retains checkout when explicit cancel is rejected, then follows paid completion', async () => {
    pollOrderStatus.mockResolvedValue({ ...orderFactory('PENDING'), order_type: 'subscription' })
    cancelOrder.mockRejectedValue(new Error('already paid'))
    const wrapper = mountSubscription()
    await wrapper.findAll('button').find(button => button.text().includes('payment.qr.cancelOrder'))!.trigger('click')
    await flushPromises()
    expect(cancelOrder).toHaveBeenCalledWith(42)
    expect(wrapper.emitted('settled')).toBeUndefined()
    expect(showError).toHaveBeenCalled()
    pollOrderStatus.mockResolvedValue({ ...orderFactory('COMPLETED'), order_type: 'subscription' })
    await vi.advanceTimersByTimeAsync(3_001)
    expect(wrapper.emitted('success')).toHaveLength(1)
    wrapper.unmount()
  })
  it('marks cancellation only after the explicit cancel request succeeds', async () => {
    cancelOrder.mockResolvedValue({})
    const wrapper = mountSubscription()
    await wrapper.findAll('button').find(button => button.text().includes('payment.qr.cancelOrder'))!.trigger('click')
    await flushPromises()
    expect(wrapper.emitted('settled')).toEqual([['cancelled']])
    expect(wrapper.emitted('success')).toBeUndefined()
    wrapper.unmount()
  })
})

describe('subscription Alipay handoff lifecycle', () => {
  let originalLocation: Location
  let originalHidden: PropertyDescriptor | undefined
  beforeEach(() => {
    vi.useFakeTimers(); pollOrderStatus.mockReset(); verifyOrder.mockReset(); toCanvas.mockResolvedValue(undefined)
    originalLocation = window.location
    originalHidden = Object.getOwnPropertyDescriptor(document, 'hidden')
    Object.defineProperty(window, 'location', { configurable: true, value: { assign: vi.fn() } })
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => false })
  })
  afterEach(() => {
    Object.defineProperty(window, 'location', { configurable: true, value: originalLocation })
    if (originalHidden) Object.defineProperty(document, 'hidden', originalHidden)
    else Reflect.deleteProperty(document, 'hidden')
    vi.useRealTimers()
  })
  it('shows processing above app-launch UI once paid and stops offering duplicate app payment', async () => {
    pollOrderStatus.mockResolvedValue({ ...orderFactory('PAID'), order_type: 'subscription' })
    const wrapper = mount(PaymentStatusPanel, { props: { orderId: 42, qrCode: 'https://pay.example/qr/42', expiresAt: '2099-01-01', paymentType: 'alipay', orderType: 'subscription', mobileAlipayDeepLink: true }, global: { stubs: { Icon: true } } })
    await vi.advanceTimersByTimeAsync(3_001)
    expect(wrapper.text()).toContain('payment.result.processing')
    expect(wrapper.find('[data-test="reopen-alipay"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="save-alipay-qr"]').exists()).toBe(false)
    expect(wrapper.emitted('success')).toBeUndefined()
    wrapper.unmount()
  })
  it('routes an unpaid handoff back to existing orders without discarding recovery', async () => {
    pollOrderStatus.mockResolvedValue({ ...orderFactory('PENDING'), order_type: 'subscription' })
    const wrapper = mount(PaymentStatusPanel, { props: { orderId: 42, qrCode: 'https://pay.example/qr/42', expiresAt: '2099-01-01', paymentType: 'alipay', orderType: 'subscription', mobileAlipayDeepLink: true }, global: { stubs: { Icon: true } } })
    await vi.advanceTimersByTimeAsync(2_201)
    await flushPromises()
    expect(wrapper.find('a[href="/orders"]').exists()).toBe(true)
    expect(wrapper.findAll('button').some(button => button.text().includes('payment.result.backToRecharge'))).toBe(false)
    expect(wrapper.emitted('done')).toBeUndefined()
    wrapper.unmount()
  })
})
