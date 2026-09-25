import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import PaymentView from '../PaymentView.vue'
import subscriptionsAPI from '@/api/subscriptions'
import { PAYMENT_RECOVERY_STORAGE_KEY } from '@/components/payment/paymentFlow'
import { formatPaymentAmount } from '@/components/payment/currency'
import AmountInput from '@/components/payment/AmountInput.vue'
import SubscriptionPlanCard from '@/components/payment/SubscriptionPlanCard.vue'
import PaymentMethodSelector from '@/components/payment/PaymentMethodSelector.vue'
import SubscriptionGroupRates from '@/components/payment/SubscriptionGroupRates.vue'
import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'
import type { UserSubscription } from '@/types'
import type { CheckoutInfoResponse, MethodLimit, SubscriptionPlan } from '@/types/payment'

enableAutoUnmount(afterEach)

const deviceState = vi.hoisted(() => ({ mobile: true }))
const routeState = vi.hoisted(() => ({
  path: '/purchase',
  query: {} as Record<string, unknown>,
}))

const routerReplace = vi.hoisted(() => vi.fn())
const routerPush = vi.hoisted(() => vi.fn())
const routerResolve = vi.hoisted(() => vi.fn(() => ({ href: '/payment/stripe?mock=1' })))
const createOrder = vi.hoisted(() => vi.fn())
const refreshUser = vi.hoisted(() => vi.fn())
const activeSubscriptionState = vi.hoisted(() => ({ items: [] as UserSubscription[] }))
const fetchActiveSubscriptions = vi.hoisted(() => vi.fn().mockResolvedValue(undefined))
const showError = vi.hoisted(() => vi.fn())
const showInfo = vi.hoisted(() => vi.fn())
const showWarning = vi.hoisted(() => vi.fn())
const legacyOptionsState = vi.hoisted(() => ({value:{enabled:false,pools:[]} as import('@/types/payment').LegacyManagementOptions}))
const getCheckoutInfo = vi.hoisted(() => vi.fn())
const quoteSubscription = vi.hoisted(() => vi.fn(async (request: { plan_id: number; units?: number; periods?: number }) => {
  const checkout = await getCheckoutInfo.mock.results.at(-1)?.value
  const plan = checkout?.data.plans.find((p: { id: number }) => p.id === request.plan_id)
  return { data: { quote_id: 'quote-test', expires_at: '2099-01-01T00:00:00Z', order_amount: (plan?.price ?? 128) * (request.units ?? request.periods ?? 1), can_renew_lots: 0, projected: { active_lot_count: (request.units ?? request.periods ?? 1), daily_limit_usd: 45, weekly_limit_usd: null, monthly_limit_usd: null, expires_at: '2099-02-01T00:00:00Z' } } }
}))
const bridgeInvoke = vi.hoisted(() => vi.fn())
const translate = vi.hoisted(() => vi.fn((key: string) => key))
// Public settings live in a reactive holder so tests can flip feature flags after mount
// and exercise the watchers that react to them.
const appStoreState = vi.hoisted(() => ({
  setPublicSettings: (_value: Record<string, unknown> | undefined) => {},
}))

vi.mock('vue-router', async () => {
  const actual = await vi.importActual<typeof import('vue-router')>('vue-router')
  return {
    ...actual,
    useRoute: () => routeState,
    useRouter: () => ({
      replace: routerReplace,
      push: routerPush,
      resolve: routerResolve,
    }),
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: translate,
    }),
  }
})

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    user: {
      username: 'demo-user',
      balance: 0,
    },
    refreshUser,
  }),
}))

vi.mock('@/stores/payment', () => ({
  usePaymentStore: () => ({
    createOrder,
  }),
}))

vi.mock('@/stores/subscriptions', () => ({
  useSubscriptionStore: () => ({
    get activeSubscriptions() { return activeSubscriptionState.items },
    fetchActiveSubscriptions,
  }),
}))

vi.mock('@/stores', async () => {
  const { reactive } = await import('vue')
  const state = reactive({ cachedPublicSettings: undefined as Record<string, unknown> | undefined })
  appStoreState.setPublicSettings = (value) => {
    state.cachedPublicSettings = value
  }
  return {
    useAppStore: () => ({
      showError,
      showInfo,
      showWarning,
      get cachedPublicSettings() {
        return state.cachedPublicSettings
      },
    }),
  }
})

vi.mock('@/api/subscriptions', () => ({ default: { getMySubscriptions: vi.fn(async () => activeSubscriptionState.items) } }))

vi.mock('@/api/payment', () => ({
  paymentAPI: {
    getCheckoutInfo,
    quoteSubscription,
    legacySubscriptionOptions: vi.fn(async () => ({data:legacyOptionsState.value})),
  },
}))

vi.mock('@/utils/device', () => ({
  isMobileDevice: () => deviceState.mobile,
}))

function checkoutInfoFixture(overrides: Partial<CheckoutInfoResponse> = {}) {
  const wxpayMethod: MethodLimit = {
    daily_limit: 0,
    daily_used: 0,
    daily_remaining: 0,
    single_min: 0,
    single_max: 0,
    fee_rate: 0,
    available: true,
  }
  const data: CheckoutInfoResponse = {
    methods: {
      wxpay: wxpayMethod,
    },
    global_min: 0,
    global_max: 0,
    plans: [],
    balance_disabled: false,
    balance_recharge_multiplier: 1,
    subscription_usd_to_cny_rate: 0,
    recharge_fee_rate: 0,
    help_text: '',
    help_image_url: '',
    stripe_publishable_key: '',
  }

  return {
    data: { ...data, ...overrides },
  }
}

function checkoutInfoWithPlansFixture(options: {
  checkout?: Partial<CheckoutInfoResponse>
  method?: Partial<MethodLimit>
  plan?: Partial<SubscriptionPlan>
  subscriptions?: UserSubscription[]
  query?: Record<string, unknown>
  useFakeClock?: boolean
  mobile?: boolean
} = {}) {
  const base = checkoutInfoFixture(options.checkout).data
  const plan: SubscriptionPlan = {
    id: 7,
    group_id: 3,
    name: 'Starter',
    description: '',
    price: 128,
    original_price: 0,
    validity_days: 30,
    validity_unit: 'day',
    rate_multiplier: 1,
    daily_limit_usd: 45,
    weekly_limit_usd: null,
    monthly_limit_usd: null,
    features: [],
    group_platform: 'openai',
    sort_order: 1,
    for_sale: true,
    group_name: 'OpenAI',
    ...options.plan,
  }

  return {
    data: {
      ...base,
      methods: {
        ...base.methods,
        wxpay: {
          ...base.methods.wxpay,
          ...options.method,
        },
      },
      plans: [plan],
    },
  }
}

function jsapiOrderFixture(resumeToken: string) {
  return {
    order_id: 123,
    amount: 88,
    pay_amount: 88,
    fee_rate: 0,
    expires_at: '2099-01-01T00:10:00.000Z',
    payment_type: 'wxpay',
    out_trade_no: 'sub2_jsapi_123',
    result_type: 'jsapi_ready' as const,
    resume_token: resumeToken,
    jsapi: {
      appId: 'wx123',
      timeStamp: '1712345678',
      nonceStr: 'nonce',
      package: 'prepay_id=wx123',
      signType: 'RSA',
      paySign: 'signed',
    },
  }
}

function oauthOrderFixture() {
  return {
    order_id: 456,
    amount: 128,
    pay_amount: 128,
    fee_rate: 0,
    expires_at: '2099-01-01T00:10:00.000Z',
    payment_type: 'wxpay',
    result_type: 'oauth_required' as const,
    oauth: {
      authorize_url: '/api/v1/auth/oauth/wechat/payment/start?payment_type=wxpay&redirect=%2Fpurchase%3Ffrom%3Dwechat',
      appid: 'wx123',
      scope: 'snsapi_base',
      redirect_url: '/auth/wechat/payment/callback',
    },
  }
}

