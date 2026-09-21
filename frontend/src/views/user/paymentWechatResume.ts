import type { LocationQuery, LocationQueryRaw } from 'vue-router'
import type { SubscriptionOperation, SubscriptionPlan } from '@/types/payment'
import { normalizeVisibleMethod } from '@/components/payment/paymentFlow'

export interface ParsedWechatResumeRoute {
  orderAmount: number
  orderType: 'balance' | 'subscription'
  paymentType: string
  planId?: number
  subscriptionMode?: SubscriptionOperation
  operation?: SubscriptionOperation
  units?: number
  periods?: number
  quoteId?: string
  subscriptionQuantity?: number
  openid?: string
  wechatResumeToken?: string
}

function readQueryString(query: LocationQuery, key: string): string {
  const value = query[key]
  if (Array.isArray(value)) {
    return typeof value[0] === 'string' ? value[0] : ''
  }
  return typeof value === 'string' ? value : ''
}

export function hasWechatResumeQuery(query: LocationQuery): boolean {
  if (readQueryString(query, 'wechat_resume') === '1') {
    return true
  }
  return readQueryString(query, 'wechat_resume_token') !== ''
    || readQueryString(query, 'openid') !== ''
}

export function parseWechatResumeRoute(
  query: LocationQuery,
  plans: SubscriptionPlan[],
  fallbackBalanceAmount: number,
): ParsedWechatResumeRoute | null {
  if (!hasWechatResumeQuery(query)) {
    return null
  }

  const wechatResumeToken = readQueryString(query, 'wechat_resume_token')
  const paymentType = normalizeVisibleMethod(readQueryString(query, 'payment_type')) || 'wxpay'
  const planId = Number.parseInt(readQueryString(query, 'plan_id'), 10)
  const hasPlanId = Number.isFinite(planId) && planId > 0
  const modeRaw = readQueryString(query, 'subscription_mode')
  const subscriptionMode = ['purchase', 'stack', 'renew', 'upgrade'].includes(modeRaw) ? modeRaw as SubscriptionOperation : undefined
  const operationRaw = readQueryString(query, 'operation')
  const v2 = {
    operation: ['purchase', 'stack', 'renew', 'upgrade'].includes(operationRaw) ? operationRaw as SubscriptionOperation : undefined,
    units: Number.parseInt(readQueryString(query, 'units'), 10) || undefined,
    periods: Number.parseInt(readQueryString(query, 'periods'), 10) || undefined,
    quoteId: readQueryString(query, 'quote_id') || undefined,
  }
  const subscriptionQuantity = Number.parseInt(readQueryString(query, 'subscription_quantity'), 10)
  const orderType = readQueryString(query, 'order_type') === 'subscription' || hasPlanId
    ? 'subscription'
    : 'balance'

  if (wechatResumeToken) {
    return {
      wechatResumeToken,
      paymentType,
      orderType,
      orderAmount: 0,
      planId: hasPlanId ? planId : undefined,
      subscriptionMode,
      ...v2,
      subscriptionQuantity: Number.isFinite(subscriptionQuantity) && subscriptionQuantity > 0 ? subscriptionQuantity : undefined,
    }
  }

  const openid = readQueryString(query, 'openid')
  if (!openid) {
    return null
  }

  const rawAmount = Number.parseFloat(readQueryString(query, 'amount'))
  const orderAmount = Number.isFinite(rawAmount) && rawAmount > 0
    ? rawAmount
    : (orderType === 'subscription'
      ? (plans.find(plan => plan.id === planId)?.price ?? 0)
      : fallbackBalanceAmount)

  return {
    openid,
    paymentType,
    orderType,
    orderAmount,
    planId: hasPlanId ? planId : undefined,
    subscriptionMode,
    ...v2,
    subscriptionQuantity: Number.isFinite(subscriptionQuantity) && subscriptionQuantity > 0 ? subscriptionQuantity : undefined,
  }
}

export function stripWechatResumeQuery(query: LocationQuery): LocationQueryRaw {
  const nextQuery: LocationQueryRaw = { ...query }
  delete nextQuery.wechat_resume
  delete nextQuery.wechat_resume_token
  delete nextQuery.openid
  delete nextQuery.state
  delete nextQuery.scope
  delete nextQuery.payment_type
  delete nextQuery.amount
  delete nextQuery.order_type
  delete nextQuery.plan_id
  delete nextQuery.subscription_mode
  delete nextQuery.operation
  delete nextQuery.units
  delete nextQuery.periods
  delete nextQuery.quote_id
  delete nextQuery.subscription_quantity
  return nextQuery
}
