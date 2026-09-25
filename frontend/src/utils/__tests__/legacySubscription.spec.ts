import { describe,expect,it } from 'vitest'
import { legacyActions,legacyPlanAvailable } from '../legacySubscription'
import type { LegacyManagedPool,SubscriptionPlan } from '@/types/payment'
const plan={id:7,validity_days:7,validity_unit:'day',daily_limit_usd:90} as SubscriptionPlan
const pool:LegacyManagedPool={subscription_id:277,management_mode:'legacy_lots',lots:[{id:288,status:'active',expires_at:'2099-01-01',plan_id:7,period_days:7,kind:'week',daily_limit_usd:90,upgrade_plan_ids:[8]}]}
describe('legacy subscription action gates',()=>{
 it('allows selected renewal, both additions and explicitly offered upgrades',()=>{
  expect(legacyActions(plan,pool)).toEqual(['purchase','stack','renew'])
  expect(legacyActions({...plan,id:8,daily_limit_usd:180},pool)).toEqual(['purchase','upgrade'])
 })
 it('rejects absent, suspended, expired, gift and unrecognized units',()=>{
  expect(legacyActions(plan,undefined)).toEqual([])
  for(const patch of [{status:'suspended'},{expires_at:'2000-01-01'},{reason:'gift'},{reason:'unrecognized'}]){
   expect(legacyActions(plan,{...pool,lots:[{...pool.lots[0],...patch}]})).toEqual([])
  }
 })
 it('does not block a normal unit due to an unsupported sibling',()=>{
  expect(legacyActions(plan,{...pool,lots:[...pool.lots,{...pool.lots[0],id:4,reason:'unrecognized'}]})).toContain('renew')
 })
 it('fails closed on the rollout flag and cross-type plans',()=>{
  expect(legacyPlanAvailable(plan,{enabled:false,pools:[pool]})).toBe(false)
  expect(legacyPlanAvailable(plan,{enabled:true,pools:[pool]})).toBe(true)
  expect(legacyActions({...plan,id:9,validity_days:30},pool)).toEqual([])
 })
})