async function mountSubscriptionConfirm(options: Parameters<typeof checkoutInfoWithPlansFixture>[0] = {}) {
  if (!options.useFakeClock) vi.useRealTimers()
  deviceState.mobile = options.mobile ?? true
  routeState.path = '/purchase'
  routeState.query = {
    tab: 'subscription',
    group: '3',
    ...options.query,
  }
  activeSubscriptionState.items = options.subscriptions ?? []
  routerReplace.mockReset().mockResolvedValue(undefined)
  routerPush.mockReset().mockResolvedValue(undefined)
  routerResolve.mockClear()
  createOrder.mockReset()
  refreshUser.mockReset()
  fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
  showError.mockReset()
  showInfo.mockReset()
  showWarning.mockReset()
  getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoWithPlansFixture(options))
  bridgeInvoke.mockReset()
  window.localStorage.clear()
  ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined

  const wrapper = shallowMount(PaymentView, {
    global: {
      stubs: {
        AppLayout: {
          template: '<div><slot /></div>',
        },
        Teleport: true,
        Transition: false,
      },
    },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

async function mountSubscriptionPlanList(planCount: number) {
  vi.useRealTimers()
  routeState.path = '/purchase'
  routeState.query = { tab: 'subscription' }
  routerReplace.mockReset().mockResolvedValue(undefined)
  routerPush.mockReset().mockResolvedValue(undefined)
  routerResolve.mockClear()
  createOrder.mockReset()
  refreshUser.mockReset()
  fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
  showError.mockReset()
  showInfo.mockReset()
  showWarning.mockReset()
  const basePlan = checkoutInfoWithPlansFixture().data.plans[0]
  const plans = Array.from({ length: planCount }, (_, index) => ({
    ...basePlan,
    id: index + 1,
    name: `Plan ${index + 1}`,
  }))
  getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture({ plans }))
  bridgeInvoke.mockReset()
  window.localStorage.clear()
  ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined

  const wrapper = shallowMount(PaymentView, {
    global: {
      stubs: {
        AppLayout: {
          template: '<div><slot /></div>',
        },
        Teleport: true,
        Transition: false,
      },
    },
  })
  await flushPromises()
  await flushPromises()
  return wrapper
}

describe('PaymentView help text', () => {
  beforeEach(() => {
    vi.useRealTimers()
    routeState.path = '/purchase'
    routeState.query = {}
    createOrder.mockReset()
    window.localStorage.clear()
  })

  async function mountHelp(help_text: string, help_image_url = '') {
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture({ help_text, help_image_url }))
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    return wrapper
  }

  it('renders headings, emphasis, links, and lists in payment help without starting checkout', async () => {
    const wrapper = await mountHelp('## Recharge help\n\n**Read first**\n\n- [Contact support](https://example.com/help)')
    const help = wrapper.get('.markdown-body')
    expect(help.get('h2').text()).toBe('Recharge help')
    expect(help.get('strong').text()).toBe('Read first')
    expect(help.get('li a').attributes('href')).toBe('https://example.com/help')
    expect(createOrder).not.toHaveBeenCalled()
  })

  it('removes scripts, event handlers, and unsafe URLs from rendered help', async () => {
    const wrapper = await mountHelp([
      '<script>alert(1)</script>',
      '<img src="https://example.com/help.png" onerror="alert(1)">',
      '[Unsafe](javascript:alert%281%29)',
      '[Support](https://example.com/help)',
    ].join('\n\n'))
    const help = wrapper.get('.markdown-body')
    expect(help.find('script').exists()).toBe(false)
    expect(help.get('img').attributes('onerror')).toBeUndefined()
    expect(help.findAll('a').map(link => link.attributes('href'))).toEqual([undefined, 'https://example.com/help'])
  })

  it('keeps plain-text soft line breaks and the separate help image preview', async () => {
    const wrapper = await mountHelp('First line\nSecond line', 'https://example.com/help.png')
    const help = wrapper.get('.markdown-body')
    expect(help.get('p').text()).toBe('First line\nSecond line')
    expect(help.find('br').exists()).toBe(false)
    await wrapper.get('img').trigger('click')
    expect(wrapper.findAll('img')).toHaveLength(2)
    expect(wrapper.findAll('img')[1].attributes('src')).toBe('https://example.com/help.png')
  })

  it('keeps image-only help without an empty Markdown container', async () => {
    const wrapper = await mountHelp('', 'https://example.com/help.png')
    expect(wrapper.find('.markdown-body').exists()).toBe(false)
    expect(wrapper.get('img').attributes('src')).toBe('https://example.com/help.png')
  })
})

describe('PaymentView group subscription rates', () => {
  const groupRates = [
    { id: 4, name: 'GPT 稳定', platform: 'openai', usage_panel: 'gpt', subscription_rate_multiplier: 1 },
    { id: 27, name: 'GPT 给力 Pro', platform: 'openai', usage_panel: 'gpt', subscription_rate_multiplier: 1.3 },
  ]

  async function mountPayment(query: Record<string, unknown> = {}) {
    routeState.path = '/purchase'
    routeState.query = query
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture({ group_rates: groupRates }))
    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    return wrapper
  }

  it('shows live group rates only on the subscribe tab', async () => {
    const wrapper = await mountPayment()
    expect(wrapper.findComponent(SubscriptionGroupRates).exists()).toBe(false)

    const subscribeTab = wrapper.findAll('button').find(button => button.text() === 'payment.tabSubscribe')
    expect(subscribeTab).toBeTruthy()
    await subscribeTab!.trigger('click')
    await flushPromises()

    expect(wrapper.findComponent(SubscriptionGroupRates).props('rates')).toEqual(groupRates)
  })

  it('keeps the rate board when opened from ?tab=subscription', async () => {
    const wrapper = await mountPayment({ tab: 'subscription' })
    expect(wrapper.findComponent(SubscriptionGroupRates).props('rates')).toEqual(groupRates)
  })
})

describe('PaymentView subscription plan grid', () => {
  it.each([3, 4, 6])('keeps %i plans on the existing mobile/tablet/desktop grid', async (planCount) => {
    const wrapper = await mountSubscriptionPlanList(planCount)
    const cards = wrapper.findAllComponents(SubscriptionPlanCard)

    expect(cards).toHaveLength(planCount)
    expect([...(cards[0].element.parentElement?.classList ?? [])]).toEqual(expect.arrayContaining([
      'grid',
      'grid-cols-1',
      'sm:grid-cols-2',
      'lg:grid-cols-3',
    ]))
  })
})

describe('PaymentView recharge rate preview', () => {
  it('uses the selected payment method currency in both locale templates', async () => {
    translate.mockClear()
    routeState.path = '/purchase'
    routeState.query = {}
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture({
      balance_recharge_multiplier: 0.5,
      methods: {
        stripe: {
          ...checkoutInfoFixture().data.methods.wxpay,
          currency: 'USD',
        },
      },
    }))

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    wrapper.getComponent(AmountInput).vm.$emit('update:modelValue', 10)
    await flushPromises()

    expect(translate).toHaveBeenCalledWith('payment.rechargeRatePreview', {
      currency: 'USD',
      usd: '0.50',
    })
    expect(en.payment.rechargeRatePreview).toBe('Current rate: 1 {currency} = {usd} USD')
    expect(zh.payment.rechargeRatePreview).toBe('当前倍率：1 {currency} = {usd} USD')
  })
})

