import type { SubscriptionContract, UserSubscription } from '@/types'
import type { PaymentOrder, SubscriptionOperation, SubscriptionPlan } from '@/types/payment'

type Translate = (key: string, values?: Record<string, unknown>) => string

export function planPeriodDays(plan: Pick<SubscriptionPlan, 'validity_days' | 'validity_unit'>): number {
  const unit = plan.validity_unit.toLowerCase().replace(/s$/, '')
  return plan.validity_days * (unit === 'month' ? 30 : unit === 'week' ? 7 : 1)
}

export function subscriptionActions(plan: SubscriptionPlan, subscriptions: UserSubscription[], now = Date.now()): { actions: SubscriptionOperation[]; reason?: string } {
  const active = subscriptions.filter(s => (s.status === 'active' || s.status === 'suspended') && (!s.expires_at || Date.parse(s.expires_at) > now))
  const days = planPeriodDays(plan)
  const supportedTier = days === 7 ? [90, 180].includes(plan.daily_limit_usd ?? 0) : days === 30 && [45, 90, 180].includes(plan.daily_limit_usd ?? 0)
  if (plan.is_legacy_compat || !supportedTier) return { actions: [], reason: 'subscriptionRights.unavailablePlan' }
  if (!active.length) return { actions: ['purchase'] }
  if (active.some(sub => sub.status === 'suspended')) return { actions: [], reason: 'subscriptionRights.suspendedHint' }
  if (active.some(campaignOnly)) return { actions: [], reason: 'subscriptionRights.campaignCompatibilityHint' }
  if (active.length !== 1 || active[0].contract?.mode !== 'v2') return { actions: [], reason: 'subscriptionRights.compatibilityHint' }
  const contract = active[0].contract
  if (Date.parse(contract.expires_at) <= now) return { actions: [], reason: 'subscriptionRights.campaignCompatibilityHint' }
  if (contract.period_days !== days) return { actions: [], reason: 'subscriptionRights.sameTypeOnly' }
  if (plan.id === contract.plan_id) return { actions: ['stack', 'renew'] }
  if ((plan.daily_limit_usd ?? 0) > contract.unit_daily_usd) return { actions: ['upgrade'] }
  return { actions: [], reason: 'subscriptionRights.noDowngrade' }
}

// geili hook: the server's daily ledger projection is authoritative on every surface.
export function subscriptionQuota(sub: Pick<UserSubscription, 'contract' | 'quota_summary' | 'plan' | 'group' | 'daily_usage_usd' | 'weekly_usage_usd' | 'monthly_usage_usd'>) {
  const source = sub.quota_summary ?? sub.plan ?? sub.group
  const daily = sub.quota_summary ? sub.quota_summary.daily_limit_usd : sub.contract?.mode === 'v2' ? sub.contract.unit_daily_usd * sub.contract.quantity : source?.daily_limit_usd ?? null
  const dailyUsed = sub.quota_summary?.daily_usage_usd ?? sub.daily_usage_usd ?? 0
  const dailyOnly = !!sub.contract
  const weekly = dailyOnly ? null : source?.weekly_limit_usd ?? null
  const monthly = dailyOnly ? null : source?.monthly_limit_usd ?? null
  const weeklyUsed = sub.quota_summary?.weekly_usage_usd ?? sub.weekly_usage_usd ?? 0
  const monthlyUsed = sub.quota_summary?.monthly_usage_usd ?? sub.monthly_usage_usd ?? 0
  const limits = [[daily, dailyUsed], [weekly, weeklyUsed], [monthly, monthlyUsed]].filter(([limit]) => limit != null && limit > 0)
  const remaining = sub.quota_summary?.active_lot_count === 0 ? 0
    : sub.quota_summary?.remaining_usd !== undefined ? sub.quota_summary.remaining_usd
    : limits.length ? Math.max(0, Math.min(...limits.map(([limit, used]) => limit! - used!))) : null
  return { daily, weekly, monthly, dailyUsed, weeklyUsed, monthlyUsed, remaining }
}

export function contractLabel(contract: SubscriptionContract, t: Translate): string {
  if (contract.mode === 'legacy_daily') return t('subscriptionRights.compatibility')
  return t('subscriptionRights.contractLabel', { kind: t(`subscriptionRights.${contract.kind}`), daily: contract.unit_daily_usd, count: contract.quantity })
}

export function subscriptionOrderLabel(order: PaymentOrder, t: Translate): string {
  const operation = order.operation ?? order.subscription_mode ?? 'purchase'
  const count = operation === 'renew' ? order.periods ?? order.subscription_quantity ?? 1 : order.units ?? order.subscription_quantity ?? 1
  return t(`subscriptionRights.${operation}`) + (operation === 'upgrade' ? '' : ` · ${t(operation === 'renew' ? 'subscriptionRights.periods' : 'subscriptionRights.units', { count })}`)
}

export function isPaidSubscriptionReview(order: Pick<PaymentOrder, 'order_type' | 'status' | 'paid_at'>): boolean {
  return order.order_type === 'subscription' && order.status === 'FAILED' && !!order.paid_at
}

export function subscriptionRefreshDelay(subscriptions: UserSubscription[], now = Date.now()): number | null {
  const boundaries = subscriptions.flatMap(sub => [sub.quota_summary?.daily_reset_at, sub.quota_summary?.next_expiry_at, sub.contract?.expires_at, sub.expires_at])
    .filter((at): at is string => typeof at === 'string')
    .map(Date.parse).filter(at => Number.isFinite(at) && at > now)
  return boundaries.length ? Math.min(2_147_483_647, Math.max(100, Math.min(...boundaries) - now + 100)) : null
}

export function campaignOnly(sub: Pick<UserSubscription, 'entitlements'>): boolean {
  return !!sub.entitlements?.length && sub.entitlements.every(lot => lot.source_type === 'campaign')
}

export function subscriptionLabel(sub: Pick<UserSubscription, 'entitlements' | 'contract' | 'plan' | 'group' | 'group_id'>, t: Translate): string {
  if (campaignOnly(sub)) return t('subscriptionRights.campaignGift')
  return sub.contract ? contractLabel(sub.contract, t) : sub.plan?.name || sub.group?.name || `Group #${sub.group_id}`
}
