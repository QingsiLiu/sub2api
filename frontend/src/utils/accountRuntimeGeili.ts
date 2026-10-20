import type { Account, AccountRuntimeStateGeili } from '@/types'

// Keep legacy status queries intact. The new UI filters are a separate API field.
export function accountAvailabilityFiltersGeili<T extends { status?: string }>(filters?: T) {
  if (!filters) return filters
  const availability = filters.status === 'geili_balance_stopped' ? 'balance_stopped'
    : filters.status === 'geili_temporary_stopped' ? 'temporarily_stopped' : undefined
  if (!availability) return filters
  const { status: _status, ...rest } = filters
  return { ...rest, availability_status: availability }
}

// The switch represents administrator intent. An error stop stays armed for
// automatic recovery until the administrator explicitly turns this switch off.
export function accountSchedulingEnabledGeili(account: Account): boolean {
  if (account.runtime_state?.stop_reason === 'manual') return false
  if (account.status === 'error') return true
  return account.status === 'active' && account.schedulable
}

export function accountRuntimeStateGeili(account: Account): AccountRuntimeStateGeili['state'] {
  if (account.runtime_state?.stop_reason === 'manual') return 'stopped'
  if (account.runtime_state?.state && account.runtime_state.state !== 'balance_stopped') return account.runtime_state.state
  if (account.status === 'error') return 'temporarily_stopped'
  if (account.status !== 'active' || !account.schedulable) return 'stopped'
  const now = Date.now()
  if ([account.rate_limit_reset_at, account.overload_until, account.temp_unschedulable_until]
    .some(value => value && new Date(value).getTime() > now)) return 'temporarily_stopped'
  return 'enabled'
}