describe('PaymentView subscription confirmation amounts', () => {
  it('shows converted CNY pay amount using the subscription rate, not the balance multiplier', async () => {
    const wrapper = await mountSubscriptionConfirm({
      checkout: {
        balance_recharge_multiplier: 0.14,
        subscription_usd_to_cny_rate: 7.15,
      },
      method: {
        currency: 'CNY',
      },
      plan: {
        price: 9.99,
        original_price: 12.99,
      },
    })

    const text = wrapper.text()
    const convertedPrice = formatPaymentAmount(71.43, 'CNY')
    const convertedOriginalPrice = formatPaymentAmount(92.88, 'CNY')

    expect(text).toContain(convertedPrice)
    expect(text).toContain(convertedOriginalPrice)
    expect(text).not.toContain(formatPaymentAmount(9.99, 'CNY'))
    // 换算必须使用订阅汇率（×7.15），而不是余额倍率（÷0.14 = 71.36）
    expect(text).not.toContain(formatPaymentAmount(71.36, 'CNY'))
    expect(wrapper.findAll('button').some(button => button.text().includes(convertedPrice))).toBe(true)
  })

  it('keeps plan price when the subscription rate is not configured or payment currency is not CNY', async () => {
    // opt-in 回归锁：即使余额倍率已配置，未配置订阅汇率时 CNY 订阅仍按 price 直付
    const cnyWrapper = await mountSubscriptionConfirm({
      checkout: {
        balance_recharge_multiplier: 0.14,
        subscription_usd_to_cny_rate: 0,
      },
      method: {
        currency: 'CNY',
      },
      plan: {
        price: 7.99,
      },
    })

    expect(cnyWrapper.text()).toContain(formatPaymentAmount(7.99, 'CNY'))
    expect(cnyWrapper.text()).not.toContain(formatPaymentAmount(57.07, 'CNY'))
    expect(cnyWrapper.text()).not.toContain(formatPaymentAmount(57.13, 'CNY'))

    const usdWrapper = await mountSubscriptionConfirm({
      checkout: {
        subscription_usd_to_cny_rate: 7.15,
      },
      method: {
        currency: 'USD',
      },
      plan: {
        price: 7.99,
        original_price: 9.99,
      },
    })

    expect(usdWrapper.text()).toContain(formatPaymentAmount(7.99, 'USD'))
    expect(usdWrapper.text()).toContain(formatPaymentAmount(9.99, 'USD'))
  })

  it('adds fee rate after CNY rate conversion to match backend pay_amount', async () => {
    const wrapper = await mountSubscriptionConfirm({
      checkout: {
        subscription_usd_to_cny_rate: 7.15,
        recharge_fee_rate: 2.5,
      },
      method: {
        currency: 'CNY',
      },
      plan: {
        price: 9.99,
      },
    })

    const text = wrapper.text()
    const convertedPrice = formatPaymentAmount(71.43, 'CNY')
    const fee = formatPaymentAmount(1.79, 'CNY')
    const total = formatPaymentAmount(73.22, 'CNY')

    expect(text).toContain(convertedPrice)
    expect(text).toContain(fee)
    expect(text).toContain(total)
    expect(wrapper.findAll('button').some(button => button.text().includes(total))).toBe(true)
  })
})

describe('PaymentView payment recovery', () => {
  beforeEach(() => {
    vi.useRealTimers()
    routeState.path = '/purchase'
    routeState.query = {}
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    routerResolve.mockClear()
    createOrder.mockReset()
    refreshUser.mockReset()
    fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    bridgeInvoke.mockReset()
    window.localStorage.clear()
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined
  })

  it('restores a custom EasyPay method as the selected payment method', async () => {
    getCheckoutInfo.mockResolvedValue(checkoutInfoFixture({
      methods: {
        wxpay: checkoutInfoFixture().data.methods.wxpay,
        ldc: {
          daily_limit: 0,
          daily_used: 0,
          daily_remaining: 0,
          single_min: 0,
          single_max: 0,
          fee_rate: 0,
          available: true,
          display_name: 'LDC Pay',
        },
      },
    }))
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      orderId: 888,
      amount: 66,
      qrCode: 'ldc-qr',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'ldc',
      payUrl: 'https://pay.example.com/ldc',
      outTradeNo: 'sub2_ldc_888',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 66,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: '',
      createdAt: Date.now(),
    }))

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          AppLayout: {
            template: '<div><slot /></div>',
          },
          PaymentStatusPanel: {
            template: '<button data-test="payment-done" @click="$emit(\'done\')" />',
          },
          PaymentMethodSelector: {
            props: ['selected'],
            template: '<div data-test="method-selector">{{ selected }}</div>',
          },
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()
    await wrapper.find('[data-test="payment-done"]').trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="method-selector"]').text()).toBe('ldc')
  })
})

describe('PaymentView WeChat JSAPI flow', () => {
  beforeEach(() => {
    routeState.path = '/purchase'
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-token-123',
    }
    routerReplace.mockReset().mockResolvedValue(undefined)
    routerPush.mockReset().mockResolvedValue(undefined)
    routerResolve.mockClear()
    createOrder.mockReset()
    refreshUser.mockReset()
    fetchActiveSubscriptions.mockReset().mockResolvedValue(undefined)
    showError.mockReset()
    showInfo.mockReset()
    showWarning.mockReset()
    getCheckoutInfo.mockReset().mockResolvedValue(checkoutInfoFixture())
    bridgeInvoke.mockReset()
    window.localStorage.clear()
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = {
      invoke: bridgeInvoke,
    }
  })

  it('resets payment state and redirects to /payment/result after JSAPI reports success', async () => {
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-123'))
    bridgeInvoke.mockImplementation((_action, _payload, callback) => {
      callback({ err_msg: 'get_brand_wcpay_request:ok' })
    })

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(routerReplace).toHaveBeenCalledWith({ path: '/purchase', query: {} })
    expect(routerPush).toHaveBeenCalledWith({
      path: '/payment/result',
      query: {
        order_id: '123',
        out_trade_no: 'sub2_jsapi_123',
        resume_token: 'resume-token-123',
      },
    })
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  it('resets payment state when JSAPI reports cancellation', async () => {
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-cancel'))
    bridgeInvoke.mockImplementation((_action, _payload, callback) => {
      callback({ err_msg: 'get_brand_wcpay_request:cancel' })
    })

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(showInfo).toHaveBeenCalledWith('payment.qr.cancelled')
    expect(routerPush).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  it('clears stale recovery state when JSAPI never becomes available', async () => {
    vi.useFakeTimers()
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-token-missing-bridge'))
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = undefined

    const wrapper = shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })

    await flushPromises()
    await vi.advanceTimersByTimeAsync(4000)
    await flushPromises()
    await flushPromises()

    expect(showError).toHaveBeenCalledWith(
      'payment.errors.wechatJsapiUnavailable payment.errors.wechatOpenInWeChatHint',
    )
    expect(routerPush).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
    expect(wrapper.html()).not.toContain('payment-status-panel-stub')
  })

  it('clears a stale recovery snapshot before handling wechat resume callback params', async () => {
    createOrder.mockRejectedValueOnce(new Error('resume failed'))
    window.localStorage.setItem(PAYMENT_RECOVERY_STORAGE_KEY, JSON.stringify({
      orderId: 999,
      amount: 66,
      qrCode: 'stale-qr',
      expiresAt: '2099-01-01T00:10:00.000Z',
      paymentType: 'alipay',
      payUrl: 'https://pay.example.com/stale',
      outTradeNo: 'stale-out-trade-no',
      clientSecret: '',
      intentId: '',
      currency: '',
      countryCode: '',
      paymentEnv: '',
      payAmount: 66,
      orderType: 'balance',
      paymentMode: 'popup',
      resumeToken: '',
      createdAt: Date.UTC(2099, 0, 1, 0, 0, 0),
    }))

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({
      wechat_resume_token: 'resume-token-123',
    }))
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toBeNull()
  })

  it('keeps subscription resume context for token-only WeChat callbacks', async () => {
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-subscription-7',
      payment_type: 'wxpay_direct',
      order_type: 'subscription',
      plan_id: '7',
    }
    getCheckoutInfo.mockResolvedValue(checkoutInfoWithPlansFixture())
    createOrder.mockResolvedValue(oauthOrderFixture())

    const originalLocation = window.location
    const locationState = {
      href: 'http://localhost/purchase',
      origin: 'http://localhost',
    }
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: locationState,
    })

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(routerReplace).toHaveBeenCalledWith({ path: '/purchase', query: {} })
    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({
      payment_type: 'wxpay',
      order_type: 'subscription',
      plan_id: 7,
      wechat_resume_token: 'resume-subscription-7',
    }))
    expect(locationState.href).toContain('/api/v1/auth/oauth/wechat/payment/start?')
    expect(new URL(locationState.href, 'http://localhost').searchParams.get('redirect')).toBe(
      '/purchase?from=wechat&payment_type=wxpay&order_type=subscription&plan_id=7&operation=purchase&units=1',
    )

    Object.defineProperty(window, 'location', {
      configurable: true,
      value: originalLocation,
    })
  })

  it('falls back to QR flow when mobile WeChat payment is unavailable', async () => {
    routeState.query = {
      wechat_resume: '1',
      wechat_resume_token: 'resume-token-h5',
      payment_type: 'wxpay_direct',
    }
    createOrder
      .mockRejectedValueOnce({ reason: 'WECHAT_H5_NOT_AUTHORIZED' })
      .mockResolvedValueOnce({
        order_id: 778,
        amount: 88,
        pay_amount: 88,
        fee_rate: 0,
        expires_at: '2099-01-01T00:10:00.000Z',
        payment_type: 'wxpay',
        qr_code: 'weixin://wxpay/bizpayurl?pr=fallback-native',
        out_trade_no: 'sub2_qr_778',
      })

    shallowMount(PaymentView, {
      global: {
        stubs: {
          Teleport: true,
          Transition: false,
        },
      },
    })
    await flushPromises()
    await flushPromises()

    expect(createOrder).toHaveBeenNthCalledWith(1, expect.objectContaining({
      payment_type: 'wxpay',
      is_mobile: true,
      wechat_resume_token: 'resume-token-h5',
    }))
    expect(createOrder).toHaveBeenNthCalledWith(2, expect.objectContaining({
      payment_type: 'wxpay',
      is_mobile: false,
      payment_source: 'hosted_redirect',
    }))
    expect(showWarning).toHaveBeenCalledWith('payment.errors.mobilePaymentFallbackToQr')
    expect(showError).not.toHaveBeenCalled()
    expect(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)).toContain('weixin://wxpay/bizpayurl?pr=fallback-native')
  })
})

