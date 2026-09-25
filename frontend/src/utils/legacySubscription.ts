import type { LegacyManagedPool, LegacyManagementOptions, SubscriptionOperation, SubscriptionPlan } from '@/types/payment'
import { planPeriodDays } from './subscriptionV2'

export function legacyActions(plan: SubscriptionPlan, pool: LegacyManagedPool | undefined, now = Date.now()): SubscriptionOperation[] {
  if (!pool) return []
  const lots = pool.lots.filter(lot => !lot.reason && lot.status === 'active' && Date.parse(lot.expires_at) > now)
  const actions: SubscriptionOperation[] = []
  if (lots.some(lot => lot.period_days === planPeriodDays(plan))) actions.push('purchase')
  if (lots.some(lot => lot.plan_id === plan.id)) actions.push('stack', 'renew')
  if (lots.some(lot => lot.upgrade_plan_ids.includes(plan.id))) actions.push('upgrade')
  return actions
}
export function legacyPlanAvailable(plan: SubscriptionPlan, options?: LegacyManagementOptions, now = Date.now()): boolean {
  return !!options?.enabled && options.pools.some(pool => legacyActions(plan, pool, now).length > 0)
}
