import { describe, expect, it } from 'vitest'
import { buildCreateOrderPayload, decidePaymentLaunch, readPaymentRecoverySnapshot } from '../paymentFlow'
import type { CreateOrderResult, SubscriptionOperation } from '@/types/payment'

const operations: Array<{ operation: SubscriptionOperation; units?: number; periods?: number }> = [{ operation: 'purchase', units: 3 }, { operation: 'stack', units: 2 }, { operation: 'renew', periods: 4 }, { operation: 'upgrade' }]
const flows = [
  { name: 'desktop Alipay QR', method: 'alipay', mobile: false, result: { qr_code: 'qr-alipay' }, kind: 'qr_waiting' },
  { name: 'mobile WeChat QR', method: 'wxpay', mobile: true, result: { qr_code: 'qr-wxpay' }, kind: 'qr_waiting' },
  { name: 'desktop redirect', method: 'alipay', mobile: false, result: { pay_url: 'https://pay.example/redirect' }, kind: 'redirect_waiting' },
  { name: 'mobile redirect', method: 'alipay', mobile: true, result: { pay_url: 'https://pay.example/redirect' }, kind: 'redirect_waiting' },
  { name: 'WeChat OAuth', method: 'wxpay', mobile: true, wechat: true, result: { result_type: 'oauth_required', oauth: { authorize_url: 'https://pay.example/oauth' } }, kind: 'wechat_oauth' },
  { name: 'WeChat JSAPI', method: 'wxpay', mobile: true, wechat: true, result: { result_type: 'jsapi_ready', jsapi: { appId: 'app', nonceStr: 'nonce' } }, kind: 'wechat_jsapi' },
  { name: 'Stripe desktop Alipay', method: 'alipay', mobile: false, result: { client_secret: 'cs-test' }, kind: 'stripe_popup' },
  { name: 'Stripe mobile', method: 'stripe', mobile: true, result: { client_secret: 'cs-test' }, kind: 'stripe_route' },
  { name: 'Airwallex mobile', method: 'airwallex', mobile: true, result: { client_secret: 'secret', intent_id: 'intent-test' }, kind: 'airwallex_route' },
  { name: 'Alipay deep link', method: 'alipay', mobile: true, result: { qr_code: 'https://qr.example/42', alipay_mobile_precreate_deep_link: true }, kind: 'alipay_deep_link' },
]

describe('subscription operation and gateway launch matrix', () => {
  it.each(flows.flatMap(flow => operations.map(operation => ({ ...flow, ...operation }))))('$name retains accepted $operation identity and resumable order', scenario => {
    const acceptedQuote = `quote-${scenario.operation}`
    const payload = buildCreateOrderPayload({ amount: 13.37, paymentType: scenario.method, orderType: 'subscription', planId: 7, quoteId: acceptedQuote, ...scenario, isMobile: scenario.mobile, isWechatBrowser: scenario.wechat ?? false })
    expect(payload).toMatchObject({ quote_id: acceptedQuote, operation: scenario.operation, amount: 13.37, plan_id: 7, payment_type: scenario.method })
    expect(payload.units).toBe(scenario.units)
    expect(payload.periods).toBe(scenario.periods)
    expect(payload.subscription_quantity).toBeUndefined()
    const result = { order_id: 42, amount: 13.37, pay_amount: 13.37, fee_rate: 0, expires_at: '2099-01-01', resume_token: 'resume-42', ...scenario.result } as CreateOrderResult
    const decision = decidePaymentLaunch(result, { visibleMethod: scenario.method, orderType: 'subscription', isMobile: scenario.mobile, isWechatBrowser: scenario.wechat ?? false, stripePopupUrl: '/payment/stripe?order_id=42', stripeRouteUrl: '/payment/stripe?order_id=42', airwallexRouteUrl: '/payment/airwallex?order_id=42' })
    expect(decision.kind).toBe(scenario.kind)
    expect(decision.recovery).toMatchObject({ orderId: 42, orderType: 'subscription', amount: 13.37, resumeToken: 'resume-42', paymentType: scenario.method })
    expect(readPaymentRecoverySnapshot(JSON.stringify(decision.recovery), { resumeToken: 'resume-42' })).toMatchObject({ orderId: 42, orderType: 'subscription' })
    expect(readPaymentRecoverySnapshot(JSON.stringify(decision.recovery), { resumeToken: 'different-order' })).toBeNull()
  })
})