describe('PaymentView subscription feature flag', () => {
  afterEach(() => {
    appStoreState.setPublicSettings(undefined)
  })

  function tabLabels(wrapper: Awaited<ReturnType<typeof mountSubscriptionPlanList>>) {
    return wrapper
      .findAll('button')
      .map((button) => button.text())
      .filter((text) => text === 'payment.tabTopUp' || text === 'payment.tabSubscribe')
  }

  it('keeps the top-up / subscribe switcher when subscription_enabled is absent (opt-out default)', async () => {
    const wrapper = await mountSubscriptionPlanList(2)

    expect(tabLabels(wrapper)).toEqual(['payment.tabTopUp', 'payment.tabSubscribe'])
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(2)
  })

  it('drops the subscribe tab, hides the switcher and ignores ?tab=subscription when subscriptions are disabled', async () => {
    appStoreState.setPublicSettings({ subscription_enabled: false })
    const wrapper = await mountSubscriptionPlanList(2)

    expect(tabLabels(wrapper)).toEqual([])
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(0)
    expect(wrapper.text()).toContain('payment.rechargeAccount')
  })

  it('shows an unavailable notice instead of a doomed top-up form when balance recharge is disabled too', async () => {
    appStoreState.setPublicSettings({ subscription_enabled: false })
    const wrapper = await mountSubscriptionConfirm({ checkout: { balance_disabled: true } })

    expect(tabLabels(wrapper)).toEqual([])
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(0)
    expect(wrapper.text()).not.toContain('payment.confirmSubscription')
    expect(wrapper.text()).not.toContain('payment.rechargeAccount')
    expect(wrapper.text()).toContain('payment.billingUnavailable')
    wrapper.unmount()
  })

  it('falls back from the subscribe tab to top-up when the flag flips off after mount', async () => {
    const wrapper = await mountSubscriptionPlanList(2)
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(2)

    appStoreState.setPublicSettings({ subscription_enabled: false })
    await flushPromises()

    expect(tabLabels(wrapper)).toEqual([])
    expect(wrapper.findAllComponents(SubscriptionPlanCard)).toHaveLength(0)
    expect(wrapper.text()).toContain('payment.rechargeAccount')
    wrapper.unmount()
  })

  it('enters the subscribe tab when a subscription-only site turns subscriptions back on', async () => {
    appStoreState.setPublicSettings({ subscription_enabled: false })
    const wrapper = await mountSubscriptionConfirm({ checkout: { balance_disabled: true } })
    expect(wrapper.text()).toContain('payment.billingUnavailable')

    appStoreState.setPublicSettings({ subscription_enabled: true })
    await flushPromises()

    expect(wrapper.text()).not.toContain('payment.billingUnavailable')
    expect(wrapper.text()).not.toContain('payment.rechargeAccount')
    expect(wrapper.findAllComponents(SubscriptionPlanCard).length).toBeGreaterThan(0)
    wrapper.unmount()
  })
})

// Regression: the user must see a quote before creating a payment order.
describe('Subscription audit purchase preview', () => {
  it('loads the projected benefits before the user commits to payment', async () => {
    quoteSubscription.mockReset().mockResolvedValue({ data: {
      quote_id: 'quote-test', expires_at: '2099-01-01T00:00:00Z',
      order_amount: 128,
      projected: { active_lot_count: 1, daily_limit_usd: 45, expires_at: '2099-02-01T00:00:00Z' },
    } })
    const wrapper = await mountSubscriptionConfirm({ plan: { daily_limit_usd: 45 } })
    try {
      expect(createOrder).not.toHaveBeenCalled()
      expect(quoteSubscription).toHaveBeenCalledWith(expect.objectContaining({ plan_id: 7 }))
    } finally {
      wrapper.unmount()
    }
  })
  it('uses the accepted quote price in the payment confirmation', async () => {
    quoteSubscription.mockReset().mockResolvedValue({ data: { quote_id: 'quote-test', expires_at: '2099-01-01T00:00:00Z', order_amount: 20, can_renew_lots: 0, projected: { active_lot_count: 1, daily_limit_usd: 45, weekly_limit_usd: null, monthly_limit_usd: null, expires_at: '2099-02-01T00:00:00Z' } } })
    const wrapper = await mountSubscriptionConfirm({ plan: { price: 10 }, method: { currency: 'USD' } })
    expect(wrapper.findAll('button').some(button => button.text().includes(formatPaymentAmount(20, 'USD')))).toBe(true)
    wrapper.unmount()
  })
  it('disables payment while the quote fails', async () => {
    quoteSubscription.mockReset().mockRejectedValue(new Error('quote unavailable'))
    const wrapper = await mountSubscriptionConfirm()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    const pay = wrapper.findAll('button').find(button => button.text().includes('payment.createOrder'))
    expect(pay?.attributes('disabled')).toBeDefined()
    expect(createOrder).not.toHaveBeenCalled()
    wrapper.unmount()
  })

})

describe('Subscription V2 purchase safety', () => {
  const quote = (amount = 12, expires = '2099-01-01T00:00:00Z') => ({ data: { quote_id: 'quote-v2', expires_at: expires, order_amount: amount, projected: { active_lot_count: 1, daily_limit_usd: 45, remaining_usd: 45, expires_at: '2099-02-01T00:00:00Z' } } })
  afterEach(() => { activeSubscriptionState.items = [] })
  it('sends the accepted quote when opening a new subscription through its plan link', async () => {
    quoteSubscription.mockReset().mockResolvedValue(quote())
    const wrapper = await mountSubscriptionConfirm({ query: { plan: '7', operation: 'purchase' } })
    createOrder.mockResolvedValue({ order_id: 123, amount: 12, pay_amount: 12, expires_at: '2099-01-01', qr_code: 'test', fee_rate: 0 })
    const pay = wrapper.findAll('button').find(button => button.text().includes('payment.createOrder'))!
    expect(pay.attributes('disabled')).toBeUndefined()
    await pay.trigger('click')
    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({ quote_id: 'quote-v2', operation: 'purchase', units: 1, plan_id: 7, amount: 12 }))
    expect(createOrder.mock.calls[0][0]).not.toHaveProperty('subscription_quantity')
    wrapper.unmount()
  })
  it('blocks an expired quote until a refreshed quote is shown', async () => {
    quoteSubscription.mockReset().mockResolvedValue(quote(12, '2000-01-01T00:00:00Z'))
    const wrapper = await mountSubscriptionConfirm()
    const pay = () => wrapper.findAll('button').find(button => button.text().includes('payment.createOrder'))!
    expect(pay().attributes('disabled')).toBeDefined()
    quoteSubscription.mockResolvedValue(quote(13))
    await wrapper.findAll('button').find(button => button.text().includes('subscriptionRights.retry'))!.trigger('click')
    await flushPromises()
    expect(pay().attributes('disabled')).toBeUndefined()
    expect(pay().text()).toContain('13')
    wrapper.unmount()
  })
  it('ignores an older quote response after the quantity changes', async () => {
    let resolveOld!: (value: ReturnType<typeof quote>) => void
    quoteSubscription.mockReset().mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve })).mockResolvedValue(quote(24))
    const wrapper = await mountSubscriptionConfirm()
    await wrapper.find('select').setValue('2')
    await flushPromises()
    resolveOld(quote(12))
    await flushPromises()
    expect(wrapper.findAll('button').find(button => button.text().includes('payment.createOrder'))!.text()).toContain('24')
    expect(quoteSubscription).toHaveBeenLastCalledWith(expect.objectContaining({ operation: 'purchase', units: 2 }))
    wrapper.unmount()
  })
  it('renews every current unit using separate period count and can switch to stack', async () => {
    quoteSubscription.mockReset().mockResolvedValue(quote())
    const contract = { mode: 'v2', kind: 'month', unit_daily_usd: 45, quantity: 2, period_days: 30, term_id: 'term-1', revision: 1, plan_id: 7, expires_at: '2099-01-01T00:00:00Z' }
    const wrapper = await mountSubscriptionConfirm({ subscriptions: [{ id: 9, plan_id: 7, status: 'active', expires_at: contract.expires_at, contract } as UserSubscription] })
    await wrapper.find('select').setValue('3')
    await flushPromises()
    expect(quoteSubscription).toHaveBeenLastCalledWith(expect.objectContaining({ operation: 'renew', periods: 3, units: undefined }))
    await wrapper.findAll('button').find(button => button.text() === 'subscriptionRights.stack')!.trigger('click')
    await flushPromises()
    expect(quoteSubscription).toHaveBeenLastCalledWith(expect.objectContaining({ operation: 'stack', units: 1, periods: undefined }))
    wrapper.unmount()
  })
  it('labels the shared daily quota as total rather than per-unit quota', async () => {
    const subscription = { id: 9, status: 'active', expires_at: '2099-01-01', contract: { mode: 'v2', kind: 'month', unit_daily_usd: 45, quantity: 2, period_days: 30, plan_id: 7 }, quota_summary: { daily_limit_usd: 90 } } as UserSubscription
    const wrapper = await mountSubscriptionConfirm({ subscriptions: [subscription], mobile: false, query: { tab: 'subscription', group: '' } })
    expect(wrapper.get('[data-testid="current-subscription-daily"]').text()).toBe('subscriptionRights.daily $90')
    wrapper.unmount()
  })
  it('does not enter checkout for a legacy compatibility contract', async () => {
    quoteSubscription.mockReset().mockResolvedValue(quote())
    const wrapper = await mountSubscriptionConfirm({ subscriptions: [{ id: 9, status: 'active', expires_at: '2099-01-01', contract: { mode: 'legacy_daily' } } as UserSubscription] })
    expect(quoteSubscription).not.toHaveBeenCalled()
    expect(wrapper.findAll('button').some(button => button.text().includes('payment.createOrder'))).toBe(false)
    expect(wrapper.text()).toContain('subscriptionRights.compatibilityHint')
    expect(showInfo).toHaveBeenCalledWith('subscriptionRights.compatibilityHint')
    showInfo.mockClear()
    const card = wrapper.findAllComponents(SubscriptionPlanCard)[0]
    card.vm.$emit('select', card.props('plan'))
    await flushPromises()
    expect(showInfo).toHaveBeenCalledWith('subscriptionRights.compatibilityHint')
    expect(quoteSubscription).not.toHaveBeenCalled()
    expect(createOrder).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})

describe('V2 subscription payment recovery', () => {
  const validQuote = { data: { quote_id: 'quote-jsapi', expires_at: '2099-01-01T00:00:00Z', order_amount: 1.4, projected: { active_lot_count: 1, daily_limit_usd: 45, expires_at: '2099-02-01T00:00:00Z' } } }
  afterEach(() => { activeSubscriptionState.items = [] })

  it.each(['get_brand_wcpay_request:fail', 'get_brand_wcpay_request:cancel', 'throw'])('preserves the one pending subscription order when JSAPI reports %s', async (result) => {
    quoteSubscription.mockReset().mockResolvedValue(validQuote)
    const wrapper = await mountSubscriptionConfirm()
    createOrder.mockResolvedValue(jsapiOrderFixture('resume-existing'))
    bridgeInvoke.mockImplementation((_action, _payload, callback) => {
      if (result === 'throw') throw new Error('wechat_jsapi_unavailable')
      callback({ err_msg: result })
    })
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = { invoke: bridgeInvoke }
    await wrapper.findAll('button').find(button => button.text().includes('payment.createOrder'))!.trigger('click')
    await flushPromises()
    expect(createOrder).toHaveBeenCalledTimes(1)
    expect(JSON.parse(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)!)).toMatchObject({ orderId: 123, orderType: 'subscription', resumeToken: 'resume-existing' })
    expect(wrapper.html()).toContain('payment-status-panel-stub')
    expect(wrapper.text()).toContain('subscriptionRights.existingPaymentHint')
    expect(routerPush).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('preserves an existing subscription if post-create navigation fails', async () => {
    quoteSubscription.mockReset().mockResolvedValue(validQuote)
    const wrapper = await mountSubscriptionConfirm()
    createOrder.mockResolvedValue({ ...jsapiOrderFixture(), client_secret: 'cs-test' })
    routerResolve.mockImplementationOnce(() => { throw new Error('PAYMENT_GATEWAY_ERROR') })
    await wrapper.findAll('button').find(button => button.text().includes('payment.createOrder'))!.trigger('click')
    await flushPromises()
    expect(createOrder).toHaveBeenCalledTimes(1)
    expect(JSON.parse(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)!)).toMatchObject({ orderId: 123, orderType: 'subscription' })
    expect(wrapper.text()).toContain('subscriptionRights.existingPaymentHint')
    wrapper.unmount()
  })

  it('keeps a channel capped at 1.47 available for a 1.40 quote plus 5% fee', async () => {
    quoteSubscription.mockReset().mockResolvedValue(validQuote)
    const wrapper = await mountSubscriptionConfirm({ method: { currency: 'USD', single_max: 1.47 }, checkout: { recharge_fee_rate: 5 } })
    const pay = wrapper.findAll('button').find(button => button.text().includes('payment.createOrder'))!
    expect(pay.attributes('disabled')).toBeUndefined()
    expect(pay.text()).toContain('1.47')
    expect(wrapper.text()).not.toContain('1.48')
    wrapper.unmount()
  })

  it('blocks a suspended contract even when the active-only header store is empty', async () => {
    quoteSubscription.mockReset().mockResolvedValue(validQuote)
    const wrapper = await mountSubscriptionConfirm({ subscriptions: [{ id: 10, status: 'suspended', expires_at: '2099-01-01', contract: { mode: 'v2' } } as UserSubscription] })
    expect(quoteSubscription).not.toHaveBeenCalled()
    expect(wrapper.findAll('button').some(button => button.text().includes('payment.createOrder'))).toBe(false)
    expect(wrapper.findAllComponents(SubscriptionPlanCard)[0].props('activeSubscriptions')[0].status).toBe('suspended')
    wrapper.unmount()
  })
})

describe('V2 checkout natural expiry', () => {
  it('refreshes full eligibility at expiry and changes the selected renewal to purchase', async () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-09-21T12:00:00Z'))
    quoteSubscription.mockReset().mockResolvedValue({ data: { quote_id: 'quote-expiry', expires_at: '2026-09-21T12:05:00Z', order_amount: 10, projected: { active_lot_count: 1, daily_limit_usd: 45, expires_at: '2026-10-21T12:00:00Z' } } })
    const subscription = { id: 9, status: 'active', expires_at: '2026-09-21T12:00:02Z', contract: { mode: 'v2', kind: 'month', period_days: 30, unit_daily_usd: 45, quantity: 1, plan_id: 7, expires_at: '2026-09-21T12:00:02Z' } } as UserSubscription
    const wrapper = await mountSubscriptionConfirm({ useFakeClock: true, subscriptions: [subscription] })
    try {
      expect(quoteSubscription).toHaveBeenLastCalledWith(expect.objectContaining({ operation: 'renew' }))
      const callsBefore = vi.mocked(subscriptionsAPI.getMySubscriptions).mock.calls.length
      activeSubscriptionState.items = []
      await vi.advanceTimersByTimeAsync(2_101)
      await flushPromises()
      expect(vi.mocked(subscriptionsAPI.getMySubscriptions).mock.calls.length).toBeGreaterThan(callsBefore)
      expect(quoteSubscription).toHaveBeenLastCalledWith(expect.objectContaining({ operation: 'purchase', units: 1, periods: undefined }))
      expect(wrapper.findAll('button').some(button => button.text() === 'subscriptionRights.purchase')).toBe(true)
      wrapper.unmount()
      const callsAfter = vi.mocked(subscriptionsAPI.getMySubscriptions).mock.calls.length
      await vi.advanceTimersByTimeAsync(300_001)
      expect(vi.mocked(subscriptionsAPI.getMySubscriptions).mock.calls.length).toBe(callsAfter)
    } finally {
      wrapper.unmount()
      activeSubscriptionState.items = []
      vi.useRealTimers()
    }
  })
})

describe('V2 five-plan operation matrix', () => {
  const plans = [
    { id: 71, validity_days: 7, daily_limit_usd: 90 },
    { id: 72, validity_days: 7, daily_limit_usd: 180 },
    { id: 73, validity_days: 30, daily_limit_usd: 45 },
    { id: 74, validity_days: 30, daily_limit_usd: 90 },
    { id: 75, validity_days: 30, daily_limit_usd: 180 },
  ]
  const current = (plan: typeof plans[number], count: number): UserSubscription => ({ id: 99, status: 'active', plan_id: plan.id, expires_at: '2099-01-01', contract: { mode: 'v2', kind: plan.validity_days === 7 ? 'week' : 'month', period_days: plan.validity_days, quantity: count, unit_daily_usd: plan.daily_limit_usd, plan_id: plan.id, expires_at: '2099-01-01' } }) as UserSubscription
  const scenarios = plans.flatMap(plan => [
    ...[1, 3, 10].map(count => ({ plan, operation: 'purchase', count, quantity: 0, subscriptions: [] as UserSubscription[] })),
    ...[1, 3].flatMap(quantity => [1, 10].map(count => ({ plan, operation: 'stack', count, quantity, subscriptions: [current(plan, quantity)] }))),
    ...[1, 3].flatMap(quantity => [1, 4, 10].map(count => ({ plan, operation: 'renew', count, quantity, subscriptions: [current(plan, quantity)] }))),
  ])
  afterEach(() => { activeSubscriptionState.items = [] })
  it.each(scenarios)('$plan.validity_days-day $plan.daily_limit_usd tier $operation with $quantity current units and count $count uses the server quote', async ({ plan, operation, count, subscriptions }) => {
    quoteSubscription.mockReset().mockResolvedValue({ data: { quote_id: `quote-${plan.id}-${operation}-${count}`, expires_at: '2099-01-01', order_amount: 23.45, projected: { active_lot_count: 3, daily_limit_usd: 270, remaining_usd: 269, expires_at: '2099-02-01' } } })
    const wrapper = await mountSubscriptionConfirm({ plan, subscriptions, query: { plan: String(plan.id), operation } })
    await wrapper.get('select').setValue(String(count))
    await flushPromises()
    expect(quoteSubscription).toHaveBeenLastCalledWith({ plan_id: plan.id, operation, units: operation === 'renew' ? undefined : count, periods: operation === 'renew' ? count : undefined })
    createOrder.mockResolvedValue({ order_id: 99, amount: 23.45, pay_amount: 23.45, fee_rate: 0, expires_at: '2099-01-01', qr_code: 'test' })
    const pay = wrapper.findAll('button').find(button => button.text().includes('payment.createOrder'))!
    expect(pay.text()).toContain('23.45')
    await pay.trigger('click')
    expect(createOrder).toHaveBeenCalledTimes(1)
    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({ plan_id: plan.id, operation, quote_id: `quote-${plan.id}-${operation}-${count}`, amount: 23.45, ...(operation === 'renew' ? { periods: count } : { units: count }) }))
    expect(createOrder.mock.calls[0][0]).not.toHaveProperty(operation === 'renew' ? 'units' : 'periods')
    wrapper.unmount()
  })

  const upgrades = [[0, 1], [2, 3], [2, 4], [3, 4]].flatMap(([from, to]) => [1, 3].map(quantity => ({ from: plans[from], to: plans[to], quantity })))
  it.each(upgrades)('upgrades $from.daily_limit_usd to $to.daily_limit_usd for all $quantity units without a quantity selector', async ({ from, to, quantity }) => {
    quoteSubscription.mockReset().mockResolvedValue({ data: { quote_id: 'upgrade-quote', expires_at: '2099-01-01', order_amount: 17.13, billable_days: 45, projected: { active_lot_count: quantity, daily_limit_usd: to.daily_limit_usd * quantity, expires_at: '2099-02-01' } } })
    const wrapper = await mountSubscriptionConfirm({ plan: to, subscriptions: [current(from, quantity)], query: { plan: String(to.id), operation: 'upgrade' } })
    expect(wrapper.find('select').exists()).toBe(false)
    expect(wrapper.text()).toContain('subscriptionRights.billableDays')
    expect(quoteSubscription).toHaveBeenLastCalledWith({ plan_id: to.id, operation: 'upgrade', units: undefined, periods: undefined })
    createOrder.mockResolvedValue({ order_id: 99, amount: 17.13, pay_amount: 17.13, fee_rate: 0, expires_at: '2099-01-01', qr_code: 'test' })
    await wrapper.findAll('button').find(button => button.text().includes('payment.createOrder'))!.trigger('click')
    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({ operation: 'upgrade', quote_id: 'upgrade-quote', amount: 17.13 }))
    expect(createOrder.mock.calls[0][0]).not.toHaveProperty('units')
    expect(createOrder.mock.calls[0][0]).not.toHaveProperty('periods')
    wrapper.unmount()
  })
})

describe('V2 live quote and submission transitions', () => {
  const quote = (id: string, amount = 10) => ({ data: { quote_id: id, expires_at: '2099-01-01', order_amount: amount, projected: { active_lot_count: 1, daily_limit_usd: 45, expires_at: '2099-02-01' } } })
  afterEach(() => { activeSubscriptionState.items = []; vi.mocked(subscriptionsAPI.getMySubscriptions).mockImplementation(async () => activeSubscriptionState.items) })
  it('locks one submitted snapshot against rapid double clicks and a concurrent quote refresh', async () => {
    quoteSubscription.mockReset().mockResolvedValue(quote('accepted', 12))
    const wrapper = await mountSubscriptionConfirm()
    let resolveOrder!: (value: ReturnType<typeof jsapiOrderFixture>) => void
    createOrder.mockImplementation(() => new Promise(resolve => { resolveOrder = resolve }))
    const pay = wrapper.findAll('button').find(button => button.text().includes('payment.createOrder'))!
    await pay.trigger('click')
    await pay.trigger('click')
    expect(createOrder).toHaveBeenCalledTimes(1)
    expect(wrapper.get('select').attributes('disabled')).toBeDefined()
    expect(wrapper.findAll('button').find(button => button.text() === 'subscriptionRights.purchase')!.attributes('disabled')).toBeDefined()
    quoteSubscription.mockResolvedValue(quote('new-price', 30))
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(createOrder.mock.calls[0][0]).toMatchObject({ amount: 12, quote_id: 'accepted', units: 1 })
    resolveOrder({ ...jsapiOrderFixture(), result_type: 'order_created', qr_code: 'test', jsapi: undefined } as unknown as ReturnType<typeof jsapiOrderFixture>)
    await flushPromises()
    expect(createOrder).toHaveBeenCalledTimes(1)
    wrapper.unmount()
  })
  it('disables submission immediately when a pending quote fails or becomes unavailable', async () => {
    let rejectQuote!: (reason: Error) => void
    quoteSubscription.mockReset().mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectQuote = reject }))
    const wrapper = await mountSubscriptionConfirm()
    const pay = () => wrapper.findAll('button').find(button => button.text().includes('payment.createOrder'))!
    expect(pay().attributes('disabled')).toBeDefined()
    rejectQuote(new Error('network offline'))
    await flushPromises()
    expect(pay().attributes('disabled')).toBeDefined()
    expect(wrapper.get('[role="alert"]').text()).toContain('network offline')
    expect(createOrder).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('invalidates an accepted quote when the server reports price or contract changes', async () => {
    quoteSubscription.mockReset().mockResolvedValue(quote('stale'))
    const wrapper = await mountSubscriptionConfirm()
    createOrder.mockRejectedValue({ reason: 'SUBSCRIPTION_QUOTE_CHANGED', message: 'review new quote' })
    await wrapper.findAll('button').find(button => button.text().includes('payment.createOrder'))!.trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toContain('review new quote')
    expect(wrapper.findAll('button').find(button => button.text().includes('payment.createOrder'))!.attributes('disabled')).toBeDefined()
    quoteSubscription.mockResolvedValue(quote('reviewed', 20))
    await wrapper.findAll('button').find(button => button.text().includes('subscriptionRights.retry'))!.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('button').find(button => button.text().includes('payment.createOrder'))!.text()).toContain('20')
    wrapper.unmount()
  })
  it('does not restore stale eligibility when an older refresh resolves after suspension', async () => {
    const visible = vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
    quoteSubscription.mockReset().mockResolvedValue(quote('current'))
    const wrapper = await mountSubscriptionConfirm()
    let resolveOld!: (subscriptions: UserSubscription[]) => void
    vi.mocked(subscriptionsAPI.getMySubscriptions).mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve })).mockResolvedValueOnce([{ id: 99, status: 'suspended', expires_at: '2099-01-01' } as UserSubscription])
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    document.dispatchEvent(new Event('visibilitychange'))
    await flushPromises()
    expect(wrapper.text()).toContain('subscriptionRights.suspendedHint')
    resolveOld([])
    await flushPromises()
    visible.mockRestore()
    expect(wrapper.text()).toContain('subscriptionRights.suspendedHint')
    expect(wrapper.findAll('button').find(button => button.text().includes('payment.createOrder'))!.attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })
})

describe('V2 checkout provider launch integration', () => {
  const cases = [
    { name: 'Alipay desktop QR', method: 'alipay', mobile: false, result: { qr_code: 'alipay-qr' }, expect: 'panel' },
    { name: 'Alipay mobile QR', method: 'alipay', mobile: true, result: { qr_code: 'alipay-qr' }, expect: 'panel' },
    { name: 'WeChat desktop QR', method: 'wxpay', mobile: false, result: { qr_code: 'wxpay-qr' }, expect: 'panel' },
    { name: 'WeChat mobile QR', method: 'wxpay', mobile: true, result: { qr_code: 'wxpay-qr' }, expect: 'panel' },
    { name: 'Alipay desktop redirect', method: 'alipay', mobile: false, result: { pay_url: 'https://pay.example/42', payment_mode: 'redirect' }, expect: 'popup' },
    { name: 'Alipay mobile redirect', method: 'alipay', mobile: true, result: { pay_url: 'https://pay.example/42' }, expect: 'redirect' },
    { name: 'Stripe desktop Alipay', method: 'alipay', mobile: false, result: { client_secret: 'cs-42' }, expect: 'popup' },
    { name: 'Stripe mobile card', method: 'stripe', mobile: true, result: { client_secret: 'cs-42' }, expect: 'stripe' },
    { name: 'Stripe desktop card', method: 'stripe', mobile: false, result: { client_secret: 'cs-42' }, expect: 'stripe' },
    { name: 'Airwallex desktop', method: 'airwallex', mobile: false, result: { client_secret: 'secret-42', intent_id: 'intent-42' }, expect: 'airwallex' },
    { name: 'Airwallex mobile', method: 'airwallex', mobile: true, result: { client_secret: 'secret-42', intent_id: 'intent-42' }, expect: 'airwallex' },
    { name: 'WeChat JSAPI success', method: 'wxpay', mobile: true, result: { result_type: 'jsapi_ready', jsapi: { appId: 'app-42', nonceStr: 'nonce-42' } }, expect: 'success' },
    { name: 'WeChat OAuth', method: 'wxpay', mobile: true, result: { result_type: 'oauth_required', oauth: { authorize_url: '/api/wechat/oauth?redirect=%2Fpurchase' } }, expect: 'oauth' },
  ]
  afterEach(() => { activeSubscriptionState.items = []; deviceState.mobile = true })
  it.each(cases)('$name launches once with accepted quote identity', async scenario => {
    quoteSubscription.mockReset().mockResolvedValue({ data: { quote_id: 'quote-launch', expires_at: '2099-01-01', order_amount: 12.34, projected: { active_lot_count: 2, daily_limit_usd: 90, expires_at: '2099-02-01' } } })
    const method = checkoutInfoFixture().data.methods.wxpay
    const wrapper = await mountSubscriptionConfirm({ mobile: scenario.mobile, checkout: { methods: { wxpay: method, [scenario.method]: method } } })
    wrapper.findComponent(PaymentMethodSelector).vm.$emit('select', scenario.method)
    await flushPromises()
    createOrder.mockResolvedValue({ order_id: 42, amount: 12.34, pay_amount: 12.34, fee_rate: 0, expires_at: '2099-01-01', out_trade_no: 'trade-42', resume_token: 'resume-42', ...scenario.result })
    bridgeInvoke.mockImplementation((_action, _payload, callback) => callback({ err_msg: 'get_brand_wcpay_request:ok' }))
    ;(window as Window & { WeixinJSBridge?: { invoke: typeof bridgeInvoke } }).WeixinJSBridge = { invoke: bridgeInvoke }
    routerResolve.mockImplementation(route => ({ href: `${route.path}?order_id=42` }))
    const originalLocation = window.location
    const location = { href: 'http://localhost/purchase', origin: 'http://localhost' }
    Object.defineProperty(window, 'location', { configurable: true, value: location })
    const popup = vi.spyOn(window, 'open').mockReturnValue({ closed: false } as Window)
    try {
      await wrapper.findAll('button').find(button => button.text().includes('payment.createOrder'))!.trigger('click')
      await flushPromises()
      expect(createOrder).toHaveBeenCalledTimes(1)
      expect(createOrder.mock.calls[0][0]).toMatchObject({ operation: 'purchase', units: 1, quote_id: 'quote-launch', amount: 12.34, payment_type: scenario.method, is_mobile: scenario.mobile })
      if (scenario.expect === 'panel') expect(wrapper.html()).toContain('payment-status-panel-stub')
      if (scenario.expect === 'popup') expect(popup).toHaveBeenCalledTimes(1)
      if (scenario.expect === 'redirect') expect(location.href).toBe('https://pay.example/42')
      if (scenario.expect === 'stripe' || scenario.expect === 'airwallex') expect(location.href).toContain(`/payment/${scenario.expect}?order_id=42`)
      if (scenario.expect === 'success') expect(routerPush).toHaveBeenCalledWith(expect.objectContaining({ path: '/payment/result', query: expect.objectContaining({ order_id: '42', resume_token: 'resume-42' }) }))
      else expect(JSON.parse(window.localStorage.getItem(PAYMENT_RECOVERY_STORAGE_KEY)!)).toMatchObject({ orderId: 42, orderType: 'subscription', resumeToken: 'resume-42' })
      if (scenario.expect === 'oauth') {
        const redirect = new URL(location.href, 'http://localhost').searchParams.get('redirect')!
        const query = new URL(redirect, 'http://localhost').searchParams
        expect(query.get('quote_id')).toBe('quote-launch')
        expect(query.get('operation')).toBe('purchase')
        expect(query.get('units')).toBe('1')
      }
    } finally { popup.mockRestore(); Object.defineProperty(window, 'location', { configurable: true, value: originalLocation }); wrapper.unmount() }
  })
})


describe('legacy selected-unit checkout', () => {
  afterEach(() => { legacyOptionsState.value = {enabled:false,pools:[]}; activeSubscriptionState.items = [] })
  const subscriptions = [{id:277,status:'active',expires_at:'2099-09-27',contract:{mode:'legacy_daily'}}] as UserSubscription[]
  function enableLegacy() {
    legacyOptionsState.value = {enabled:true,pools:[{subscription_id:277,management_mode:'legacy_lots',lots:[
      {id:288,status:'active',expires_at:'2099-09-26T19:10:00+08:00',daily_limit_usd:45,plan_id:7,kind:'month',period_days:30,upgrade_plan_ids:[8]},
      {id:295,status:'active',expires_at:'2099-09-27T13:15:00+08:00',daily_limit_usd:45,plan_id:7,kind:'month',period_days:30,upgrade_plan_ids:[8]},
      {id:4,status:'expired',expires_at:'2000-01-01',daily_limit_usd:45,reason:'inactive',upgrade_plan_ids:[]}
    ]}]}
  }
  it('quotes selected lots and sends only the signed accepted price to payment', async () => {
    enableLegacy()
    quoteSubscription.mockResolvedValue({data:{quote_id:'legacy-signed',expires_at:'2099-01-01',order_amount:15,projected:{daily_limit_usd:90,remaining_usd:70,active_lot_count:2}}})
    const wrapper=await mountSubscriptionConfirm({subscriptions})
    expect(quoteSubscription).toHaveBeenLastCalledWith(expect.objectContaining({subscription_id:277,operation:'renew',entitlement_ids:[288,295],periods:1}))
    const checks=wrapper.findAll('input[type="checkbox"]')
    expect(checks).toHaveLength(2)
    await checks[1].setValue(false);await flushPromises()
    expect(quoteSubscription).toHaveBeenLastCalledWith(expect.objectContaining({entitlement_ids:[288]}))
    await checks[0].setValue(false);await flushPromises()
    expect(wrapper.text()).toContain('subscriptionRights.selectionRequired')
    expect(wrapper.findAll('button').find(b=>b.text().includes('payment.createOrder'))!.attributes('disabled')).toBeDefined()
    await checks[0].setValue(true);await flushPromises()
    createOrder.mockResolvedValue({order_id:123,amount:15,pay_amount:15,expires_at:'2099-01-01',qr_code:'synthetic',fee_rate:0})
    await wrapper.findAll('button').find(b=>b.text().includes('payment.createOrder'))!.trigger('click')
    expect(createOrder).toHaveBeenCalledWith(expect.objectContaining({quote_id:'legacy-signed',operation:'renew',plan_id:7}))
    wrapper.unmount()
  })
  it('distinguishes full-cycle additions from expiry-aligned stacking', async () => {
    enableLegacy()
    const wrapper=await mountSubscriptionConfirm({subscriptions})
    await wrapper.findAll('button').find(b=>b.text()==='subscriptionRights.legacyPurchase')!.trigger('click');await flushPromises()
    expect(quoteSubscription).toHaveBeenLastCalledWith(expect.objectContaining({subscription_id:277,operation:'purchase',units:1,entitlement_ids:undefined,expiry_anchor_entitlement_id:undefined}))
    await wrapper.findAll('button').find(b=>b.text()==='subscriptionRights.stack')!.trigger('click');await flushPromises()
    expect(quoteSubscription).toHaveBeenLastCalledWith(expect.objectContaining({operation:'stack',expiry_anchor_entitlement_id:288}))
    wrapper.unmount()
  })
  it('fails closed if the rollout switch is disabled during checkout', async () => {
    enableLegacy()
    const wrapper=await mountSubscriptionConfirm({subscriptions})
    legacyOptionsState.value={enabled:false,pools:[]}
    document.dispatchEvent(new Event('visibilitychange'));await flushPromises();await flushPromises()
    const pay=wrapper.findAll('button').find(b=>b.text().includes('payment.createOrder'))
    expect(pay?.attributes('disabled')).toBeDefined()
    expect(createOrder).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})

describe('legacy refresh preserves an explicit subset', () => {
  afterEach(() => {legacyOptionsState.value={enabled:false,pools:[]};activeSubscriptionState.items=[]})
  it('does not reselect excluded units when the page becomes visible', async () => {
    const lots=[288,295].map(id=>({id,status:'active',expires_at:'2099-01-01',daily_limit_usd:45,plan_id:7,kind:'month' as const,period_days:30,upgrade_plan_ids:[]}))
    legacyOptionsState.value={enabled:true,pools:[{subscription_id:277,management_mode:'legacy_lots',lots}]}
    const wrapper=await mountSubscriptionConfirm({subscriptions:[{id:277,status:'active',expires_at:'2099-01-01',contract:{mode:'legacy_daily'}} as UserSubscription]})
    await wrapper.findAll('input[type="checkbox"]')[1].setValue(false);await flushPromises()
    legacyOptionsState.value=JSON.parse(JSON.stringify(legacyOptionsState.value))
    document.dispatchEvent(new Event('visibilitychange'));await flushPromises();await flushPromises()
    expect((wrapper.findAll('input[type="checkbox"]')[1].element as HTMLInputElement).checked).toBe(false)
    expect(quoteSubscription).toHaveBeenLastCalledWith(expect.objectContaining({entitlement_ids:[288]}))
    wrapper.unmount()
  })
})
